package tui

import (
	"context"
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// watchingInbox is fakeInbox that also serves the human feed, as the daemon's inbox module does, so
// the window's own crew inbox reads it and a cell drives the inbox's replies and senders as the
// program does: inbox.watch answers a snapshot of open (the open requests, no attention) at cursor
// 0, else waits up to its timeout for newMail, a report's change; inbox.focus keeps each session
// asked for in focus.
type watchingInbox struct {
	*fakeInbox
	mu      sync.Mutex
	open    []any         // the snapshot's items
	seq     int64         // the feed's cursor: newMail moves it on
	changed chan struct{} // closed, and made again, at each newMail
	focus   chan string   // each inbox.focus's session
}

func newWatchingInbox(f *fakeInbox, open ...any) *watchingInbox {
	return &watchingInbox{fakeInbox: f, open: open, seq: 1, changed: make(chan struct{}), focus: make(chan string, 4)}
}

// newMail is a report arriving: the feed's cursor moves on, past a change the window reads its
// inbox again for.
func (w *watchingInbox) newMail() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.seq++
	close(w.changed)
	w.changed = make(chan struct{})
}

func (w *watchingInbox) Verbs() []module.Verb {
	type watchParams struct {
		Cursor  int64  `param:"cursor" doc:"the last answer's cursor"`
		Timeout int64  `param:"timeout,optional" doc:"milliseconds to wait"`
		Page    string `param:"page,optional" doc:"a snapshot's next"`
	}
	type focusParams struct {
		Session string `param:"session" doc:"the session"`
	}
	c := func(verb string, effect module.Effect) module.Contract {
		return module.Contract{Verb: verb, Since: 1, Scope: module.ScopeLocalPeer, Audience: module.AudienceUser, Effect: effect,
			Result: "{}", Doc: "a cell's " + verb}
	}
	return append(w.fakeInbox.Verbs(),
		module.Handle(c("inbox.watch", module.EffectRead), func(ctx context.Context, p watchParams) (map[string]any, error) {
			w.mu.Lock()
			seq, changed, open := w.seq, w.changed, w.open
			w.mu.Unlock()
			if p.Cursor == 0 {
				return map[string]any{"cursor": seq, "snapshot": map[string]any{"items": open, "attention": []any{}, "next": ""}}, nil
			}
			if seq <= p.Cursor {
				select {
				case <-changed:
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(p.Timeout) * time.Millisecond):
					return map[string]any{"cursor": p.Cursor, "changes": []any{}}, nil
				}
				w.mu.Lock()
				seq = w.seq
				w.mu.Unlock()
			}
			return map[string]any{"cursor": seq, "changes": []any{map[string]any{"seq": seq, "kind": "report", "ref": "new"}}}, nil
		}),
		module.Handle(c("inbox.focus", module.EffectWrite), func(_ context.Context, p focusParams) (map[string]any, error) {
			w.focus <- p.Session
			return map[string]any{"session": p.Session, "owner": "another"}, nil
		}),
	)
}
