package daemon

import (
	"context"
	"errors"
	"fmt"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE START SWEEP AND THE CONVERSION (ADR 1791284787 §2.3, item 3; §2.5) — once the workspaces are
// open, every stored workspace keeps only its active model's and its target's vectors: a workspace
// an indexer runs is swept through it (its Sweep waits for setupModels, so the target it fills is
// recorded first); one with no indexer (no provider, or its root unavailable) through the store.
// It runs in the background, while the daemon serves. Each workspace's vectors whose text no chunk
// has go next (ADR 1791329335 §2.4). Then, once, a store not yet in incremental
// auto-vacuum mode is compacted into it, behind its free-space check: refused for want of room, it is
// said, and tried again at the next start. It is compacted only once every workspace has swept: a
// compaction while one still holds its leftovers would leave them out of the one rewrite that
// restores the store's layout, so a sweep that failed leaves the compaction to a later start.

// SweepModels runs the start sweep, then the conversion, until ctx ends.
func (m *Workspaces) SweepModels(ctx context.Context) {
	ws, err := m.db.Workspaces(ctx)
	if err != nil {
		logger.Warning(m.opts.Log, err, logger.Fields{"event": "models.sweep.failed"})
		return
	}
	swept := true
	for _, w := range ws {
		got, err := m.sweepOne(ctx, w.ID, w.Name)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			// what it left is swept at the next start
			logger.Warning(m.opts.Log, err, logger.Fields{"event": "models.sweep.failed", "workspace": w.Name})
			swept = false
			continue
		}
		var vectors int
		for _, r := range got {
			vectors += r.Vectors
		}
		if len(got) > 0 {
			detail := fmt.Sprintf("%d models no longer used, %d vectors", len(got), vectors)
			logger.Info(m.opts.Log, logger.Fields{"event": "models.reclaimed", "workspace": w.Name, "models": len(got), "vectors": vectors})
			m.notice(ctx, store.Event{Kind: "models.reclaimed", Workspace: w.Name, Detail: detail})
		}
		orphans, err := m.sweepOrphansOne(ctx, w.ID, w.Name)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			logger.Warning(m.opts.Log, err, logger.Fields{"event": "embedding.orphans.sweep_failed", "workspace": w.Name})
			swept = false
			continue
		}
		// after the first start on a build that keeps them collected, anything but 0 is a missed path
		logger.Info(m.opts.Log, logger.Fields{"event": "embedding.orphans.swept", "workspace": w.Name, "vectors": orphans})
	}
	if !swept {
		logger.Info(m.opts.Log, logger.Fields{"event": "store.compact.deferred", "why": "a workspace's sweep failed; the next start sweeps it, then compacts"})
		return
	}
	m.convert(ctx)
}

// ReclaimRetired reclaims the models a provider's removal took out of use, through the store: their
// workspaces' indexers are restarting without a provider. One cut short is finished by the next
// start's sweep.
func (m *Workspaces) ReclaimRetired(ctx context.Context, retired []store.Retired) {
	for _, r := range retired {
		got, err := index.ReclaimStore(ctx, m.db, r.Workspace, r.FP)
		if err != nil {
			if ctx.Err() == nil {
				logger.Warning(m.opts.Log, err, logger.Fields{"event": "model.reclaim.failed", "model": r.FP})
			}
			continue
		}
		logger.Info(m.opts.Log, logger.Fields{"event": "model.reclaimed", "model": r.FP, "vectors": got.Vectors, "gone": got.Gone})
	}
}

// sweepOne sweeps workspace id through its indexer when one runs, else through the store.
func (m *Workspaces) sweepOne(ctx context.Context, id int64, name string) ([]index.Reclaimed, error) {
	m.mu.Lock()
	s := m.served[name]
	m.mu.Unlock()
	if s != nil && s.w != nil && s.w.Index != nil && s.ctx != nil {
		// bounded by the workspace's life too: an indexer stopped mid-sweep leaves the rest to the next start
		sctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
		return s.w.Index.Sweep(sctx)
	}
	return index.SweepStore(ctx, m.db, id)
}

// sweepOrphansOne deletes workspace id's vectors whose text no chunk has (ADR 1791329335 §2.4):
// through its indexer when one runs, else through the store.
func (m *Workspaces) sweepOrphansOne(ctx context.Context, id int64, name string) (int, error) {
	m.mu.Lock()
	s := m.served[name]
	m.mu.Unlock()
	if s != nil && s.w != nil && s.w.Index != nil && s.ctx != nil {
		sctx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(s.ctx, cancel)
		defer stop()
		return s.w.Index.SweepOrphans(sctx)
	}
	return index.SweepOrphansStore(ctx, m.db, id)
}

// convert compacts a store not yet in incremental auto-vacuum mode, once.
func (m *Workspaces) convert(ctx context.Context) {
	mode, err := m.db.AutoVacuum(ctx)
	if err != nil {
		logger.Warning(m.opts.Log, err, logger.Fields{"event": "store.compact.failed"})
		return
	}
	if mode == store.AutoVacuumIncremental {
		return
	}
	logger.Info(m.opts.Log, logger.Fields{"event": "store.compact.start"})
	c, err := m.db.Compact(ctx)
	switch {
	case errors.Is(err, store.ErrNoRoom):
		detail := err.Error() + "; it is retried at the next start"
		logger.Warning(m.opts.Log, err, logger.Fields{"event": "store.compact.skipped"})
		m.notice(ctx, store.Event{Kind: "store.compact_skipped", Detail: detail})
	case err != nil:
		logger.Warning(m.opts.Log, err, logger.Fields{"event": "store.compact.failed"})
	default:
		detail := fmt.Sprintf("the store compacted from %d MB to %d MB", c.Before>>20, c.After>>20)
		logger.Info(m.opts.Log, logger.Fields{"event": "store.compacted", "before": c.Before, "after": c.After})
		m.notice(ctx, store.Event{Kind: "store.compacted", Detail: detail})
	}
}

// notice logs a daemon-wide event for the clients that follow them; a failure only costs the notice.
func (m *Workspaces) notice(ctx context.Context, e store.Event) {
	if _, err := m.db.AppendEvent(ctx, e); err != nil {
		logger.Warning(m.opts.Log, err, logger.Fields{"event": "event.append.failed", "kind": e.Kind})
	}
}
