package tui

import (
	"context"
	"errors"
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
}

func newCrewDaemonForHost() *crewDaemon {
	clock := crewtest.NewClock()
	return &crewDaemon{m: crew.New(crewtest.NewMem(clock.Now)), clock: clock}
}

func (d *crewDaemon) call(ctx context.Context, verb string, p ...any) (any, error) {
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
	default:
		return nil, errors.New("no verb " + verb)
	}
	switch {
	case errors.Is(err, crew.ErrScopeBusy):
		return nil, &golibrpc.Error{Code: crew.CodeScopeBusy, Message: err.Error()}
	case errors.Is(err, crew.ErrStaleEpoch):
		return nil, &golibrpc.Error{Code: crew.CodeStaleEpoch, Message: err.Error()}
	case err != nil:
		return nil, err
	}
	return map[string]any{"epoch": epoch}, nil
}

// crewWindow is one window's crewCore: work runs at once, timers wait until fired, notices are kept.
type crewWindow struct {
	d       *crewDaemon
	timers  []func()
	notices []string
}

func (w *crewWindow) Call(ctx context.Context, verb string, p ...any) (any, error) {
	return w.d.call(ctx, verb, p...)
}
func (w *crewWindow) Go(fn func(context.Context) any, then func(any)) { then(fn(context.Background())) }
func (w *crewWindow) After(_ time.Duration, fn func())                { w.timers = append(w.timers, fn) }
func (w *crewWindow) Notify(msg string)                               { w.notices = append(w.notices, msg) }

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

// TestAWindowHoldsOneScopeAcrossReconnects (§4.3): the first connection claims a new scope and lists
// it; every later one reattaches with the same run token, the same scope and epoch; renewals keep the
// lease; a second window, its own run token, gets a scope of its own.
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

// TestACrashedWindowsScopeComesBack (§4.3): a window that ends without releasing keeps its scope
// until its lease lapses; then the next window claims it back, the oldest listed first, at the next
// epoch, before it would make a new one. A clean quit releases the scope at once.
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
