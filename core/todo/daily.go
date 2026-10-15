package todo

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/logger"
)

// RefreshEvery is how often the daemon refreshes each claimed store by itself (ADR 1791532020
// §2.3): bucket placement, errors[], and the 28-day archive, which otherwise run only when a
// client calls todos.refresh.
const RefreshEvery = 24 * time.Hour

// RefreshDaily refreshes every claimed store once a day until ctx ends: at once each store whose
// daily refresh last ran RefreshEvery ago or more, or never, then each one as it falls due. The
// daemon runs it for as long as it serves; it first carries older routes' kb_roots into their
// stores (CarryKBRoots).
func (m *Manager) RefreshDaily(ctx context.Context) {
	m.CarryKBRoots(ctx) // once per start; a no-op once every route's kb_root is carried and cleared
	m.RefreshDue(ctx)
	// NOTE: timed. The daemon gets no event for "a day passed", so it looks for a store due every
	// refreshCheck (an hour by default), by the wall clock against the last run the journal keeps:
	// a restart neither repeats a day's refresh nor skips one, and a machine that slept catches up
	// within a check of waking.
	tick := time.NewTicker(m.o.refreshCheck)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.RefreshDue(ctx)
		}
	}
}

// RefreshDue refreshes each claimed store whose daily refresh last ran RefreshEvery ago or more,
// or never, and records the run in the store's record. A refresh that changed the store logs
// todos.changed, as the verb does. One that failed is logged and recorded in sys.events
// (todos.refresh_failed), and counts as the day's run: it is tried again a day later, not at every
// check. A claimed store with no directory yet has nothing to refresh.
func (m *Manager) RefreshDue(ctx context.Context) {
	st, err := m.j.Settings(ctx)
	if err != nil {
		if ctx.Err() == nil {
			logger.Warning(m.o.log, err, logger.Fields{"event": "todos.refresh_daily.failed"})
			m.event(Event{Kind: "todos.refresh_failed", Detail: err.Error()})
		}
		return
	}
	for _, id := range slices.Sorted(maps.Keys(st.Stores)) {
		rec := st.Stores[id]
		if !rec.Claimed || m.now().Sub(rec.RefreshedAt) < RefreshEvery {
			continue
		}
		if ctx.Err() != nil {
			return
		}
		m.refreshDaily(ctx, st, rec)
	}
}

// refreshDaily is one claimed store's daily refresh, reported and recorded.
func (m *Manager) refreshDaily(ctx context.Context, st Settings, rec StoreRecord) {
	ran := m.now()
	w, err := m.whereOf(ctx, st, rec)
	var sum RefreshSummary
	if err == nil {
		sum, err = m.refresh(ctx, w)
	}
	switch {
	case ctx.Err() != nil: // the daemon is stopping: the next start runs it
		return
	case errors.Is(err, ErrNotAStore), errors.Is(err, ErrUnclaimed):
		// claimed before its directory or buckets exist (todos.scaffold makes them), its root gone,
		// or released since the settings were read: nothing to refresh
		logger.Info(m.o.log, logger.Fields{"event": "todos.refresh_daily.skipped", "store": rec.ID, "path": rec.Path, "why": err.Error()})
	case err != nil:
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.refresh_daily.failed", "store": rec.ID, "path": rec.Path})
		m.event(Event{Kind: "todos.refresh_failed", Store: rec.ID, Path: rec.Path, Detail: err.Error()})
	case sum.Moved+sum.Rewritten > 0:
		logger.Info(m.o.log, logger.Fields{"event": "todos.refresh_daily", "store": rec.ID, "path": rec.Path,
			"archived": sum.Archived, "moved": sum.Moved, "rewritten": sum.Rewritten})
		m.event(m.changedOf(w))
	}
	err = m.j.UpdateSettings(ctx, func(s *Settings) error {
		if r, ok := s.Stores[rec.ID]; ok {
			r.RefreshedAt = ran
			s.Stores[rec.ID] = r
		}
		return nil
	})
	if err != nil {
		logger.Warning(m.o.log, err, logger.Fields{"event": "todos.refresh_daily.unrecorded", "store": rec.ID})
	}
}

// whereOf resolves a store by a root that resolves to it now: a root whose override is the store,
// or whose .todo-list it is.
func (m *Manager) whereOf(ctx context.Context, st Settings, rec StoreRecord) (where, error) {
	var roots []string
	for _, root := range slices.Sorted(maps.Keys(st.Roots)) {
		if rr := st.Roots[root]; rr.Override == rec.Path || (rr.Override == "" && root+"/"+todoDir == rec.Path) {
			roots = append(roots, root)
		}
	}
	if root, ok := strings.CutSuffix(rec.Path, "/"+todoDir); ok && !slices.Contains(roots, root) {
		roots = append(roots, root)
	}
	var errs []error
	for _, root := range roots {
		w, err := m.resolve(ctx, At{Root: root})
		if err == nil && w.rec.ID == rec.ID {
			return w, nil
		}
		if err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return where{}, errors.Join(errs...)
	}
	return where{}, fmt.Errorf("todo: no root resolves to the store %s", rec.Path)
}

// changedOf is the todos.changed a write to w logs, as the verbs' changedEvent: a workspace's
// store by the workspace's name, any other by its root.
func (m *Manager) changedOf(w where) Event {
	for _, name := range m.roots.Names() {
		if r, ok := m.roots.Root(name); ok && path.Clean(r) == w.root {
			return Event{Kind: "todos.changed", Workspace: name}
		}
	}
	return Event{Kind: "todos.changed", Path: w.root}
}
