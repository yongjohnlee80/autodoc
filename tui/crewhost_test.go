package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// crewDaemon answers a window's crew verbs with a real crew.Manager on the in-memory store, its
// refusals as the daemon's codes.
type crewDaemon struct {
	m       *crew.Manager
	clock   *crewtest.Clock
	failing string // a scope whose claims fail as a store failure would
	none    bool   // a daemon before crew: every crew verb is unknown
	refuse  error  // what crew.register answers instead of registering, when set
	// orphans is what crew.orphans answers; settled each crew.adopt and crew.discard, its verb then
	// its params; settleErr what they answer, when set
	orphans   []any
	settled   [][]any
	settleErr error
}

func newCrewDaemonForHost() *crewDaemon {
	clock := crewtest.NewClock()
	return &crewDaemon{m: crew.New(crewtest.NewMem(clock.Now)), clock: clock}
}

func (d *crewDaemon) call(ctx context.Context, verb string, p ...any) (any, error) {
	if d.none {
		return nil, &golibrpc.Error{Code: golibrpc.CodeMethodNotFound, Message: "unknown method: " + verb}
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
		reg, err := d.m.Register(ctx, crew.Registration{Scope: p[0].(string), Run: p[1].(string), Epoch: p[2].(int64),
			Codename: p[3].(string), Kind: p[4].(string), Workspace: p[5].(string), Instance: p[6].(string), Secret: p[7].(string)})
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
	}{{crew.ErrScopeBusy, crew.CodeScopeBusy}, {crew.ErrStaleEpoch, crew.CodeStaleEpoch}, {crew.ErrCodenameBusy, crew.CodeCodenameBusy},
		{crew.ErrInstanceReused, crew.CodeInstanceReused}, {crew.ErrBadCredential, crew.CodeBadCredential}} {
		if errors.Is(err, m.err) {
			return &golibrpc.Error{Code: m.code, Message: err.Error()}
		}
	}
	return err
}

// crewWindow is one window's crewCore: work runs at once, timers wait until fired, notices are kept.
type crewWindow struct {
	marked  bool // Tr and Trf answer the marked test language (markedText), else the English catalog's words
	d       *crewDaemon
	timers  []func()
	notices []string
	hold    bool     // work waits in held until release, as a call in flight does
	late    bool     // with hold, the work runs at once and only its answer waits, as a lost-then-late reply
	held    []func() // the held work's answers
	store   string   // the store the daemon serves, "store-1" when unset
	socket  string   // the daemon's address, "/run/test/d.sock" when unset
	calls   []string // the verbs called, in order
	// inboxSend, when set, answers inbox.send (the user's doorbell) instead of the daemon: the
	// messaging cells choose whether it fired (agentmessaging_test.go)
	inboxSend func(p []any) (any, error)
	card      *asker // the question card, made at the first question
	// lost is how many of a verb's next answers are lost after the daemon committed the call, as a
	// dropped connection loses them; down how many of its next calls fail before reaching it
	lost, down map[string]int
}

