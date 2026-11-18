package pgstore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
)

// A model's first build waits for its tenants' bulk loads (annbulk.go): deferred while a tenant
// whose active model it is has DeferBacklog or more documents not ready, built once the backlog
// drains or DeferMax after it first waited; and the build's session memory, with its serial
// fallback when the database's shared memory is too small.

// bulkConfig is the defaults with a backlog of 100 documents.
func bulkConfig() ANNConfig {
	cfg := DefaultANNConfig()
	cfg.DeferBacklog = 100
	return cfg
}

// unready marks n of tenant's documents not ready, as a bulk load under way leaves them.
func unready(t *testing.T, conn dao.DataConn, tenant string, n int) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), `UPDATE rag_document SET semantic_ready = false
		WHERE id IN (SELECT id FROM rag_document WHERE tenant = $1 AND semantic_ready ORDER BY id LIMIT $2)`, tenant, n); err != nil {
		t.Fatal(err)
	}
}

// readyAgain marks n of tenant's unready documents ready, as the backfill does.
func readyAgain(t *testing.T, conn dao.DataConn, tenant string, n int) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), `UPDATE rag_document SET semantic_ready = true
		WHERE id IN (SELECT id FROM rag_document WHERE tenant = $1 AND NOT semantic_ready ORDER BY id LIMIT $2)`, tenant, n); err != nil {
		t.Fatal(err)
	}
}

func statusOf(t *testing.T, a *ANN, model string) ANNStatus {
	t.Helper()
	st, err := a.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range st {
		if s.Model == model {
			return s
		}
	}
	t.Fatalf("no status for %s: %+v", model, st)
	return ANNStatus{}
}

// A tenant loading its model: with a backlog at the threshold the index waits (no index, the row
// wanted, the status saying why and until when, the deadline DeferMax from the first wait and kept
// across ticks), and once the backlog falls below it the next tick builds it and clears the reason.
func TestANNBulk_AFirstBuildWaitsForTheBulkLoad(t *testing.T) {
	conn, a := annSetup(t, bulkConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 500, annDims, 0.41)
	unready(t, conn, "t", 150)
	ctx := context.Background()
	if err := a.Want(ctx, model); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	r := indexOf(t, a, model)
	if r.State != annWanted || indexState(t, conn, model).Exists {
		t.Fatalf("with a backlog of 150: %s, index %+v; want wanted, no index", r.State, indexState(t, conn, model))
	}
	if r.NextAt == nil || r.NextAt.Before(before.Add(a.cfg.DeferMax-time.Minute)) || r.NextAt.After(time.Now().Add(a.cfg.DeferMax)) {
		t.Fatalf("the deadline %v; want DeferMax (%s) from the first wait", r.NextAt, a.cfg.DeferMax)
	}
	// the deadline is kept, not recomputed: moved back an hour, as if the hour had passed, a later
	// tick keeps the moved one and says it
	if _, err := conn.ExecContext(ctx, "UPDATE rag_ann_index SET next_at = next_at - interval '1 hour'"); err != nil {
		t.Fatal(err)
	}
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	r = indexOf(t, a, model)
	deadline := *r.NextAt
	if !deadline.Before(before.Add(a.cfg.DeferMax - 59*time.Minute)) {
		t.Fatalf("the deadline %v after a tick; want the one moved back an hour kept", deadline)
	}
	s := statusOf(t, a, model)
	if s.Mode != ModeANNBuilding || !strings.HasPrefix(s.Reason, deferredPrefix) ||
		!strings.Contains(s.Reason, "at least 100 documents") || !strings.Contains(s.Reason, "tenant t") ||
		!strings.Contains(s.Reason, deadline.Format(time.RFC3339)) {
		t.Fatalf("the status while deferred: %+v", s)
	}
	readyAgain(t, conn, "t", 50) // 100 left: still at the threshold
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annWanted || r.NextAt == nil || !r.NextAt.Equal(deadline) {
		t.Fatalf("at a backlog of 100: %s, deadline %v; want wanted, %v kept", r.State, r.NextAt, deadline)
	}
	readyAgain(t, conn, "t", 1) // 99: below it
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annReady || !indexState(t, conn, model).Valid || r.Error != "" || r.NextAt != nil {
		t.Fatalf("after the drain: %s, error %q, next_at %v", r.State, r.Error, r.NextAt)
	}
	if s := statusOf(t, a, model); s.Mode != ModeANN || s.Reason != "" {
		t.Fatalf("the status after the drain: %+v", s)
	}
}

