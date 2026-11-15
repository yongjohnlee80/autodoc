package pgstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
)

// A MODEL'S INDEX WAITS FOR ITS BULK LOAD (docs/decisions/server.md#a-models-index-waits-for-its-bulk-load).
// Inserting into a model whose HNSW index is built costs several times more than into one without
// (ef_construction 200: 159 inserts a second against 500 at 1M rows), so a tenant's first import
// of its model would pay it throughout. A wanted index is therefore not built while a tenant whose
// active model it is has DeferBacklog or more documents not yet ready, its backfill under way, and
// is built once, concurrently, when the backlog falls below it, or DeferMax after it was first
// deferred whatever the backlog. Searches answer exactly meanwhile, as they do while any index is
// wanted. The backlog is counted only while an index is wanted, capped at DeferBacklog: a full
// backlog stops after that many rows, a drained one reads 000003's partial index of the unready
// documents. A backlog that cannot be counted holds every wanted model for that tick (Tick).
//
// Deferred: a wanted index, its first build or a rebuild after its index went missing. Not
// deferred: a model whose index is ready, a model only a tenant's target (a switch's fill), and a
// failed build's retry (due on its own backoff): their inserts pay the index's cost.

// deferredPrefix begins a deferred index row's error: the reason ANNStatus reports while it waits.
const deferredPrefix = "deferred: "

// deferral is why a wanted model's build waits, and until when at most.
type deferral struct {
	reason   string
	deadline time.Time
}

// unreadyBacklog counts tenant's documents not ready, capped at limit.
func (a *ANN) unreadyBacklog(ctx context.Context, tenant string, limit int) (int, error) {
	rows, err := a.conn.QueryContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM rag_document
		WHERE tenant = $1 AND NOT semantic_ready LIMIT $2) backlog`, tenant, limit)
	if err != nil {
		return 0, err
	}
	var n int64
	if err := scanRows(rows, &n); err != nil {
		return 0, err
	}
	return int(n), nil
}

// deferrals is the wanted models whose build waits for a tenant's bulk load, each with its
// reason and deadline: a deadline already recorded (next_at) is kept, a new one is DeferMax from
// now. A model past its deadline builds, whatever the backlog.
func (a *ANN) deferrals(ctx context.Context) (map[string]deferral, error) {
	out := map[string]deferral{}
	if a.cfg.DeferBacklog <= 0 {
		return out, nil
	}
	wanted, err := a.rwIndex.DAO(a.opts(ctx)...).With(aiState, annWanted).Select(aiModel, aiNextAt, aiError)
	if err != nil || len(wanted) == 0 {
		return out, err
	}
	metas, err := a.rwMeta.DAO(a.opts(ctx)...).Select(mTenant, mActive)
	if err != nil {
		return nil, err
	}
	now := a.now()
	for _, w := range wanted {
		// the deadline holdDeferred recorded with its reason; a next_at without one (a retry's
		// backoff, left on a row wanted again) is no deferral's, and a new one starts
		deadline := now.Add(a.cfg.DeferMax)
		if w.NextAt != nil && strings.HasPrefix(w.Error, deferredPrefix) {
			deadline = *w.NextAt
		}
		if !now.Before(deadline) {
			continue
		}
		for _, m := range metas {
			if m.Active != w.Model {
				continue
			}
			n, err := a.unreadyBacklog(ctx, m.Tenant, a.cfg.DeferBacklog)
			if err != nil {
				return nil, fmt.Errorf("the backlog of tenant %s: %w", m.Tenant, err)
			}
			if n >= a.cfg.DeferBacklog {
				out[w.Model] = deferral{deadline: deadline,
					reason: fmt.Sprintf("%sbacklog of at least %d documents not ready (tenant %s), builds by %s",
						deferredPrefix, a.cfg.DeferBacklog, m.Tenant, deadline.Format(time.RFC3339))}
				break
			}
		}
	}
	return out, nil
}

// holdDeferred records each deferred model's reason and deadline on its row, and clears the
// reason of a wanted model no longer deferred, under the builder row as every D5 transaction.
func (a *ANN) holdDeferred(ctx context.Context, held map[string]deferral) error {
	return dao.RunTx(ctx, func(tx *dao.Transaction) error {
		if _, err := a.builderRow(ctx, tx); err != nil {
			return err
		}
		rows, err := a.rwIndex.On(tx, a.locked(ctx)...).With(aiState, annWanted).Select(aiModel, aiError)
		if err != nil {
			return err
		}
		for _, r := range rows {
			d, waits := held[r.Model]
			switch {
			case waits && r.Error != d.reason:
				err = a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, r.Model).
					Set(aiError, d.reason).Set(aiNextAt, d.deadline).Set(aiUpdated, a.now()).Update()
			case !waits && strings.HasPrefix(r.Error, deferredPrefix):
				err = a.rwIndex.On(tx, a.opts(ctx)...).With(aiModel, r.Model).
					Set(aiError, "").Set(aiUpdated, a.now()).Update()
			}
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// sessionMemory sets the build session p's maintenance_work_mem to the configured size, and, when
// serial, no parallel workers: a parallel build keeps its memory in dynamic shared memory
// (/dev/shm), which a container's default 64 MB cannot hold. Both are the session's alone
// (set_config's is_local false, in a transaction of the session): p is discarded after the build.
func (a *ANN) sessionMemory(ctx context.Context, p postgres.PinnedConn, serial bool) error {
	if a.cfg.MaintenanceWorkMem == "" && !serial {
		return nil
	}
	tx, err := p.BeginSessionTx(ctx, dao.TxOptions{})
	if err != nil {
		return err
	}
	if a.cfg.MaintenanceWorkMem != "" {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('maintenance_work_mem', $1, false)`, a.cfg.MaintenanceWorkMem); err != nil {
			_ = tx.RollbackContext(context.Background())
			return fmt.Errorf("maintenance_work_mem %s: %w", a.cfg.MaintenanceWorkMem, err)
		}
	}
	if serial {
		if _, err := tx.ExecContext(ctx, `SELECT set_config('max_parallel_maintenance_workers', '0', false)`); err != nil {
			_ = tx.RollbackContext(context.Background())
			return err
		}
	}
	return tx.CommitContext(ctx)
}

