package pgstore

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
)

// AP-7's lifecycle cells: one builder at a time for the table, every
// builder write checked against its owner and generation, recovery that fences first, safe
// retirement and bounded retries. Two ANNs on one database stand for two servers.

func builderOf(t *testing.T, a *ANN) *annBuilderRow {
	t.Helper()
	b, err := a.rwBuilder.DAO().With(abID, 1).Get()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func indexOf(t *testing.T, a *ANN, model string) *annIndexRow {
	t.Helper()
	r, err := a.rwIndex.DAO().With(aiModel, model).Get()
	if err != nil {
		t.Fatalf("the index row of %s: %v", model, err)
	}
	return r
}

func lapse(t *testing.T, conn dao.DataConn) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), "UPDATE rag_ann_builder SET lease_until = now() - interval '1 minute'"); err != nil {
		t.Fatal(err)
	}
}

func indexState(t *testing.T, conn dao.DataConn, model string) postgres.IndexState {
	t.Helper()
	st, err := postgres.ReadIndexState(context.Background(), conn, "", annIndexName(model))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// Two workers, one model: both tick at once and exactly one builds; the other finds the builder
// row busy, then the index ready.
func TestANNLife_TwoWorkersOneModel(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	b := NewANN(conn, DefaultANNConfig(), "runb")
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 2000, annDims, 0.11)
	if err := a.Want(context.Background(), model); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, w := range []*ANN{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = w.Tick(context.Background()) }()
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annReady {
		t.Fatalf("after both ticks: %s", r.State)
	}
	if g := builderOf(t, a).Generation; g != 1 {
		t.Fatalf("generation %d: the model was claimed %d times; want once", g, g)
	}
}

