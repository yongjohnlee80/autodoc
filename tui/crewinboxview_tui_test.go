package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// fakeInbox serves inbox.list from items (the history; the undeliverable empty) and takes every
// inbox.ack, counting them.
type fakeInbox struct {
	items []any
	acks  chan string
}

func (*fakeInbox) Name() string                 { return "inbox" }
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
			if p.Filter != "" || p.Before != 0 {
				return map[string]any{"items": []any{}}, nil
			}
			return map[string]any{"items": f.items}, nil
		}),
		module.Handle(c("inbox.ack", module.EffectWrite), func(_ context.Context, p ackParams) (struct{}, error) {
			f.acks <- p.Message
			return struct{}{}, nil
		}),
	}
}

// On screen, against a daemon serving the inbox verbs: :inbox opens the dialog on the user's mail,
// its title, columns and rows drawn; the message under the cursor shows below the list; opening a
// report marks it read and the row says so.
func TestTheInboxShowsTheMailOnScreen(t *testing.T) {
	f := &fakeInbox{items: []any{mailItem(2, "message", "open"), mailItem(1, "request", "open")}, acks: make(chan string, 4)}
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{modules: []module.Module{f}})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	onLoop(r, func() bool { r.h.runEntry("crew.inbox"); return true })
	r.s.WaitFor(t, "the inbox drawn", func(sc string) bool {
		return strings.Contains(sc, "Inbox — All") && strings.Contains(sc, "SUBJECT") && strings.Contains(sc, "subject m002") &&
			strings.Contains(sc, "subject m001") && strings.Contains(sc, "request")
	})
	if err := onLoop(r, func() error {
		return r.h.reg.handlers["App.inboxShown"]([]qml.SpecValue{{Kind: argNum, Raw: "0"}})
	}); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "body of m002")
	if err := onLoop(r, func() error {
		return r.h.reg.handlers["App.inboxOpen"]([]qml.SpecValue{{Kind: argNum, Raw: "0"}})
	}); err != nil {
		t.Fatal(err)
	}
	if got := <-f.acks; got != "m002" {
		t.Fatalf("opened the report: acked %q, want m002", got)
	}
	r.s.WaitFor(t, "the report listed read", func(sc string) bool {
		for _, line := range strings.Split(sc, "\n") {
			if strings.Contains(line, "subject m002") {
				return strings.Contains(line, "read") && !strings.Contains(line, "●")
			}
		}
		return false
	})
}