// A backlog that cannot be counted holds every wanted model that tick, the error reported: a load is
// never paid for because its count failed. Once the count works again, the full backlog defers it.
func TestANNBulk_AFailedCountHoldsTheBuild(t *testing.T) {
	conn, a := annSetup(t, bulkConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 500, annDims, 0.51)
	unready(t, conn, "t", 300)
	ctx := context.Background()
	_ = a.Want(ctx, model)
	if _, err := conn.ExecContext(ctx, "ALTER TABLE rag_document RENAME TO rag_document_away"); err != nil {
		t.Fatal(err)
	}
	err := a.Tick(ctx)
	if _, rerr := conn.ExecContext(ctx, "ALTER TABLE rag_document_away RENAME TO rag_document"); rerr != nil {
		t.Fatal(rerr)
	}
	if err == nil || !strings.Contains(err.Error(), "the bulk loads") {
		t.Fatalf("the tick with no count: %v; want the bulk loads' error", err)
	}
	if r := indexOf(t, a, model); r.State != annWanted || indexState(t, conn, model).Exists {
		t.Fatalf("with the count failing: %s, index %+v; want wanted, no index", r.State, indexState(t, conn, model))
	}
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annWanted || !strings.HasPrefix(r.Error, deferredPrefix) {
		t.Fatalf("with the count back: %s %q; want deferred", r.State, r.Error)
	}
}

