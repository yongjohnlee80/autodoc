package tui

import (
	"context"
	"fmt"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// The user's own sends to the window's agents go through the crew when the agent's doorbell rings
// (docs/decisions/crew.md#the-users-own-sends): a mail from `user` that starts the agent's turn with
// no key pressed, nothing typed into its terminal.

// userSendTries is how many times a user's send whose outcome is unknown (a lost answer, a dropped
// connection) is asked again, with the same key, userSendGap apart: the daemon answers a repeat
// with the first send's answer, so a doorbell that rang is never rung or typed twice.
const (
	userSendTries = 3
	// NOTE: can't announce: a lost answer or a dropped connection says nothing about when to ask
	// again, so the send is asked again after a fixed gap.
	userSendGap = time.Second
)

// userSend sends body to codename's session in the window's scope as the user (inbox.send with
// fire: rung, or nothing sent). then hears, on the loop:
//   - nil: it rang;
//   - an error wrapping errDoorbellNotSent: the daemon refused it for certain (no wake reaches
//     the agent, no scope or session, a stale epoch, a refused body, an older daemon), so the
//     caller may type it instead;
//   - any other error: its outcome is unknown after userSendTries asks, and the caller types
//     nothing, since it may have rung.
func (h *crewHost) userSend(codename, kind, subject, body string, then func(error)) {
	scope, epoch, ok := h.held()
	if !ok || codename == "" {
		then(fmt.Errorf("%w: the window holds no crew scope", errDoorbellNotSent))
		return
	}
	key := newSecret() // one key for every ask of this send
	var ask func(left int)
	ask = func(left int) {
		h.c.Go(func(ctx context.Context) any {
			_, err := h.c.Call(ctx, "inbox.send", scope, h.run, epoch, []string{codename}, body, key, kind, subject, true)
			return err
		}, func(v any) {
			err, _ := v.(error)
			switch {
			case err == nil:
				then(nil)
			case doorbellNotSent(err):
				then(fmt.Errorf("%w: %v", errDoorbellNotSent, err))
			case left > 1:
				h.c.After(userSendGap, func() { ask(left - 1) })
			default:
				then(err)
			}
		})
	}
	ask(userSendTries)
}

// doorbellNotSent reports whether err is the daemon's certain refusal of a user's send: nothing was
// stored, so nothing can arrive.
func doorbellNotSent(err error) bool {
	switch code(err) {
	case inbox.CodeUnreachable, inbox.CodeTooLarge, mail.CodeNoRecipient, mail.CodeTooLarge,
		crew.CodeStaleEpoch, crew.CodeScopeBusy, golibrpc.CodeInvalidParams, golibrpc.CodeMethodNotFound:
		return true
	}
	return false
}

// promptsReported asks the crew whether codename's session in the window's scope has its permission
// prompts reported now (crew.list's prompts_reported: a live session whose reporter is there, the
// daemon's rule). then hears it on the loop; a window holding no scope, a failed ask or no such
// session hears false.
func (h *crewHost) promptsReported(codename string, then func(reported bool)) {
	scope, _, ok := h.held()
	if !ok || codename == "" {
		then(false)
		return
	}
	h.c.Go(func(ctx context.Context) any {
		v, err := h.c.Call(ctx, "crew.list")
		if err != nil {
			return nil
		}
		return v
	}, func(v any) {
		for _, e := range asList(v) {
			m := asMap(e)
			if str(m, "scope") == scope && str(m, "codename") == codename {
				reported, _ := m["prompts_reported"].(bool)
				then(reported)
				return
			}
		}
		then(false)
	})
}
