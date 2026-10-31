package tui

import (
	"context"
	"errors"

	"github.com/yongjohnlee80/autodoc/core/inbox"
)

// The user's own sends to the window's agents go through the crew when a wake reaches the agent
// (docs/decisions/crew.md#the-users-own-sends): a mail from `user` that starts the agent's turn with
// no key pressed, nothing typed into its terminal. An agent no wake reaches is typed to instead, as
// before, with a notice that says why.

// errCrewUnreachable is a user's send the daemon refused because no wake reaches the agent now:
// nothing was sent, so the caller types the text instead.
var errCrewUnreachable = errors.New("no wake reaches the agent now")

// userSend sends body to codename's session in the window's scope as the user (inbox.send), kind
// "message" or "request" with subject, asking for a wake that reaches it. then hears nil once it was
// sent, errCrewUnreachable when nothing was sent because no wake reaches the agent (or the window
// holds no scope), or the daemon's other refusal. A fresh key per send: the user's sends never
// repeat one another.
func (h *crewHost) userSend(codename, kind, subject, body string, then func(error)) {
	scope, epoch, ok := h.held()
	if !ok || codename == "" {
		then(errCrewUnreachable)
		return
	}
	key := newSecret()
	h.c.Go(func(ctx context.Context) any {
		_, err := h.c.Call(ctx, "inbox.send", scope, h.run, epoch, []string{codename}, body, key, kind, subject, true)
		return err
	}, func(v any) {
		err, _ := v.(error)
		if code(err) == inbox.CodeUnreachable {
			err = errCrewUnreachable
		}
		then(err)
	})
}

// reachable tells then whether codename has a live session in the window's scope that a wake
// reaches now (crew.list's reachable), false when the window holds no scope or the daemon can't say.
func (h *crewHost) reachable(codename string, then func(bool)) {
	scope, _, ok := h.held()
	if !ok || codename == "" {
		then(false)
		return
	}
	h.c.Go(func(ctx context.Context) any {
		v, err := h.c.Call(ctx, "crew.list")
		if err != nil {
			return false
		}
		for _, e := range asList(v) {
			m := asMap(e)
			if str(m, "codename") == codename && str(m, "scope") == scope {
				r, _ := m["reachable"].(bool)
				return r
			}
		}
		return false
	}, func(v any) {
		r, _ := v.(bool)
		then(r)
	})
}
