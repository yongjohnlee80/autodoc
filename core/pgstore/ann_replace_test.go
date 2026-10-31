package pgstore

import (
	"context"
	"fmt"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
)

// The replacement of an index that serves: one built with other parameters than the declared ones
// (an upgrade's) is rebuilt under a second name while it serves, then swapped in; a replacement
// that fails leaves it serving.

// replaceRig is a model's index built at ef_construction 64, as before the upgrade, and the
// upgraded server's ANN (the declared 200) over the same database, with a search through it.
type replaceRig struct {
	conn  dao.DataConn
	b     *ANN
	model string
	store *Store
	q     []float32
}

func newReplaceRig(t *testing.T) *replaceRig {
	t.Helper()
	old := DefaultANNConfig()
	old.Threshold, old.EfConstruction = 1000, 64
	conn, a := annSetup(t, old)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 3000, annDims, 0.12)
	buildFor(t, a, model)
	if st := indexState(t, conn, model); !st.Valid || st.Def.Options != "ef_construction=64" {
		t.Fatalf("the index before the upgrade: %+v; want valid, ef_construction=64", st)
	}
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	b := NewANN(conn, cfg, "upgraded")
	st, err := New(conn, "t", WithANN(b))
	if err != nil {
		t.Fatal(err)
	}
	return &replaceRig{conn: conn, b: b, model: model, store: st, q: queryVecs(t, conn, "t", model, 1)[0]}
}

// served checks a search answers through the index: its candidate stage ran and came back whole.
func (r *replaceRig) served(t *testing.T, what string) {
	t.Helper()
	h := &annRan{}
	withViewHook(t, h)
	before := r.b.underfillOf(r.model).Load()
	got := semantic(t, r.store, r.model, r.q, search.Filter{}, 10)
	if len(got) != 10 || h.cand != 1 || r.b.underfillOf(r.model).Load() != before {
		t.Fatalf("%s: %d hits, %d candidate stages, %d underfills; want 10 through the index", what, len(got), h.cand, r.b.underfillOf(r.model).Load()-before)
	}
}

func (r *replaceRig) mode(t *testing.T) ANNStatus {
	t.Helper()
	sts, err := r.b.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sts {
		if s.Model == r.model {
			return s
		}
	}
	t.Fatalf("no status for %s", r.model)
	return ANNStatus{}
}

// hold takes the lock a concurrent build waits on, and answers its release (also on cleanup).
func (r *replaceRig) hold(t *testing.T) (release func()) {
	t.Helper()
	ctx := context.Background()
	blocker, err := r.conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = blocker.Rollback() })
	if _, err := blocker.ExecContext(ctx, "LOCK TABLE rag_embedding IN SHARE UPDATE EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	return func() { _ = blocker.Rollback() }
}

func (r *replaceRig) waitingBuild(t *testing.T) {
	t.Helper()
	until(t, "the replacement's build to wait on the held table", func() bool {
		rows, _ := r.conn.QueryContext(context.Background(), "SELECT count(*) FROM pg_stat_activity WHERE query LIKE 'CREATE INDEX CONCURRENTLY%"+annNextName(r.model)+"%' AND wait_event_type = 'Lock'")
		var n int
		if rows != nil && rows.Next() {
			_ = rows.Scan(&n)
			rows.Close()
		}
		return n > 0
	})
}

func (r *replaceRig) nextExists(t *testing.T) bool {
	t.Helper()
	st, err := postgres.ReadIndexState(context.Background(), r.conn, "", annNextName(r.model))
	if err != nil {
		t.Fatal(err)
	}
	return st.Exists
}