// sqlstateShmFull is a parallel build's "could not resize shared memory segment": the database's
// /dev/shm smaller than maintenance_work_mem.
const sqlstateShmFull = "53100"

// ErrBuiltSerially is a model's index built without parallel workers, after a parallel build found
// the database's shared memory (/dev/shm) too small for maintenance_work_mem: built, and said, so a
// Docker deployment gives its database --shm-size at least that.
var ErrBuiltSerially = errors.New("ann: the database's /dev/shm is smaller than the builder's maintenance_work_mem, so the index was built without parallel workers; give the database's container --shm-size at least that")

// beforeCreate runs before each CREATE INDEX CONCURRENTLY of the builder: nil in production; a cell
// reads the session's settings in it, or fails that attempt as a small shm would. It can only fail
// an attempt, never clear one's error.
var beforeCreate func(ctx context.Context, p postgres.PinnedConn, serial bool) error

// createIndex builds spec on p with the configured memory, and once more serially, without parallel
// workers, when the parallel build ran out of shared memory (53100): its invalid leftover dropped
// first. serially reports that fallback. Each attempt has an error of its own: the serial one never
// sees the parallel one's.
func (a *ANN) createIndex(ctx context.Context, p postgres.PinnedConn, spec postgres.IndexSpec) (serially bool, err error) {
	for _, serial := range []bool{false, true} {
		if err := a.sessionMemory(ctx, p, serial); err != nil {
			return serial, err
		}
		var attempt error
		if beforeCreate != nil {
			if err := beforeCreate(ctx, p, serial); err != nil {
				attempt = err
			}
		}
		if attempt == nil {
			attempt = postgres.CreateIndexConcurrently(ctx, p, spec, postgres.BuildOptions{})
		}
		if serial || sqlState(attempt) != sqlstateShmFull {
			return serial, attempt
		}
		if err := postgres.DropIndexConcurrently(ctx, p, "", spec.Name, postgres.BuildOptions{}); err != nil {
			return serial, err
		}
	}
	return true, nil // the serial attempt returned above
}

// sqlState is err's PostgreSQL SQLSTATE, "" when it carries none.
func sqlState(err error) string {
	var e interface{ SQLState() string }
	if errors.As(err, &e) {
		return e.SQLState()
	}
	return ""
}
