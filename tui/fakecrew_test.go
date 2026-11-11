package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	corecrew "github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/msgpack"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// The crew mail's test fakes the host's cells share, copied from tui/crew's cells (its own stay
// there): the crew window and daemon, the bridge cell, the inbox dialog's item, and the daemon's
// inbox module the whole-program cells serve (fakeInbox).

// crewDaemon answers a window's crew verbs with a real corecrew.Manager on the in-memory store, its
// refusals as the daemon's codes.
type crewDaemon struct {
	m       *corecrew.Manager
	mem     *crewtest.Mem // m's store
	clock   *crewtest.Clock
	failing string // a scope whose claims fail as a store failure would
	none    bool   // a daemon before crew: every crew verb is unknown
	refuse  error  // what crew.register answers instead of registering, when set
	// orphans is what crew.orphans answers; settled each crew.adopt and crew.discard, its verb then
	// its params; settleErr what they answer, when set
	orphans   []any
	settled   [][]any
	settleErr error
	// todos, when set, answers the todos verbs, in their wire form: the stores a window's assignment
	// feed reads (crewtodos_test.go)
	todos *todo.Manager
}

func newCrewDaemonForHost() *crewDaemon {
	clock := crewtest.NewClock()
	mem := crewtest.NewMem(clock.Now)
	return &crewDaemon{m: corecrew.New(mem), clock: clock, mem: mem}
}