// A deadline that passes while another build holds the builder row stays the deadline: the row,
// still wanted under a full backlog, is not deferred again on the busy ticks (next_at as it was, its
// mark kept), and builds once the builder is free.
func TestANNBulk_APassedDeadlineIsKeptWhileTheBuilderIsBusy(t *testing.T) {
	conn, a := annSetup(t, bulkConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 500, annDims, 0.53)
	unready(t, conn, "t", 300)
	ctx := context.Background()
	_ = a.Want(ctx, model)
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annWanted || !strings.HasPrefix(r.Error, deferredPrefix) {
		t.Fatalf("precondition: %s %q, want deferred", r.State, r.Error)
	}
	for _, q := range []string{
		"UPDATE rag_ann_index SET next_at = now() - interval '1 minute'",
		// another server's build of another model holds the builder row
		"UPDATE rag_ann_builder SET model = 'test|other||8', owner = 'another-server', generation = generation + 1, lease_until = now() + interval '1 hour'",
	} {
		if _, err := conn.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	passed := *indexOf(t, a, model).NextAt
	for i := 1; i <= 3; i++ {
		if err := a.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		r := indexOf(t, a, model)
		if r.State != annWanted || r.NextAt == nil || !r.NextAt.Equal(passed) || r.Error != deferredDone {
			t.Fatalf("busy tick %d: %s, next_at %v (want %v kept), error %q (want %q)", i, r.State, r.NextAt, passed, r.Error, deferredDone)
		}
	}
	if s := statusOf(t, a, model); s.Reason != deferredDone {
		t.Errorf("the status while it waits for the builder: %+v", s)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE rag_ann_builder SET model = NULL, owner = '', backend_pid = NULL, backend_start = NULL, backend_token = '', lease_until = NULL"); err != nil {
		t.Fatal(err)
	}
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annReady || !indexState(t, conn, model).Valid || r.Error != "" || r.NextAt != nil {
		t.Fatalf("with the builder free: %s %q %v; want built", r.State, r.Error, r.NextAt)
	}
}

// A next_at with no deferral's reason (a retry's backoff left on a row wanted again) is no deadline:
// a full backlog defers the build, from a deadline of its own.
func TestANNBulk_AStaleNextAtIsNoDeadline(t *testing.T) {
	conn, a := annSetup(t, bulkConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 500, annDims, 0.52)
	unready(t, conn, "t", 300)
	ctx := context.Background()
	_ = a.Want(ctx, model)
	if _, err := conn.ExecContext(ctx, "UPDATE rag_ann_index SET next_at = now() - interval '1 hour', error = ''"); err != nil {
		t.Fatal(err)
	}
	before := time.Now()
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	r := indexOf(t, a, model)
	if r.State != annWanted || indexState(t, conn, model).Exists {
		t.Fatalf("a stale next_at under a full backlog: %s; want deferred, no index", r.State)
	}
	if r.NextAt == nil || r.NextAt.Before(before.Add(a.cfg.DeferMax-time.Minute)) {
		t.Fatalf("the deadline %v; want a new one, DeferMax from now", r.NextAt)
	}
}

// A load that never drains: past its deadline the index is built whatever the backlog.
func TestANNBulk_DeferMaxBuildsAnyway(t *testing.T) {
	conn, a := annSetup(t, bulkConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 500, annDims, 0.42)
	unready(t, conn, "t", 300)
	ctx := context.Background()
	_ = a.Want(ctx, model)
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annWanted {
		t.Fatalf("precondition: %s, want deferred", r.State)
	}
	if _, err := conn.ExecContext(ctx, "UPDATE rag_ann_index SET next_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annReady || !indexState(t, conn, model).Valid || r.Error != "" {
		t.Fatalf("past the deadline with a backlog of 300: %s, error %q; want built", r.State, r.Error)
	}
}

// Only a tenant whose active model it is holds a build back: another tenant's backlog on another
// model does not, a backlog below the threshold does not, and DeferBacklog 0 never defers.
func TestANNBulk_OnlyTheModelsOwnBacklogDefers(t *testing.T) {
	ctx := context.Background()
	t.Run("another model's tenant", func(t *testing.T) {
		conn, a := annSetup(t, bulkConfig())
		model, other := annModel(annDims), "test|other-model||8"
		annCorpus(t, conn, "t", model, 300, annDims, 0.43)
		if _, err := conn.ExecContext(ctx, `INSERT INTO rag_document (tenant, path, tags, semantic_ready)
			SELECT 'u', 'doc/' || g, '{}', false FROM generate_series(1, 400) g`); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(ctx, `INSERT INTO rag_meta (tenant, active_model) VALUES ('u', $1)`, other); err != nil {
			t.Fatal(err)
		}
		_ = a.Want(ctx, model)
		_ = a.Tick(ctx)
		if r := indexOf(t, a, model); r.State != annReady {
			t.Fatalf("another model's tenant's backlog held the build: %s %q", r.State, r.Error)
		}
	})
	t.Run("below the threshold", func(t *testing.T) {
		conn, a := annSetup(t, bulkConfig())
		model := annModel(annDims)
		annCorpus(t, conn, "t", model, 300, annDims, 0.44)
		unready(t, conn, "t", 99)
		_ = a.Want(ctx, model)
		if err := a.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		if r := indexOf(t, a, model); r.State != annReady {
			t.Fatalf("a backlog of 99 held the build: %s %q", r.State, r.Error)
		}
	})
	t.Run("never", func(t *testing.T) {
		cfg := bulkConfig()
		cfg.DeferBacklog = 0
		conn, a := annSetup(t, cfg)
		model := annModel(annDims)
		annCorpus(t, conn, "t", model, 300, annDims, 0.45)
		unready(t, conn, "t", 300)
		_ = a.Want(ctx, model)
		if err := a.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		if r := indexOf(t, a, model); r.State != annReady {
			t.Fatalf("DeferBacklog 0 deferred: %s %q", r.State, r.Error)
		}
	})
}

// A model whose index is ready is left serving when a tenant's backlog grows: the deferral is a
// first build's (the known limit: that load pays the index's cost).
func TestANNBulk_AReadyIndexIsNotDeferred(t *testing.T) {
	conn, a := annSetup(t, bulkConfig())
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 300, annDims, 0.46)
	ctx := context.Background()
	_ = a.Want(ctx, model)
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	oid := annIndexOID(t, conn, model)
	unready(t, conn, "t", 300)
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if r := indexOf(t, a, model); r.State != annReady || r.Error != "" || annIndexOID(t, conn, model) != oid {
		t.Fatalf("a ready index under a backlog: %s %q, oid %d → %d", r.State, r.Error, oid, annIndexOID(t, conn, model))
	}
}

// sessionSetting is name's value on p's session, read in a transaction of the session.
func sessionSetting(ctx context.Context, p postgres.PinnedConn, name string) (string, error) {
	tx, err := p.BeginSessionTx(ctx, dao.TxOptions{})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.RollbackContext(context.Background()) }()
	rows, err := tx.QueryContext(ctx, `SELECT current_setting($1)`, name)
	if err != nil {
		return "", err
	}
	var v string
	if err := scanRows(rows, &v); err != nil {
		return "", err
	}
	return v, tx.CommitContext(ctx)
}

// setBeforeCreate installs the builder's hook for one cell.
func setBeforeCreate(t *testing.T, f func(context.Context, postgres.PinnedConn, bool) error) {
	t.Helper()
	beforeCreate = f
	t.Cleanup(func() { beforeCreate = nil })
}

