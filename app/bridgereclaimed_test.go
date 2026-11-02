package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestEachReclaimedHelperIsJournaled (docs/decisions/crew.md#crew-bridges): each helper the daemon's
// look ended is one sys.events crew.bridge_reclaimed, its detail saying what it was (kind and pid),
// whose (its owner, else the session it served), why it was an orphan, and how it ended: on SIGTERM,
// on SIGKILL, incomplete with how many processes still ran, or the kill's failure.
func TestEachReclaimedHelperIsJournaled(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	d := &moduleDeps{store: db, log: logger.Nop{}}
	for _, c := range []struct {
		r    crew.Reclaimed
		want string
	}{
		{crew.Reclaimed{Row: crew.BridgeRow{Kind: crew.BridgeAgy, PID: 4242, Owner: "gold@autovim:p", Session: "s-1", Why: crew.OrphanSessionEnded},
			Outcome: crew.KilledTerm}, "agy-bridge 4242 · gold@autovim:p · session ended · term"},
		{crew.Reclaimed{Row: crew.BridgeRow{Kind: crew.BridgeCodexServer, PID: 77, Session: "s-2", Why: crew.OrphanHostGone},
			Outcome: crew.KilledKill}, "codex-server 77 · s-2 · host gone · kill"},
		{crew.Reclaimed{Row: crew.BridgeRow{Kind: crew.BridgeAgy, PID: 9, Owner: "zen@autovim:p", Why: crew.OrphanAgyGone},
			Outcome: crew.KilledIncomplete, Survivors: 2}, "agy-bridge 9 · zen@autovim:p · language server gone · incomplete, 2 still running"},
		{crew.Reclaimed{Row: crew.BridgeRow{Kind: crew.BridgeAgy, PID: 10, Why: crew.OrphanNoEndpoint},
			Outcome: crew.KilledTerm, Err: errors.New("operation not permitted")}, "agy-bridge 10 · no endpoint names its socket · failed: operation not permitted"},
	} {
		bridgeReclaimed(d, c.r)
		evs, _, _, err := db.Events(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		last := evs[len(evs)-1]
		if last.Kind != "crew.bridge_reclaimed" || last.Detail != c.want {
			t.Errorf("journaled %q %q; want crew.bridge_reclaimed %q", last.Kind, last.Detail, c.want)
		}
	}
}

// reclaimedStore is a store for the reclaim cells, and its events of kind crew.bridge_reclaimed.
func reclaimedStore(t *testing.T) (*moduleDeps, func() []string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &moduleDeps{store: db, log: logger.Nop{}}, func() []string {
		t.Helper()
		evs, _, _, err := db.Events(ctx, 0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range evs {
			if e.Kind == "crew.bridge_reclaimed" {
				out = append(out, e.Detail)
			}
		}
		return out
	}
}

// TestOneLookOfTheReclaimLoop: each look of the daemon's reclaim loop (reclaimOnce) journals every
// helper the crew's Reclaim ended and keeps looking; a look that failed is warned of and the loop
// keeps looking; on a system whose processes it can't read (ErrNoBridges) the loop stops looking.
func TestOneLookOfTheReclaimLoop(t *testing.T) {
	deps, reclaimedEvents := reclaimedStore(t)
	d := &process{ctx: context.Background(), log: logger.Nop{}, deps: deps}
	now := time.Now()
	ended := []crew.Reclaimed{
		{Row: crew.BridgeRow{Kind: crew.BridgeAgy, PID: 1, Owner: "gold@autovim:p", Why: crew.OrphanSessionEnded}, Outcome: crew.KilledTerm},
		{Row: crew.BridgeRow{Kind: crew.BridgeCodexServer, PID: 2, Owner: "zen@autovim:p", Why: crew.OrphanHostGone}, Outcome: crew.KilledKill},
	}
	var asked time.Time
	keep := d.reclaimOnce(d.ctx, now, func(_ context.Context, at time.Time) ([]crew.Reclaimed, error) {
		asked = at
		return ended, nil
	})
	if got := reclaimedEvents(); !keep || !asked.Equal(now) || len(got) != 2 {
		t.Fatalf("a look that ended two helpers: keep %v, asked at %v (want %v), journaled %q; want both journaled, and the loop keeps looking",
			keep, asked, now, got)
	}
	if keep := d.reclaimOnce(d.ctx, now, func(context.Context, time.Time) ([]crew.Reclaimed, error) {
		return nil, errors.New("the scan failed")
	}); !keep || len(reclaimedEvents()) != 2 {
		t.Fatalf("a look that failed: keep %v, %d journaled; want the loop to keep looking, nothing journaled", keep, len(reclaimedEvents()))
	}
	if keep := d.reclaimOnce(d.ctx, now, func(context.Context, time.Time) ([]crew.Reclaimed, error) {
		return nil, crew.ErrNoBridges
	}); keep || len(reclaimedEvents()) != 2 {
		t.Fatalf("a system whose processes can't be read: keep %v, %d journaled; want the loop to stop looking", keep, len(reclaimedEvents()))
	}
}

// TestTheReclaimLoopEndsWithTheDaemon: the daemon's reclaim loop stops with the daemon (its stop,
// pushed when it starts, ends it and waits for it), before its next look.
func TestTheReclaimLoopEndsWithTheDaemon(t *testing.T) {
	deps, _ := reclaimedStore(t)
	d := &process{ctx: context.Background(), log: logger.Nop{}, deps: deps}
	d.reclaimBridges()
	stopped := make(chan struct{})
	go func() { d.stops.unwind(); close(stopped) }()
	select {
	case <-stopped:
	// NOTE: the bound is the property: a loop that ignored its stop would hold it a whole look away.
	case <-time.After(5 * time.Second):
		t.Fatal("the reclaim loop did not end within 5 s of the daemon's stop")
	}
}
