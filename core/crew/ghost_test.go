package crew_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// ghostRig is wakeRig whose daemon started at the store's clock, so a host's lease can lapse in a
// cell (crew.WithStarted).
func ghostRig(t *testing.T) *wakeRig {
	r := &wakeRig{t: t, ctx: context.Background(), clock: crewtest.NewClock(), w: &fakeWaker{}}
	r.mem = crewtest.NewMem(r.clock.Now)
	r.m = crew.New(r.mem, crew.WithSessionEnder(&crewtest.Enders{}), crew.WithStarted(r.clock.Now()))
	r.s = r.m.NewScheduler("run1", map[string]crew.Waker{"fake": r.w}, crew.WithWakeClock(r.clock.Now),
		crew.WithWakeErrors(func(err error) { t.Errorf("scheduler: %v", err) }))
	e, err := r.m.Claim(r.ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	r.epoch = e
	return r
}

func (r *wakeRig) sessionRow(id string) crew.Session {
	r.t.Helper()
	var s crew.Session
	if err := r.mem.Read(r.ctx, func(tx crew.Tx) error { var err error; s, _, err = tx.Session(id); return err }); err != nil {
		r.t.Fatal(err)
	}
	return s
}

// A ghost (a session whose process is gone, left live because its host never sent crew.exit: a
// window that closed its tab before the fix, cloud-strife's finding 4) is superseded when the
// scope's current owner registers its codename again under another instance: the owner is the
// authority on its own agents and wouldn't start one it still runs. The old session ends
// ('takeover'), the new one is live and reachable, and the codename is never refused as busy. A
// registration from an epoch the owner no longer holds is still refused, by the guard.
func TestTheScopesOwnerSupersedesAGhostAtItsNextRegister(t *testing.T) {
	r := ghostRig(t)
	ghost := r.session("sephiroth") // instance i-sephiroth, live, its process long gone
	next, err := r.m.Register(r.ctx, crew.Registration{Scope: "autodoc:w", Run: "host", Epoch: r.epoch, Codename: "sephiroth",
		Kind: "fake", Workspace: "w", Instance: "i-sephiroth-2", Secret: strings.Repeat("t", 40)})
	if err != nil {
		t.Fatalf("the owner's next register of sephiroth: %v, want the ghost superseded", err)
	}
	old := r.sessionRow(ghost.Session)
	if next.Session == ghost.Session || next.Profile != ghost.Profile || old.Ended == nil || old.EndedWhy != crew.EndedTakeover {
		t.Fatalf("the new session %+v (the ghost %+v); the ghost now %+v, want it ended 'takeover'", next, ghost, old)
	}
	if e := r.entry("sephiroth"); e.Presence != crew.PresenceLive {
		t.Fatalf("sephiroth after the register: %+v, want live", e)
	}
	if _, err := r.m.Register(r.ctx, crew.Registration{Scope: "autodoc:w", Run: "host", Epoch: r.epoch - 1, Codename: "sephiroth",
		Kind: "fake", Workspace: "w", Instance: "i-sephiroth-3", Secret: strings.Repeat("u", 40)}); !errors.Is(err, crew.ErrStaleEpoch) {
		t.Fatalf("a register from an older epoch: %v, want ErrStaleEpoch", err)
	}
	if r.sessionRow(next.Session).Ended != nil {
		t.Fatal("a refused register ended the live session")
	}
}

// A session whose host is gone (its lease lapsed: a window killed, with no one left to send
// crew.exit) is not reachable, why host_gone, and no wake is sent to it: its endpoint is a dead
// process's. Its mail waits for the profile. Once its host renews, it is reachable again and woken.
func TestAGoneHostsSessionIsUnreachableAndNotWoken(t *testing.T) {
	r := ghostRig(t)
	s := r.session("tifa")
	r.clock.Advance(2 * time.Minute) // past the 90 s lease, and the daemon older than one
	e := r.entry("tifa")
	if e.Presence != crew.PresenceOffline || e.Reachable || e.Why != crew.WhyHostGone {
		t.Fatalf("tifa with her host gone: %+v, want offline, unreachable, why %s", e, crew.WhyHostGone)
	}
	r.look()
	if r.w.count() != 0 {
		t.Fatalf("a session whose host is gone was woken: %v", r.w.calls)
	}
	if err := r.mem.Read(r.ctx, func(tx crew.Tx) error {
		ok, err := r.m.Reachable(tx, s.Profile)
		if ok {
			t.Error("Reachable says a gone host's session can be woken")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.m.Renew(r.ctx, "autodoc:w", "host", r.epoch); err != nil {
		t.Fatal(err)
	}
	if e := r.entry("tifa"); !e.Reachable || e.Presence != crew.PresenceLive {
		t.Fatalf("tifa after her host renewed: %+v, want live and reachable", e)
	}
	r.clock.Advance(2 * time.Minute) // past the deferred try
	if err := r.m.Renew(r.ctx, "autodoc:w", "host", r.epoch); err != nil {
		t.Fatal(err)
	}
	r.look()
	if r.w.count() != 1 {
		t.Fatalf("after the host came back: %d wakes, want 1", r.w.count())
	}
}