// The build's session has the configured maintenance_work_mem, and the database's own parallel
// workers; the pool's other sessions keep the database's memory.
func TestANNBulk_TheBuildSessionHasItsMemory(t *testing.T) {
	cfg := bulkConfig()
	cfg.MaintenanceWorkMem = "70MB"
	conn, a := annSetup(t, cfg)
	model := annModel(annDims)
	annCorpus(t, conn, "t", model, 300, annDims, 0.47)
	ctx := context.Background()
	var seen []string
	setBeforeCreate(t, func(ctx context.Context, p postgres.PinnedConn, serial bool) error {
		m, err := sessionSetting(ctx, p, "maintenance_work_mem")
		if err != nil {
			return err
		}
		w, err := sessionSetting(ctx, p, "max_parallel_maintenance_workers")
		if err != nil {
			return err
		}
		seen = append(seen, m+" "+w)
		return nil
	})
	dbWorkers := scalar(t, conn, `SELECT current_setting('max_parallel_maintenance_workers')`)
	_ = a.Want(ctx, model)
	if err := a.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "70MB "+dbWorkers {
		t.Fatalf("the build session's settings %q; want one build at 70MB with %s workers", seen, dbWorkers)
	}
	if m := scalar(t, conn, `SELECT current_setting('maintenance_work_mem')`); m == "70MB" {
		t.Fatalf("the pool's session took the builder's memory")
	}
	if indexOf(t, a, model).State != annReady {
		t.Fatal("not built")
	}
}

// shmFull is a parallel build's error when /dev/shm can't hold its memory.
type shmFull struct{}

func (shmFull) Error() string {
	return "could not resize shared memory segment: No space left on device"
}
func (shmFull) SQLState() string { return sqlstateShmFull }

// A parallel build that runs out of shared memory (53100, its half-built index left behind) is
// dropped and built again without parallel workers: the index is ready and valid, and the tick
// says it was built serially. Any other build error fails the build as before, with no second try.
func TestANNBulk_ASmallShmBuildsSerially(t *testing.T) {
	ctx := context.Background()
	t.Run("53100", func(t *testing.T) {
		conn, a := annSetup(t, bulkConfig())
		model := annModel(annDims)
		annCorpus(t, conn, "t", model, 300, annDims, 0.48)
		var calls []string
		setBeforeCreate(t, func(ctx context.Context, p postgres.PinnedConn, serial bool) error {
			w, err := sessionSetting(ctx, p, "max_parallel_maintenance_workers")
			if err != nil {
				return err
			}
			calls = append(calls, w)
			if serial {
				return nil
			}
			// the parallel build's leftover under the index's name, then its failure
			if _, err := conn.ExecContext(ctx, `CREATE INDEX "`+annIndexName(model)+`" ON rag_embedding (model)`); err != nil {
				return err
			}
			return shmFull{}
		})
		_ = a.Want(ctx, model)
		err := a.Tick(ctx)
		if !errors.Is(err, ErrBuiltSerially) {
			t.Fatalf("the tick: %v; want ErrBuiltSerially", err)
		}
		if len(calls) != 2 || calls[1] != "0" {
			t.Fatalf("the builds' parallel workers %q; want a parallel build, then one with 0", calls)
		}
		st := indexState(t, conn, model)
		if r := indexOf(t, a, model); r.State != annReady || !st.Valid || st.Def.Method != "hnsw" {
			t.Fatalf("after the serial build: %s, index %+v", r.State, st)
		}
		if err := a.Tick(ctx); errors.Is(err, ErrBuiltSerially) {
			t.Fatal("the next tick said it again")
		}
	})
	t.Run("another error", func(t *testing.T) {
		conn, a := annSetup(t, bulkConfig())
		model := annModel(annDims)
		annCorpus(t, conn, "t", model, 300, annDims, 0.49)
		calls := 0
		setBeforeCreate(t, func(context.Context, postgres.PinnedConn, bool) error {
			calls++
			return errors.New("out of disk")
		})
		_ = a.Want(ctx, model)
		err := a.Tick(ctx)
		if errors.Is(err, ErrBuiltSerially) || calls != 1 {
			t.Fatalf("another error: %v after %d builds; want one build, no serial retry", err, calls)
		}
		if r := indexOf(t, a, model); r.State != annFailed || !strings.Contains(r.Error, "out of disk") {
			t.Fatalf("another error: %s %q; want failed", r.State, r.Error)
		}
	})
}
