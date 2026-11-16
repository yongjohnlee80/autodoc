package pgstore

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
)

// probeRan counts, beside annRan's candidate stages, the threshold probes a View sends: the read
// of the tenant's embeddings at OFFSET threshold − 1.
type probeRan struct {
	annRan
	probes int
}

func (h *probeRan) BeforeExec(ctx context.Context, q *dao.QueryInfo) error {
	if strings.Contains(q.SQL, `FROM "rag_embedding"`) && strings.Contains(q.SQL, "OFFSET") {
		h.probes++
	}
	return h.annRan.BeforeExec(ctx, q)
}

// A tenant seen past the threshold is remembered for pastFor: its next search sends no probe. A
// reclaim through the Store forgets it, and the next search probes again; another tenant's drop
// does not. Embeddings deleted below the threshold behind the Store's back (another server's
// reclaim) leave a stale "past" until pastFor ends: in that window the search tries the index,
// whose candidates under the tenant's filter come back short, and it is answered exactly
// (underfill), the exact answer. Past pastFor the probe runs again and the search is exact up
// front. The mutants, named here: a "past" never remembered (the second search probes); a reclaim
// not forgetting (the search after it sends no probe).
func TestANN_PastTheThresholdIsRememberedForAWhile(t *testing.T) {
	ctx := context.Background()
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	conn, built := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 2000, annDims, 0.2)
	buildFor(t, built, model)
	exact, _ := New(conn, "t")
	at, _ := New(conn, "t", WithANN(built))
	other, _ := New(conn, "u", WithANN(built))
	q := queryVecs(t, conn, "t", model, 1)[0]

	run := func(what string) (*probeRan, []int64) {
		t.Helper()
		h := &probeRan{}
		withViewHook(t, h)
		got := docsOf(semantic(t, at, model, q, search.Filter{}, 10))
		viewHook = nil
		return h, got
	}
	if h, _ := run("first"); h.probes != 1 || h.cand != 1 {
		t.Fatalf("the first search: %d probes, %d candidate stages; want the probe and the index", h.probes, h.cand)
	}
	if h, _ := run("second"); h.probes != 0 || h.cand != 1 {
		t.Fatalf("the second search: %d probes, %d candidate stages; want no probe, the index", h.probes, h.cand)
	}
	if _, err := conn.ExecContext(ctx, `INSERT INTO rag_embedding (tenant, model, text_hash, vec)
		SELECT tenant, 'test|old-model||16', text_hash, vec FROM rag_embedding WHERE tenant = 't' LIMIT 3`); err != nil {
		t.Fatal(err)
	}
	if n, err := at.ReclaimModels(ctx); err != nil || n != 3 {
		t.Fatalf("the reclaim: %d, %v; want the old model's 3", n, err)
	}
	if h, _ := run("after a reclaim"); h.probes != 1 {
		t.Fatalf("the search after a reclaim: %d probes; want it asked again", h.probes)
	}
	if err := other.Drop(ctx); err != nil {
		t.Fatal(err)
	}
	if h, _ := run("after another tenant's drop"); h.probes != 0 {
		t.Fatalf("the search after another tenant's drop: %d probes; want it still remembered", h.probes)
	}

	// another server reclaims behind this Store's back: 50 embeddings left, under k (100)
	if _, err := conn.ExecContext(ctx, `DELETE FROM rag_embedding WHERE tenant = 't' AND text_hash NOT IN
		(SELECT text_hash FROM rag_embedding WHERE tenant = 't' ORDER BY text_hash LIMIT 50)`); err != nil {
		t.Fatal(err)
	}
	want := docsOf(semantic(t, exact, model, q, search.Filter{}, 10))
	underfill := built.underfillOf(model).Load()
	h, got := run("stale")
	if h.probes != 0 || h.cand != 1 || built.underfillOf(model).Load()-underfill != 1 || !slices.Equal(got, want) {
		t.Fatalf("a stale past: %d probes, %d candidate stages, underfill +%d, %v; want no probe, the index tried, an underfill and the exact answer %v",
			h.probes, h.cand, built.underfillOf(model).Load()-underfill, got, want)
	}
	pastClock = func() time.Time { return time.Now().Add(2 * pastFor) }
	t.Cleanup(func() { pastClock = time.Now })
	h, got = run("past pastFor")
	if h.probes != 1 || h.cand != 0 || !slices.Equal(got, want) {
		t.Fatalf("past pastFor: %d probes, %d candidate stages, %v; want the probe, exact up front, %v", h.probes, h.cand, got, want)
	}
}
