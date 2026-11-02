package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

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