// sessionEnded reports whether the daemon's store has session id ended.
func (d *crewDaemon) sessionEnded(t *testing.T, id string) bool {
	t.Helper()
	ended := false
	if err := d.mem.Read(context.Background(), func(tx corecrew.Tx) error {
		s, ok, err := tx.Session(id)
		ended = ok && s.Ended != nil
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return ended
}

func (d *crewDaemon) call(ctx context.Context, verb string, p ...any) (any, error) {
	if d.none {
		return nil, &golibrpc.Error{Code: golibrpc.CodeMethodNotFound, Message: "unknown method: " + verb}
	}
	if d.todos != nil && strings.HasPrefix(verb, "todos.") {
		for _, v := range d.todos.Verbs() {
			if v.Contract.Verb == verb {
				r, err := v.Call(ctx, p)
				if err != nil {
					return nil, err
				}
				return module.Wire(r)
			}
		}
	}
	if verb == "crew.claim" && d.failing != "" && p[0] == d.failing {
		return nil, errors.New("the store is unavailable")
	}
	var epoch int64
	var err error
	switch verb {
	case "crew.claim":
		epoch, err = d.m.Claim(ctx, p[0].(string), p[1].(string), p[2].(string))
	case "crew.renew":
		err = d.m.Renew(ctx, p[0].(string), p[1].(string), p[2].(int64))
	case "crew.release":
		err = d.m.Release(ctx, p[0].(string), p[1].(string), p[2].(int64))
	case "crew.register":
		if d.refuse != nil {
			return nil, d.refuse
		}
		reg, err := d.m.Register(ctx, corecrew.Registration{Scope: p[0].(string), Run: p[1].(string), Epoch: p[2].(int64),
			Codename: p[3].(string), Kind: p[4].(string), Workspace: p[5].(string), Instance: p[6].(string), Secret: p[7].(string),
			Prompts: p[8].(bool), MailVia: optString(p, 9)})
		if err != nil {
			return nil, crewWire(err)
		}
		return map[string]any{"session": reg.Session, "profile": reg.Profile}, nil
	case "crew.orphans":
		return d.orphans, nil
	case "crew.list": // as the wire decodes it: a list of objects
		entries, err := d.m.List(ctx)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(entries)
		var out []any
		err = json.Unmarshal(b, &out)
		return out, err
	case "crew.adopt", "crew.discard":
		d.settled = append(d.settled, append([]any{verb}, p...))
		return map[string]any{}, d.settleErr
	case "crew.exit":
		err = d.m.Exit(ctx, p[0].(string), p[1].(string), p[2].(int64), p[3].(string), p[4].(string))
	default:
		return nil, errors.New("no verb " + verb)
	}
	if err != nil {
		return nil, crewWire(err)
	}
	return map[string]any{"epoch": epoch}, nil
}

// crewWire is err as the daemon answers it: a crew refusal by its code, as the module maps it.
func crewWire(err error) error {
	for _, m := range []struct {
		err  error
		code int64
	}{{corecrew.ErrScopeBusy, corecrew.CodeScopeBusy}, {corecrew.ErrStaleEpoch, corecrew.CodeStaleEpoch}, {corecrew.ErrCodenameBusy, corecrew.CodeCodenameBusy},
		{corecrew.ErrInstanceReused, corecrew.CodeInstanceReused}, {corecrew.ErrBadCredential, corecrew.CodeBadCredential}} {
		if errors.Is(err, m.err) {
			return &golibrpc.Error{Code: m.code, Message: err.Error()}
		}
	}
	return err
}

// crewWindow is one window's crewCore: work runs at once, timers wait until fired, notices are kept.
type crewWindow struct {
	skipHold int  // with hold: the Go calls answered at once before the ones held
	marked   bool // Tr and Trf answer the marked test language (markedText), else the English catalog's words
	d        *crewDaemon
	timers   []func()
	notices  []string
	hold     bool     // work waits in held until release, as a call in flight does
	late     bool     // with hold, the work runs at once and only its answer waits, as a lost-then-late reply
	held     []func() // the held work's answers
	store    string   // the store the daemon serves, "store-1" when unset
	socket   string   // the daemon's address, "/run/test/d.sock" when unset
	calls    []string // the verbs called, in order
	// inboxSend, when set, answers inbox.send (the user's doorbell) instead of the daemon: the
	// messaging cells choose whether it fired (agentmessaging_test.go)
	inboxSend func(p []any) (any, error)
	// inboxForward, when set, answers inbox.forward (an assignment a peer made, sent by the daemon
	// from its maker) instead of the daemon (crewtodos_test.go)
	inboxForward func(p []any) (any, error)
	// crewList, when set, answers crew.list instead of the daemon: the messaging cells choose what
	// it says of a session's prompts
	crewList func() (any, error)
	// fallbacks and fallbackDone, when set, answer mail.fallbacks and mail.fallback_done: unset, the
	// daemon is one before protocol 42, which has neither (crewfallback_test.go)
	fallbacks, fallbackDone func(p []any) (any, error)
	card                    *feature.Asker // the question card, made at the first question
	// lost is how many of a verb's next answers are lost after the daemon committed the call, as a
	// dropped connection loses them; down how many of its next calls fail before reaching it
	lost, down map[string]int
}

func (w *crewWindow) Call(ctx context.Context, verb string, p ...any) (any, error) {
	w.calls = append(w.calls, verb)
	// the params as the real client sends them: one the wire can't carry fails here, as it does there
	if _, err := msgpack.Marshal(p); err != nil {
		return nil, fmt.Errorf("rpc: encode: %w", err)
	}
	if w.down[verb] > 0 {
		w.down[verb]--
		return nil, io.ErrUnexpectedEOF
	}
	if verb == "inbox.send" && w.inboxSend != nil {
		return w.inboxSend(p)
	}
	if verb == "inbox.forward" && w.inboxForward != nil {
		return w.inboxForward(p)
	}
	if verb == "crew.list" && w.crewList != nil {
		return w.crewList()
	}
	if hook := map[string]func([]any) (any, error){"mail.fallbacks": w.fallbacks, "mail.fallback_done": w.fallbackDone}[verb]; hook != nil {
		return hook(p)
	} else if verb == "mail.fallbacks" || verb == "mail.fallback_done" {
		return nil, &golibrpc.Error{Code: golibrpc.CodeMethodNotFound, Message: "unknown method: " + verb}
	}
	v, err := w.d.call(ctx, verb, p...)
	if err == nil && w.lost[verb] > 0 {
		w.lost[verb]--
		return nil, io.ErrUnexpectedEOF
	}
	return v, err
}

func (w *crewWindow) Go(fn func(context.Context) any, then func(any)) {
	if w.hold && w.skipHold > 0 { // answered at once: a crew start's setup before the call a cell holds
		w.skipHold--
		then(fn(context.Background()))
		return
	}
	if w.hold && w.late {
		v := fn(context.Background())
		w.held = append(w.held, func() { then(v) })
		return
	}
	if w.hold {
		w.held = append(w.held, func() { then(fn(context.Background())) })
		return
	}
	then(fn(context.Background()))
}

// release answers the held work, as its calls returning would.
func (w *crewWindow) release() {
	h := w.held
	w.held, w.hold = nil, false
	for _, f := range h {
		f()
	}
}

func (w *crewWindow) After(_ time.Duration, fn func()) { w.timers = append(w.timers, fn) }

func (w *crewWindow) Notify(msg string) { w.notices = append(w.notices, msg) }

// AskWhenFree asks on a real question card (asker), as the window's: its replacement and its
// reentrancy are the card's own.
func (w *crewWindow) AskWhenFree(q feature.Question) {
	if w.card == nil {
		w.card = feature.NewAsker(newScripted("kb"))
	}
	w.card.AskWhenFree(q)
}

func (w *crewWindow) DaemonAddress() string {
	if w.socket != "" {
		return w.socket
	}
	return "/run/test/d.sock"
}

func (w *crewWindow) StoreID() string {
	if w.store != "" {
		return w.store
	}
	return "store-1"
}

// fire runs the timers set so far, as their time coming would.
func (w *crewWindow) fire() {
	ts := w.timers
	w.timers = nil
	for _, f := range ts {
		f()
	}
}

// markedText is the marked test language: a message's id and its arguments, so a cell sees that a
// notice came through the catalog, and which message it was.
func markedText(id string, args ...any) string {
	parts := []string{"⟪" + id + "⟫"}
	for _, a := range args {
		parts = append(parts, fmt.Sprint(a))
	}
	return strings.Join(parts, " ")
}

func (w *crewWindow) Tr(id string) string { return w.Trf(id) }

func (w *crewWindow) Trf(id string, args ...any) string {
	if w.marked {
		return markedText(id, args...)
	}
	return feature.Fill(englishText(id), args...)
}

// optString is p's i-th parameter as a string, "" when it isn't sent (an optional one).
func optString(p []any, i int) string {
	if i < len(p) {
		s, _ := p[i].(string)
		return s
	}
	return ""
}

// bridgeCellEnv makes this test binary a stand-in crew helper (TestMain): it waits to be signalled.
const bridgeCellEnv = "AUTODOC_TUI_BRIDGE_CELL"

func bridgeCell() {
	for {
		time.Sleep(time.Hour)
	}
}

// mailItem is a message as inbox.list answers it, at seq.
func mailItem(seq int64, kind, state string) map[string]any {
	id := fmt.Sprintf("m%03d", seq)
	return map[string]any{"message": id, "kind": kind, "from": "gold", "scope": "autodoc:w", "session": "s-gold",
		"subject": "subject " + id, "body": "body of " + id, "reply_to": "", "state": state,
		"created": time.Date(2026, 11, 2, 12, 0, int(seq), 0, time.UTC).Format(time.RFC3339), "delivery": "d-" + id, "seq": seq,
		"status": "queued", "why": ""}
}

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
