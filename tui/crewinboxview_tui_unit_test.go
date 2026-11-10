package tui

import (
	"context"
	"sync"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// fakeInbox serves inbox.list from items (the history, newest first, paged by before and limit; the
// undeliverable empty), counting the pages asked below a message, and takes every inbox.ack. Its
// items may change while it serves (setItems): new mail.
type fakeInbox struct {
	mu    sync.Mutex
	items []any
	acks  chan string
	older int // pages asked below a message (More)
}

func (f *fakeInbox) setItems(items ...any) { f.mu.Lock(); f.items = items; f.mu.Unlock() }

func (f *fakeInbox) olderPages() int { f.mu.Lock(); defer f.mu.Unlock(); return f.older }

func (*fakeInbox) Name() string { return "inbox" }

func (*fakeInbox) Errors() []module.PublicError { return nil }

func (f *fakeInbox) Verbs() []module.Verb {
	type listParams struct {
		Filter string `param:"filter,optional" doc:"the filter"`
		Before int64  `param:"before,optional" doc:"the seq to page below"`
		Limit  int64  `param:"limit,optional" doc:"at most"`
	}
	type ackParams struct {
		Message string `param:"message" doc:"the message"`
	}
	c := func(verb string, effect module.Effect) module.Contract {
		return module.Contract{Verb: verb, Since: 1, Scope: module.ScopeLocalPeer, Audience: module.AudienceUser, Effect: effect,
			Result: "{}", Doc: "a cell's " + verb}
	}
	return []module.Verb{
		module.Handle(c("inbox.list", module.EffectRead), func(_ context.Context, p listParams) (map[string]any, error) {
			f.mu.Lock()
			defer f.mu.Unlock()
			if p.Filter != "" {
				return map[string]any{"items": []any{}}, nil
			}
			var page []any
			if p.Before != 0 {
				f.older++
			}
			for _, x := range f.items {
				if m := x.(map[string]any); p.Before == 0 || m["seq"].(int64) < p.Before {
					page = append(page, x)
				}
				if len(page) == int(p.Limit) {
					break
				}
			}
			return map[string]any{"items": page}, nil
		}),
		module.Handle(c("inbox.ack", module.EffectWrite), func(_ context.Context, p ackParams) (struct{}, error) {
			f.acks <- p.Message
			return struct{}{}, nil
		}),
	}
}
