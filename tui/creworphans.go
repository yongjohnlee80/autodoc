package tui

import (
	"context"
	"fmt"
	"strings"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// THE ORPHANS OFFER (ADR 1793271500 §4.3, Q2 b): a window's scope whose holder is gone (its lease
// lapsed: the window crashed, or quit without a release reaching the daemon) keeps its agents'
// unhandled mail. Once this window holds its own scope, it asks the daemon for such scopes
// (crew.orphans) and offers each, one at a time: Adopt moves its mail under this window's agents
// (crew.adopt), Discard fails it and tells each sender (crew.discard), Keep leaves it for its window
// or a later one. It is asked once a run, at the first claim, never on a reconnect.

// crewOrphan is one orphan scope, as the offer says it.
type crewOrphan struct {
	scope    string
	profiles []string
	unread   int
}

// offerOrphans asks for the orphan scopes, other than this window's, and offers them in turn.
func (h *crewHost) offerOrphans() {
	if h.offered {
		return
	}
	h.offered = true
	own := h.scope
	h.c.Go(func(ctx context.Context) any {
		v, err := h.c.Call(ctx, "crew.orphans")
		if err != nil {
			return err
		}
		var out []crewOrphan
		for _, o := range asList(v) {
			m := asMap(o)
			if s := str(m, "scope"); s != "" && s != own {
				var ps []string
				for _, p := range asList(m["profiles"]) {
					if s, ok := p.(string); ok {
						ps = append(ps, s)
					}
				}
				out = append(out, crewOrphan{scope: s, profiles: ps, unread: int(num(m, "unread"))})
			}
		}
		return out
	}, func(v any) {
		orphans, ok := v.([]crewOrphan)
		if !ok {
			if err, _ := v.(error); err != nil && code(err) != golibrpc.CodeMethodNotFound {
				h.c.Notify("The crew's orphaned mail could not be listed: " + wireMessage(err))
			}
			return
		}
		h.offerNext(orphans)
	})
}

// offerNext offers orphans[0], then the rest once it is answered.
func (h *crewHost) offerNext(orphans []crewOrphan) {
	if len(orphans) == 0 {
		return
	}
	o, rest := orphans[0], orphans[1:]
	next := func() { h.offerNext(rest) }
	h.c.Ask(Question{
		Title: "Mail for a window that is gone",
		Text: fmt.Sprintf("%d unhandled message(s) wait for %s, agents of a window that is gone (%s). "+
			"Adopt moves them to this window's agents of the same names; Discard fails them and tells each sender; "+
			"Keep leaves them for that window, or a later one.", o.unread, strings.Join(o.profiles, ", "), o.scope),
		Yes:      "&Adopt",
		No:       "&Keep",
		Other:    "&Discard",
		OnYes:    func() { h.settleOrphan(o, "crew.adopt", next) },
		OnOther:  func() { h.settleOrphan(o, "crew.discard", next) },
		OnCancel: next,
	})
}

// settleOrphan adopts or discards o (verb) under the scope the window holds, says how it went, and
// then offers the next.
func (h *crewHost) settleOrphan(o crewOrphan, verb string, next func()) {
	scope, epoch, ok := h.held()
	if !ok {
		next()
		return
	}
	h.c.Go(func(ctx context.Context) any {
		_, err := h.c.Call(ctx, verb, o.scope, scope, h.run, epoch)
		return err
	}, func(v any) {
		err, _ := v.(error)
		switch {
		case v != nil:
			h.c.Notify(fmt.Sprintf("%s's mail was not settled: %s", o.scope, wireMessage(err)))
		case verb == "crew.adopt":
			h.c.Notify(fmt.Sprintf("Adopted %d message(s) for %s.", o.unread, strings.Join(o.profiles, ", ")))
		default:
			h.c.Notify(fmt.Sprintf("Discarded %d message(s) for %s; each sender is told.", o.unread, strings.Join(o.profiles, ", ")))
		}
		next()
	})
}
