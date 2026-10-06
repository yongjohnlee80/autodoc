package index

import (
	"context"
	"errors"
	"fmt"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// RECLAIMING MODELS (ADR 1791284787) — a workspace keeps vectors for its active model and its
// target (the model its indexer fills; the active one outside a switch), and no other. Every other
// model is reclaimed: its vectors in batches, each its own write transaction so other writes go on
// between them, then its row. A batch first checks that the model is still neither active nor the
// target, so one switched back to keeps what is left. A reclaim cut short leaves an inactive model
// with fewer vectors, which the next sweep finishes.
//
// When: a superseded model once the flip is published, a model no longer targeted once setupModels
// has moved the target, and every reclaimable model at the daemon's start (Sweep, or SweepStore for
// a workspace with no indexer).

// reclaimBatch is how many of a model's vectors one write transaction deletes (a variable, so a test
// can take smaller steps).
var reclaimBatch uint64 = 2000

// writeFn runs fn as one write transaction: the indexer's writer, or the store's.
type writeFn func(ctx context.Context, fn func(context.Context, *store.Tx) error) error

// Reclaimed is what a reclaim removed: the vectors, and whether the model's row went too (false when
// the model became active or the target again, and was left).
type Reclaimed struct {
	FP      string
	Vectors int
	Gone    bool
}

// reclaimable lists the workspace's models that are neither active nor the target.
func (s *Store) reclaimable(ctx context.Context) ([]string, error) {
	var out []string
	err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := s.sc.Models(tx).With(store.ModelActive, int64(0)).With(store.ModelTarget, int64(0)).Select(store.ModelFP)
		for _, r := range rows {
			out = append(out, r.FP)
		}
		return err
	})
	return out, err
}

// reclaim deletes model fp's vectors, reclaimBatch at a time, each batch through write, then its
// row; then the store's freed pages go back to the file system. It stops, leaving what remains, at a
// batch that finds fp active or the target.
func (s *Store) reclaim(ctx context.Context, fp string, write writeFn) (Reclaimed, error) {
	out := Reclaimed{FP: fp}
	for {
		var n int
		var done bool
		err := write(ctx, func(_ context.Context, tx *store.Tx) error {
			m, err := s.sc.Models(tx).With(store.ModelFP, fp).Get(store.ModelActive, store.ModelTarget)
			if errors.Is(err, dao.ErrNoRows) {
				done = true // reclaimed already
				return nil
			}
			if err != nil {
				return err
			}
			if m.Active == 1 || m.Target == 1 {
				done = true // in use again: what is left stays
				return nil
			}
			keys, err := s.sc.Embeddings(tx).With(store.EmbModel, fp).Limit(reclaimBatch).Select(store.EmbTextHash)
			if err != nil {
				return err
			}
			if len(keys) == 0 {
				done, out.Gone = true, true
				return s.sc.Models(tx).With(store.ModelFP, fp).Delete()
			}
			hashes := make([]any, len(keys))
			for i, k := range keys {
				hashes[i] = k.TextHash
			}
			n = len(keys)
			return s.sc.Embeddings(tx).With(store.EmbModel, fp).With(store.EmbTextHash, hashes...).Delete()
		})
		if err != nil {
			return out, fmt.Errorf("index: reclaiming %s: %w", fp, err)
		}
		out.Vectors += n
		if done {
			break
		}
	}
	if out.Vectors > 0 || out.Gone {
		if err := s.db.IncrementalVacuum(ctx); err != nil {
			return out, err
		}
	}
	return out, nil
}

// storeWrite is a write transaction on the store alone, for a workspace no indexer writes.
func (s *Store) storeWrite(ctx context.Context, fn func(context.Context, *store.Tx) error) error {
	return s.db.Write(ctx, func(tx *store.Tx) error { return fn(ctx, tx) })
}

// SweepStore reclaims every model of workspace that is neither active nor the recorded target, for
// a workspace whose indexer does not run: no provider, or its root unavailable. A workspace an
// indexer runs is swept through it (Sweep), whose writer its reclaim must wait behind.
func SweepStore(ctx context.Context, db *store.Store, workspace int64) ([]Reclaimed, error) {
	s := Open(db, workspace)
	return s.sweep(ctx, s.storeWrite)
}

// ReclaimStore reclaims workspace's model fp through the store alone: a model taken out of use with
// its provider's removal, whose workspace's indexer is restarting without one.
func ReclaimStore(ctx context.Context, db *store.Store, workspace int64, fp string) (Reclaimed, error) {
	s := Open(db, workspace)
	return s.reclaim(ctx, fp, s.storeWrite)
}

func (s *Store) sweep(ctx context.Context, write writeFn) ([]Reclaimed, error) {
	fps, err := s.reclaimable(ctx)
	if err != nil {
		return nil, err
	}
	var out []Reclaimed
	for _, fp := range fps {
		r, err := s.reclaim(ctx, fp, write)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// Sweep reclaims every model of the workspace that is neither active nor the target, through the
// indexer's writer. It waits for the indexer to have recorded its target (setupModels), so a model
// it is about to fill is not taken for a leftover.
func (x *Indexer) Sweep(ctx context.Context) ([]Reclaimed, error) {
	select {
	case <-x.ready:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return x.store.sweep(ctx, x.do)
}

// reclaimLater queues fp's reclaim on the indexer's reclaimer: from the writer itself, which the
// reclaim's batches must not wait on.
func (x *Indexer) reclaimLater(fp string) {
	if fp == "" {
		return
	}
	select {
	case x.reclaims <- fp:
	default:
		// the queue is full: the next start's sweep reclaims it
		logger.Warning(x.opts.Logger, nil, logger.Fields{"event": "model.reclaim.deferred", "model": fp})
	}
}

// reclaimer runs the queued reclaims, one at a time, until ctx ends.
func (x *Indexer) reclaimer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case fp := <-x.reclaims:
			r, err := x.store.reclaim(ctx, fp, x.do)
			if err != nil {
				if ctx.Err() == nil {
					logger.Warning(x.opts.Logger, err, logger.Fields{"event": "model.reclaim.failed", "model": fp})
				}
				continue
			}
			logger.Info(x.opts.Logger, logger.Fields{"event": "model.reclaimed", "model": fp, "vectors": r.Vectors, "gone": r.Gone})
		}
	}
}