// Two workers, two models: the builder row serializes them, one build after the other, never at
// once.
func TestANNLife_TwoWorkersTwoModels(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	b := NewANN(conn, DefaultANNConfig(), "runb")
	m1, m2 := annModel(annDims), annModel(8)
	annCorpus(t, conn, "t1", m1, 2000, annDims, 0.12)
	annCorpus(t, conn, "t2", m2, 2000, 8, 0.13)
	for _, m := range []string{m1, m2} {
		if err := a.Want(context.Background(), m); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	for _, w := range []*ANN{a, b} {
		wg.Add(1)
		go func() { defer wg.Done(); _ = w.Tick(context.Background()) }()
	}
	wg.Wait()
	ready := 0
	for _, m := range []string{m1, m2} {
		if indexOf(t, a, m).State == annReady {
			ready++
		}
	}
	if ready != 1 || builderOf(t, a).Generation != 1 {
		t.Fatalf("after one round of both: %d ready, generation %d; want 1 and 1 (one build at a time)", ready, builderOf(t, a).Generation)
	}
	if err := b.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if indexOf(t, a, m1).State != annReady || indexOf(t, a, m2).State != annReady || builderOf(t, a).Generation != 2 {
		t.Fatalf("after the next tick: %s %s, generation %d", indexOf(t, a, m1).State, indexOf(t, a, m2).State, builderOf(t, a).Generation)
	}
}

// An expired but live old owner: A's lease lapses while its backend still runs. Recovery fences
// A, cancels the backend and drops nothing while it lives; A's completion, with its stale
// generation, changes nothing; the rebuild then succeeds.
func TestANNLife_ExpiredButLiveOwner(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	b := NewANN(conn, DefaultANNConfig(), "runb")
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 2000, annDims, 0.14)
	ctx := context.Background()
	if err := a.Want(ctx, model); err != nil {
		t.Fatal(err)
	}
	job, err := a.claim(ctx)
	if err != nil || job == nil {
		t.Fatalf("A's claim: %v %v", job, err)
	}
	p, err := postgres.PinSessionConn(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Discard()
	backend, err := postgres.MarkSession(ctx, p, job.token())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.owned(ctx, *job, func(d dao.DAO[*annBuilderRow, annBuilderField, int]) dao.DAO[*annBuilderRow, annBuilderField, int] {
		return d.Set(abPID, backend.PID).Set(abStart, backend.Start).Set(abToken, backend.Token)
	}, nil); err != nil {
		t.Fatal(err)
	}
	// A's backend runs a long statement: the build that outlives its lease
	running := make(chan error, 1)
	go func() {
		_, err := p.(postgres.SimpleQuerier).SimpleQuery(ctx, "SELECT pg_sleep(60)", func(postgres.ExtendedMessage) error { return nil })
		running <- err
	}()
	until(t, "A's backend to run", func() bool {
		var n int
		rows, _ := conn.QueryContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE pid = $1 AND state = 'active'", backend.PID)
		if rows != nil && rows.Next() {
			_ = rows.Scan(&n)
			rows.Close()
		}
		return n == 1
	})
	lapse(t, conn)
	if err := b.recoverLapsed(ctx); err != nil {
		t.Fatal(err)
	}
	if bo := builderOf(t, a); bo.Owner != "recovering:runb" || bo.Generation != job.gen+1 || bo.Model == nil {
		t.Fatalf("after recovery's first pass: owner %q gen %d model %v; want fenced, still held", bo.Owner, bo.Generation, bo.Model)
	}
	select {
	case <-running:
	case <-time.After(10 * time.Second):
		t.Fatal("recovery didn't cancel the live backend")
	}
	// A's completion with its stale generation changes nothing
	err = a.owned(ctx, *job, freed, func(tx *dao.Transaction) error {
		return a.rwIndex.On(tx).With(aiModel, model).Set(aiState, annReady).Update()
	})
	if !errors.Is(err, errFenced) || indexOf(t, a, model).State != annBuilding {
		t.Fatalf("A's stale completion: %v, state %s; want fenced, unchanged", err, indexOf(t, a, model).State)
	}
	p.Discard() // A's backend gone
	until(t, "A's backend to exit", func() bool {
		alive, err := postgres.BackendAlive(ctx, conn, backend)
		return err == nil && !alive
	})
	if err := b.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if indexOf(t, a, model).State != annReady || !indexState(t, conn, model).Valid {
		t.Fatalf("after recovery and the rebuild: %s", indexOf(t, a, model).State)
	}
}

func until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("waited 15 s for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A paused completion after a new claim: A's backend is gone and A waits before completing;
// recovery frees the row and B claims the next generation. A's completion then changes nothing:
// B's owner, generation, lease and the model's row are as they were, and a third worker still
// can't claim. B's own completion (the positive control) completes and frees.
func TestANNLife_PausedCompletionAfterANewClaim(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	b := NewANN(conn, DefaultANNConfig(), "runb")
	c := NewANN(conn, DefaultANNConfig(), "runc")
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 2000, annDims, 0.15)
	ctx := context.Background()
	_ = a.Want(ctx, model)
	jobA, err := a.claim(ctx)
	if err != nil || jobA == nil {
		t.Fatal(err)
	}
	lapse(t, conn)
	if err := b.recoverLapsed(ctx); err != nil { // no backend recorded: frees at once
		t.Fatal(err)
	}
	jobB, err := b.claim(ctx)
	if err != nil || jobB == nil || jobB.gen <= jobA.gen {
		t.Fatalf("B's claim: %+v %v", jobB, err)
	}
	before := builderOf(t, a)
	err = a.owned(ctx, *jobA, freed, func(tx *dao.Transaction) error {
		return a.rwIndex.On(tx).With(aiModel, model).Set(aiState, annReady).Update()
	})
	after := builderOf(t, a)
	if !errors.Is(err, errFenced) || after.Owner != before.Owner || after.Generation != before.Generation ||
		!after.Lease.Equal(*before.Lease) || indexOf(t, a, model).State != annBuilding {
		t.Fatalf("A's paused completion changed something: %v; %+v → %+v; state %s", err, before, after, indexOf(t, a, model).State)
	}
	if job, err := c.claim(ctx); err != nil || job != nil {
		t.Fatalf("a third worker claimed: %+v %v", job, err)
	}
	if err := b.build(ctx, *jobB); err != nil {
		t.Fatal(err)
	}
	if indexOf(t, a, model).State != annReady || builderOf(t, a).Model != nil {
		t.Fatal("B's completion didn't complete and free")
	}
}

// A failed backend registration: a worker fenced before it registers its backend never issues
// the DDL.
func TestANNLife_AFencedWorkerNeverBuilds(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 2000, annDims, 0.16)
	ctx := context.Background()
	_ = a.Want(ctx, model)
	job, err := a.claim(ctx)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	lapse(t, conn)
	if _, err := conn.ExecContext(ctx, "UPDATE rag_ann_builder SET owner = 'recovering:runb', generation = generation + 1"); err != nil {
		t.Fatal(err)
	}
	if err := a.build(ctx, *job); !errors.Is(err, errFenced) {
		t.Fatalf("the fenced build: %v, want errFenced", err)
	}
	if st := indexState(t, conn, model); st.Exists {
		t.Fatal("a fenced worker issued the DDL")
	}
}