func (w *crewWindow) Call(ctx context.Context, verb string, p ...any) (any, error) {
	w.calls = append(w.calls, verb)
	if w.down[verb] > 0 {
		w.down[verb]--
		return nil, io.ErrUnexpectedEOF
	}
	if verb == "inbox.send" && w.inboxSend != nil {
		return w.inboxSend(p)
	}
	v, err := w.d.call(ctx, verb, p...)
	if err == nil && w.lost[verb] > 0 {
		w.lost[verb]--
		return nil, io.ErrUnexpectedEOF
	}
	return v, err
}
func (w *crewWindow) Go(fn func(context.Context) any, then func(any)) {
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
func (w *crewWindow) Notify(msg string)                { w.notices = append(w.notices, msg) }

// AskWhenFree asks on a real question card (asker), as the window's: its replacement and its
// reentrancy are the card's own.
func (w *crewWindow) AskWhenFree(q Question) {
	if w.card == nil {
		w.card = newAsker(newScripted("kb"))
	}
	w.card.askWhenFree(q)
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

func listedScopes(t *testing.T, dir string) []string {
	t.Helper()
	es, _ := os.ReadDir(filepath.Join(dir, ".crew-scopes"))
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}

// TestAWindowHoldsOneScopeAcrossReconnects (docs/decisions/crew.md#scopes-and-fenced-ownership):
// the first connection claims a new scope and lists it; every later one reattaches with the same
// run token, the same scope and epoch; renewals keep the lease; a second window, its own run token,
// gets a scope of its own.
func TestAWindowHoldsOneScopeAcrossReconnects(t *testing.T) {
	d, dir := newCrewDaemonForHost(), t.TempDir()
	w := &crewWindow{d: d}
	h := newCrewHost(w, dir)
	h.Connected()
	scope, epoch, ok := h.held()
	if !ok || !strings.HasPrefix(scope, "autodoc:") || epoch != 1 {
		t.Fatalf("the first claim: %q epoch %d (%v), notices %v", scope, epoch, ok, w.notices)
	}
	if got := listedScopes(t, dir); len(got) != 1 || got[0] != scope {
		t.Fatalf("listed %v, want %s", got, scope)
	}
	h.Connected() // a reconnect
	if s, e, _ := h.held(); s != scope || e != epoch {
		t.Fatalf("after a reconnect: %s epoch %d, want the same %s epoch %d", s, e, scope, epoch)
	}
	d.clock.Advance(80 * time.Second)
	w.fire() // a renewal
	d.clock.Advance(80 * time.Second)
	other := newCrewHost(&crewWindow{d: d}, dir)
	other.Connected()
	if s, _, ok := other.held(); !ok || s == scope {
		t.Fatalf("a second window, its renewed neighbour's scope still held: %q (%v), want a scope of its own", s, ok)
	}
	if len(w.notices) != 0 {
		t.Errorf("notices %v, want none", w.notices)
	}
}

// TestACrashedWindowsScopeComesBack (docs/decisions/crew.md#scopes-and-fenced-ownership): a window
// that ends without releasing keeps its scope until its lease lapses; then the next window claims
// it back, the oldest listed first, at the next epoch, before it would make a new one. A clean quit
// releases the scope at once.
func TestACrashedWindowsScopeComesBack(t *testing.T) {
	d, dir := newCrewDaemonForHost(), t.TempDir()
	crashed := newCrewHost(&crewWindow{d: d}, dir)
	crashed.Connected()
	scope, epoch, _ := crashed.held()
	past := time.Now().Add(-time.Hour) // listed earlier than any other: the successor tries it first
	if err := os.Chtimes(filepath.Join(dir, ".crew-scopes", scope), past, past); err != nil {
		t.Fatal(err)
	}
	early := newCrewHost(&crewWindow{d: d}, dir)
	early.Connected()
	if s, _, _ := early.held(); s == scope {
		t.Fatal("a window took a crashed window's scope while its lease held")
	}
	d.clock.Advance(91 * time.Second)
	successor := newCrewHost(&crewWindow{d: d}, dir)
	successor.Connected()
	if s, e, _ := successor.held(); s != scope || e != epoch+1 {
		t.Fatalf("the successor: %s epoch %d, want the crashed window's %s at epoch %d", s, e, scope, epoch+1)
	}
	successor.Stopped() // a clean quit
	next := newCrewHost(&crewWindow{d: d}, dir)
	next.Connected()
	if s, _, _ := next.held(); s != scope {
		t.Fatalf("after a clean quit: %s, want the released %s at once", s, scope)
	}
}

// TestAStoreFailureMintsNoScope: a claim the daemon fails (not busy) leaves the window without a
// scope, says so, and tries again later: a failure never mints an identity in place of the listed
// scope it could not claim.
func TestAStoreFailureMintsNoScope(t *testing.T) {
	d, dir := newCrewDaemonForHost(), t.TempDir()
	crashed := newCrewHost(&crewWindow{d: d}, dir)
	crashed.Connected()
	scope, _, _ := crashed.held()
	d.clock.Advance(91 * time.Second)
	d.failing = scope
	w := &crewWindow{d: d}
	h := newCrewHost(w, dir)
	h.Connected()
	if _, _, ok := h.held(); ok || len(listedScopes(t, dir)) != 1 || len(w.notices) != 1 {
		t.Fatalf("a failed claim: held %v, listed %v, notices %v; want none, only the crashed window's, one", ok, listedScopes(t, dir), w.notices)
	}
	d.failing = ""
	w.fire() // the retry
	if s, _, ok := h.held(); !ok || s != scope {
		t.Fatalf("the retry after the store came back: %q (%v), want the listed %s", s, ok, scope)
	}
}

// TestALostScopeIsClaimedAgain: when another owner took the window's scope (a takeover while it was
// away), its next renewal is refused for a stale epoch, and the window claims a scope again and says
// it moved.
func TestALostScopeIsClaimedAgain(t *testing.T) {
	d, dir := newCrewDaemonForHost(), t.TempDir()
	w := &crewWindow{d: d}
	h := newCrewHost(w, dir)
	h.Connected()
	scope, _, _ := h.held()
	if _, err := d.m.Takeover(context.Background(), scope, "someone-else", ""); err != nil {
		t.Fatal(err)
	}
	w.fire() // the renewal, refused
	if s, _, ok := h.held(); !ok || s == scope {
		t.Fatalf("after losing %s: %q (%v), want a scope claimed again", scope, s, ok)
	}
	if len(w.notices) != 1 || !strings.Contains(w.notices[0], "lost its crew scope") {
		t.Fatalf("notices %v, want one saying the scope moved", w.notices)
	}
}

// TestADaemonWithoutCrewLeavesTheWindowQuiet: a daemon with no crew verbs (one before protocol 37, or
// a test's) leaves the window without a scope, says nothing and sets no retry: the next connection
// asks again.
func TestADaemonWithoutCrewLeavesTheWindowQuiet(t *testing.T) {
	d, dir := newCrewDaemonForHost(), t.TempDir()
	d.none = true
	w := &crewWindow{d: d}
	h := newCrewHost(w, dir)
	h.Connected()
	if _, _, ok := h.held(); ok || len(w.notices) != 0 || len(w.timers) != 0 || len(listedScopes(t, dir)) != 0 {
		t.Fatalf("a daemon without crew: held %v, notices %v, %d timers, listed %v; want none of them", ok, w.notices, len(w.timers), listedScopes(t, dir))
	}
	d.none = false
	h.Connected() // a newer daemon, after a restart
	if _, _, ok := h.held(); !ok {
		t.Fatal("the next connection, to a daemon with crew, claimed nothing")
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
	return fill(englishText(id), args...)
}