// An outdated index is replaced while it serves: the upgraded server's first tick marks it
// replacing, and while the replacement waits to build, searches still go through the old index
// and the status says ann; once it is swapped in, the index of the model's name is the declared
// one (ef_construction 200), the second name is gone, and searches go through it.
func TestANNLife_AnOutdatedIndexIsReplacedWhileItServes(t *testing.T) {
	r := newReplaceRig(t)
	release := r.hold(t)
	done := make(chan error, 1)
	go func() { done <- r.b.Tick(context.Background()) }()
	r.waitingBuild(t)
	if s := indexOf(t, r.b, r.model).State; s != annReplacing {
		t.Fatalf("while the replacement builds: %s; want replacing", s)
	}
	r.served(t, "while the replacement waits to build")
	if s := r.mode(t); s.Mode != ModeANN || s.Reason == "" {
		t.Errorf("the status while replacing: %+v; want ann, with the reason", s)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	st := indexState(t, r.conn, r.model)
	if s := indexOf(t, r.b, r.model).State; s != annReady || !st.Valid || st.Def.Options != "ef_construction=200" {
		t.Fatalf("after the swap: %s, %+v; want ready, valid, ef_construction=200", s, st)
	}
	if r.nextExists(t) {
		t.Fatal("the second name is still there after the swap")
	}
	if s := r.mode(t); s.Mode != ModeANN || s.Reason != "" {
		t.Errorf("the status after the swap: %+v; want ann, no reason", s)
	}
	r.served(t, "after the swap")
}

// A replacement that fails (its build cancelled) leaves the old index serving: the model stays
// replacing with the attempt counted and its error kept, the second name's leftover is dropped,
// and searches still go through the old index.
func TestANNLife_AFailedReplacementLeavesTheIndexServing(t *testing.T) {
	r := newReplaceRig(t)
	ctx := context.Background()
	release := r.hold(t)
	defer release()
	done := make(chan error, 1)
	go func() { done <- r.b.Tick(ctx) }()
	r.waitingBuild(t)
	bo := builderOf(t, r.b)
	if bo.PID == nil || bo.Start == nil {
		t.Fatal("the builder's backend is not recorded")
	}
	if ok, err := postgres.CancelBackend(ctx, r.conn, postgres.Backend{PID: *bo.PID, Start: *bo.Start, Token: bo.Token}); err != nil || !ok {
		t.Fatalf("cancelling the replacement's build: %v %v", ok, err)
	}
	if err := <-done; err == nil {
		t.Fatal("the tick of a cancelled replacement answered no error")
	}
	release()
	row := indexOf(t, r.b, r.model)
	st := indexState(t, r.conn, r.model)
	if row.State != annReplacing || row.Attempts != 1 || row.Error == "" || !st.Valid || st.Def.Options != "ef_construction=64" {
		t.Fatalf("after the failed replacement: %s (attempts %d, error %q), index %+v; want replacing, 1, its error, the old index valid",
			row.State, row.Attempts, row.Error, st)
	}
	if r.nextExists(t) {
		t.Fatal("the failed replacement's leftover is still there")
	}
	if s := r.mode(t); s.Mode != ModeANN || s.Reason == "" {
		t.Errorf("the status after the failure: %+v; want ann, with the reason", s)
	}
	r.served(t, "after the failed replacement")
}

// Cell 1's skewed tenant pinned across builds: pgvector draws each node's level at random, so one
// build's recall is not the next's; at the declared ef_construction, skew90's recall@10 and @50
// hold ≥ 0.95 over three fresh builds.
func TestANN_SkewedTenantRecallHoldsAcrossBuilds(t *testing.T) {
	if testing.Short() {
		t.Skip("the recall corpora are large")
	}
	model := annModel(annDims)
	for b := range 3 {
		t.Run(fmt.Sprintf("build%d", b), func(t *testing.T) {
			cfg := DefaultANNConfig()
			cfg.Threshold = 1000
			conn, a := annSetup(t, cfg)
			annCorpus(t, conn, "big", model, 100000, annDims, 0.1)
			annCorpus(t, conn, "skew90", model, 90000, annDims, 0.2)
			annCorpus(t, conn, "skew5", model, 5000, annDims, 0.3)
			buildFor(t, a, model)
			exact, err := New(conn, "skew90")
			if err != nil {
				t.Fatal(err)
			}
			approx, err := New(conn, "skew90", WithANN(a))
			if err != nil {
				t.Fatal(err)
			}
			qs := queryVecs(t, conn, "skew90", model, 50)
			for _, n := range []int{10, 50} {
				h := &annRan{}
				withViewHook(t, h)
				sum := 0.0
				for _, q := range qs {
					sum += recall(docsOf(semantic(t, approx, model, q, search.Filter{}, n)), docsOf(semantic(t, exact, model, q, search.Filter{}, n)))
				}
				if h.cand != len(qs) {
					t.Fatalf("%d candidate stages ran for %d searches", h.cand, len(qs))
				}
				r := sum / float64(len(qs))
				t.Logf("build %d: recall@%d %.4f", b, n, r)
				if r < 0.95 {
					t.Errorf("build %d: recall@%d is %.4f, below 0.95", b, n, r)
				}
			}
		})
	}
}