// A restart with a good index keeps the row ready, with no rebuild; a mismatched definition (the
// name built with another operator class) is found at the restart, dropped and rebuilt.
func TestANNLife_RestartsCheckTheDefinition(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 2000, annDims, 0.17)
	buildFor(t, a, model)
	ctx := context.Background()
	gen := builderOf(t, a).Generation
	restarted := NewANN(conn, DefaultANNConfig(), "restart1")
	if err := restarted.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if indexOf(t, a, model).State != annReady || builderOf(t, a).Generation != gen {
		t.Fatal("a restart with a good index rebuilt it")
	}
	// the same name, another operator class
	p, err := postgres.PinSessionConn(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	if err := postgres.DropIndexConcurrently(ctx, p, "", annIndexName(model), postgres.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	el, _ := postgres.HNSWIndexElement("vec", dao.L2, dao.VectorType{Kind: dao.VectorFloat32, Dims: annDims})
	l2 := postgres.IndexSpec{Table: tEmb, Name: annIndexName(model), Method: "hnsw", Element: el, WhereEq: []postgres.EqText{{Column: "model", Value: model}}}
	if err := postgres.CreateIndexConcurrently(ctx, p, l2, postgres.BuildOptions{}); err != nil {
		t.Fatal(err)
	}
	p.Discard()
	again := NewANN(conn, DefaultANNConfig(), "restart2")
	if err := again.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if st := indexState(t, conn, model); indexOf(t, a, model).State != annReady || st.Def.OpClass != "vector_cosine_ops" {
		t.Fatalf("after the restart: %s, %+v; want rebuilt with cosine", indexOf(t, a, model).State, st.Def)
	}
}

// A killed builder: its build is cut off midway and it never completes; its lease lapses; the
// restart drops its invalid index, rebuilds it and marks it ready. Search is exact throughout.
func TestANNLife_AKilledBuilder(t *testing.T) {
	cfg := DefaultANNConfig()
	cfg.Threshold = 1000
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 120000, annDims, 0.18)
	ctx := context.Background()
	_ = a.Want(ctx, model)
	job, err := a.claim(ctx)
	if err != nil || job == nil {
		t.Fatal(err)
	}
	dead, kill := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = a.build(dead, *job) }()
	until(t, "the build to run", func() bool {
		rows, _ := conn.QueryContext(ctx, "SELECT count(*) FROM pg_stat_activity WHERE query LIKE 'CREATE INDEX CONCURRENTLY%'")
		var n int
		if rows != nil && rows.Next() {
			_ = rows.Scan(&n)
			rows.Close()
		}
		return n > 0
	})
	kill() // the process dies: its context with it, so nothing it would still write is written
	<-done
	st, _ := New(conn, "t", WithANN(a))
	if mode, _, _ := st.ANNMode(ctx, model); mode == ModeANN {
		t.Fatal("search is approximate before the index is ready")
	}
	if s := indexState(t, conn, model); !s.Exists || s.Valid {
		t.Fatalf("the killed build left %+v; want an invalid index", s)
	}
	lapse(t, conn)
	restarted := NewANN(conn, cfg, "restart")
	// recovery waits for the killed build's backend to exit before it drops anything: a tick may
	// only cancel it
	until(t, "the restart to recover and rebuild", func() bool {
		if err := restarted.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		return indexOf(t, a, model).State == annReady
	})
	if indexOf(t, a, model).State != annReady || !indexState(t, conn, model).Valid {
		t.Fatalf("after the restart: %s", indexOf(t, a, model).State)
	}
	if mode, _, _ := st.ANNMode(ctx, model); mode != ModeANN {
		t.Fatalf("after the rebuild the mode is %s", mode)
	}
}

