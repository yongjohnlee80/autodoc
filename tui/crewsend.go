package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/tui/feature"
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
//   - errHandedOver: it didn't ring, and the window's fallback job delivers it (crewfallback.go);
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
			_, err := h.c.Call(ctx, "inbox.send", scope, h.run, epoch, []any{codename}, body, key, kind, subject, true) // a list as []any: golib's msgpack encodes no []string
			return err
		}, func(v any) {
			err, _ := v.(error)
			switch {
			case err == nil:
				then(nil)
			case feature.Code(err) == inbox.CodeHandedOver: // its fallback job brings it back: never asked again, never typed here
				then(errHandedOver)
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
	switch feature.Code(err) {
	case inbox.CodeUnreachable, inbox.CodeTooLarge, mail.CodeNoRecipient, mail.CodeTooLarge,
		crew.CodeStaleEpoch, crew.CodeScopeBusy, golibrpc.CodeInvalidParams, golibrpc.CodeMethodNotFound:
		return true
	}
	return false
}

// peerAgents answers, on the loop, the live crew agents of every scope but the window's (peersOf):
// none while it holds no scope, or when the crew can't be asked.
func (h *crewHost) peerAgents(then func([]feature.AgentEntry)) {
	scope, _, ok := h.held()
	if !ok {
		then(nil)
		return
	}
	h.c.Go(func(ctx context.Context) any {
		v, err := h.c.Call(ctx, "crew.list")
		if err != nil {
			return nil
		}
		return v
	}, func(v any) { then(peersOf(v, scope)) })
}

// sendTask sends codename@scope (another host's agent) a request as the user, from the window's
// scope (inbox.send), keyed by key: never rung, so it is queued and woken as any mail, and a stopped
// agent gets its own host's fallback; the window never types into another host's terminal.
func (h *crewHost) sendTask(codename, scope, subject, body, key string, then func(error)) {
	own, epoch, ok := h.held()
	if !ok {
		then(fmt.Errorf("%w: the window holds no crew scope", errDoorbellNotSent))
		return
	}
	h.c.Go(func(ctx context.Context) any {
		_, err := h.c.Call(ctx, "inbox.send", own, h.run, epoch, []any{codename + "@" + scope}, body, key, mail.KindRequest, subject, false)
		return err
	}, func(v any) {
		err, _ := v.(error)
		then(err)
	})
}

// queueSend mails codename, the window's own agent, a message from the user as userSend does, but
// never fired: it waits, queued, for the agent's first receive (an agent that hasn't started yet,
// which nothing may be typed into). then hears nil once it is stored.
func (h *crewHost) queueSend(codename, kind, subject, body string, then func(error)) {
	scope, epoch, ok := h.held()
	if !ok || codename == "" {
		then(fmt.Errorf("%w: the window holds no crew scope", errDoorbellNotSent))
		return
	}
	key := newSecret()
	h.c.Go(func(ctx context.Context) any {
		_, err := h.c.Call(ctx, "inbox.send", scope, h.run, epoch, []any{codename}, body, key, kind, subject, false)
		return err
	}, func(v any) {
		err, _ := v.(error)
		then(err)
	})
}

// startedOf asks the crew whether codename's session in the window's scope is past its CLI's
// startup, for a kind that starts with its endpoint (crew.StartsWithItsEndpoint): only what proves
// the endpoint registered says so, crew.list's reachable, or a why that only a session with an
// endpoint has (wakes_failing, backoff_until). Every other why fails closed: host_gone is read
// before the endpoint is, so it says nothing of one. then hears it on the loop; a window holding no
// scope, a failed ask or no such session hears false: nothing is typed into what isn't known to
// have started.
func (h *crewHost) startedOf(codename string, then func(started bool)) {
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
		for _, e := range feature.AsList(v) {
			m := feature.AsMap(e)
			if feature.Str(m, "scope") == scope && feature.Str(m, "codename") == codename {
				reachable, _ := m["reachable"].(bool)
				why := feature.Str(m, "why")
				then(reachable || strings.HasPrefix(why, crew.WhyWakesFailing) || strings.HasPrefix(why, crew.WhyBackoffUntil))
				return
			}
		}
		then(false)
	})
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
		for _, e := range feature.AsList(v) {
			m := feature.AsMap(e)
			if feature.Str(m, "scope") == scope && feature.Str(m, "codename") == codename {
				reported, _ := m["prompts_reported"].(bool)
				then(reported)
				return
			}
		}
		then(false)
	})
}

// peersOf are crew.list's rows (rows) A may hand a task to beside the window's own agents: every
// live agent of a scope other than own.
func peersOf(rows any, own string) []feature.AgentEntry {
	var out []feature.AgentEntry
	for _, r := range feature.AsList(rows) {
		m := feature.AsMap(r)
		code, scope, presence := feature.Str(m, "codename"), feature.Str(m, "scope"), feature.Str(m, "presence")
		if code == "" || scope == "" || scope == own || scope == mail.GlobalScope || // the user's and the daemon's own
			(presence != crew.PresenceLive && presence != crew.PresenceReconnecting) {
			continue
		}
		project := scope
		if name, ok := strings.CutPrefix(scope, "autovim:"); ok {
			project = name
		}
		reachable, _ := m["reachable"].(bool)
		out = append(out, feature.AgentEntry{Ref: feature.AgentRef{Codename: code}, Name: code,
			Peer: &feature.PeerAgent{Scope: scope, Project: project, Reachable: reachable, Why: feature.Str(m, "why")}})
	}
	return out
}