// Two tenants retiring: while B still uses the model, A's retiring it leaves the index; once B
// retires it too and its embeddings are gone, the index is dropped and its row deleted.
func TestANNLife_TwoTenantsRetiring(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	model, other := annModel(annDims), annModel(8)
	annCorpus(t, conn, "ta", model, 1000, annDims, 0.19)
	annCorpus(t, conn, "tb", model, 1000, annDims, 0.2)
	buildFor(t, a, model)
	ctx := context.Background()
	retireTenant := func(tenant string) {
		st, _ := New(conn, tenant, WithANN(a))
		if err := st.SetModels(ctx, other, ""); err != nil {
			t.Fatal(err)
		}
		if _, err := st.ReclaimModels(ctx); err != nil {
			t.Fatal(err)
		}
	}
	retireTenant("ta")
	if err := a.retire(ctx); err != nil {
		t.Fatal(err)
	}
	if !indexState(t, conn, model).Exists || indexOf(t, a, model).State != annReady {
		t.Fatal("the index went while tenant tb still uses the model")
	}
	retireTenant("tb")
	if err := a.retire(ctx); err != nil {
		t.Fatal(err)
	}
	if indexState(t, conn, model).Exists {
		t.Fatal("the index stayed after the last tenant retired the model")
	}
	if _, err := a.rwIndex.DAO().With(aiModel, model).Get(); !errors.Is(err, dao.ErrNoRows) {
		t.Fatalf("the row stayed: %v", err)
	}

	// a model a tenant targets with no embedding of it yet (a switch just begun) is in use: the
	// tenant's models decide, not only the embeddings left
	fresh := annModel(12)
	tc, _ := New(conn, "tc", WithANN(a))
	if err := tc.SetModels(ctx, other, fresh); err != nil {
		t.Fatal(err)
	}
	if err := a.retire(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, fresh); r.State != annWanted {
		t.Fatalf("a targeted model with no embeddings yet: %s; want it kept wanted", r.State)
	}
}

// An attach racing a retire: a tenant wants the model between its row's retiring and the drop.
// The row ends wanted, not deleted, and the builder makes the index again: never ready without
// one.
func TestANNLife_AnAttachRacingARetire(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 1000, annDims, 0.21)
	buildFor(t, a, model)
	ctx := context.Background()
	if err := a.rwIndex.DAO().With(aiModel, model).Set(aiState, annRetiring).Update(); err != nil {
		t.Fatal(err)
	}
	p, err := postgres.PinSessionConn(ctx, conn)
	if err != nil {
		t.Fatal(err)
	}
	_ = postgres.DropIndexConcurrently(ctx, p, "", annIndexName(model), postgres.BuildOptions{})
	p.Discard()
	if err := a.Want(ctx, model); err != nil { // the attach, between the drop and the delete
		t.Fatal(err)
	}
	if err := a.rwIndex.DAO().With(aiModel, model).With(aiState, annRetiring).Delete(); err != nil { // retire's last step
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annWanted {
		t.Fatalf("after the attach: %s, want wanted", r.State)
	}
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if indexOf(t, a, model).State != annReady || !indexState(t, conn, model).Valid {
		t.Fatal("the attached model's index wasn't made again")
	}
}

// Bounded retries: a build that fails (a vector of the wrong dimensions in the model's rows) backs
// off, stops after 5 attempts, and the status says so; search stays exact.
func TestANNLife_BoundedRetries(t *testing.T) {
	conn, a := annSetup(t, DefaultANNConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 500, annDims, 0.22)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "UPDATE rag_embedding SET vec = '[1,2,3]' WHERE text_hash = (SELECT min(text_hash) FROM rag_embedding)"); err != nil {
		t.Fatal(err)
	}
	_ = a.Want(ctx, model)
	for i := 1; i <= annMaxAttempts+1; i++ {
		_ = a.Tick(ctx)
		r := indexOf(t, a, model)
		want := min(i, annMaxAttempts)
		if r.State != annFailed || r.Attempts != want {
			t.Fatalf("tick %d: %s after %d attempts; want failed after %d", i, r.State, r.Attempts, want)
		}
		if i < annMaxAttempts && (r.NextAt == nil || !r.NextAt.After(time.Now())) {
			t.Fatalf("tick %d: no backoff (%v)", i, r.NextAt)
		}
		if _, err := conn.ExecContext(ctx, "UPDATE rag_ann_index SET next_at = now() - interval '1 second'"); err != nil {
			t.Fatal(err)
		}
	}
	st, err := a.Status(ctx)
	if err != nil || len(st) != 1 || st[0].Mode != ModeANNFailed || !strings.Contains(st[0].Reason, "dimensions") {
		t.Fatalf("the status: %+v %v", st, err)
	}
}
