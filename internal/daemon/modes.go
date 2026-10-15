package daemon

import (
	"context"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// TodoStore is a todo store as the workspaces read the todo registry (ADR 1792015212 §2.2).
type TodoStore struct {
	ID   string            // its stable id in the registry
	Dir  string            // its directory, absolute
	Vars map[string]string // its variables' values (ADR 1792015069), by name; a name without one is left out
}

// TodoStores lists the todo registry's stores: the daemon's todo system, wired by the app.
type TodoStores func(ctx context.Context) ([]TodoStore, error)

// SetTodoStores gives the workspaces the todo registry and reads it at once, so a workspace started
// after it indexes its stores' tasks graph-only from its first pass. Call it once, before OpenAll:
// it starts the reader TodoStoresChanged wakes, which runs until the workspaces' context ends.
func (m *Workspaces) SetTodoStores(list TodoStores) {
	m.mu.Lock()
	m.todoStores = list
	m.mu.Unlock()
	m.readTodoStores()
	go func() {
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-m.storesDirty:
				m.rereadTodoStores()
			}
		}
	}()
}

// TodoStoresChanged says a store was registered, moved or removed, or a variable changed. It never
// waits: the todo system calls it from its verbs, and reading the registry can write it. The reader
// reads it once for every call made while it was reading.
func (m *Workspaces) TodoStoresChanged() {
	select {
	case m.storesDirty <- struct{}{}:
	default: // a read is pending already: it sees this change too
	}
}

// rereadTodoStores reads the registry again. When what the workspaces see changed, every served
// workspace re-indexes the documents whose mode or store changed: their indexer version tells
// them, no file changed (ADR 1792015212 §2.2).
func (m *Workspaces) rereadTodoStores() {
	if !m.readTodoStores() {
		return
	}
	m.mu.Lock()
	var ids []int64
	for _, s := range m.served {
		ids = append(ids, s.id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		m.revalidate(id)
	}
}

// readTodoStores reads the registry into the snapshot every workspace's modes read, longest
// directory first, and reports whether it changed.
func (m *Workspaces) readTodoStores() bool {
	m.mu.Lock()
	list := m.todoStores
	m.mu.Unlock()
	if list == nil {
		return false
	}
	got, err := list(m.ctx)
	if err != nil {
		logger.Warning(m.opts.Log, err, "reading the todo stores: the workspaces keep the stores they had")
		return false
	}
	sort.Slice(got, func(i, j int) bool { return len(got[i].Dir) > len(got[j].Dir) })
	old := m.stores.Load()
	if old != nil && slices.EqualFunc(*old, got, func(a, b TodoStore) bool {
		return a.ID == b.ID && a.Dir == b.Dir && maps.Equal(a.Vars, b.Vars)
	}) {
		return false
	}
	m.stores.Store(&got)
	return true
}

// storeAt is the todo store holding abs, the innermost one; nil for none.
func (m *Workspaces) storeAt(abs string) *TodoStore {
	list := m.stores.Load()
	if list == nil {
		return nil
	}
	for i, st := range *list {
		if paths.Under(abs, st.Dir) {
			return &(*list)[i]
		}
	}
	return nil
}

// modes are one workspace's graph_only settings (ADR 1792015212 §2.2), read by its indexer for each
// path. They outlive its restarts, like its schema.
type modes struct {
	m    *Workspaces
	root string
	cfg  atomic.Pointer[modeConfig]
}

// modeConfig is a workspace's graph_only list and todo_search, compiled.
type modeConfig struct {
	list   []string          // as set, the default when the workspace never set one
	user   workspace.Matcher // the patterns a user added: graph-only whatever todo_search says
	todo   bool              // the list holds the default entry, the todo rule's
	search bool              // todo_search: the todo rule makes a path full, not graph-only
}

var todoMatcher = workspace.NewMatcher([]string{workspace.TodoPattern}, nil)

func newModeConfig(list []string, search bool) *modeConfig {
	if list == nil {
		list = workspace.DefaultGraphOnly()
	}
	c := &modeConfig{list: list, search: search}
	var user []string
	for _, p := range list {
		if p == workspace.TodoPattern {
			c.todo = true
		} else {
			user = append(user, p)
		}
	}
	c.user = workspace.NewMatcher(user, nil)
	return c
}

// set replaces the workspace's settings.
func (h *modes) set(list []string, search bool) { h.cfg.Store(newModeConfig(list, search)) }

// mode is path p's mode, first match deciding (ADR 1792015212 §2.2): exclude is the indexer's own
// Match, before this is asked; a user's graph_only pattern makes it graph-only; a todo store's path,
// by the registry or the default entry, is graph-only unless todo_search is on; anything else is
// full. A path in a store has the store, whatever its mode.
func (h *modes) mode(p string) index.Mode {
	c := h.cfg.Load()
	var md index.Mode
	st := h.m.storeAt(filepath.Join(h.root, filepath.FromSlash(p)))
	if st != nil {
		md.Store = &index.TodoStore{ID: st.ID, Vars: st.Vars}
	}
	switch {
	case c.user.Match(p):
		md.GraphOnly = true
	case st != nil || (c.todo && todoMatcher.Match(p)):
		md.GraphOnly = !c.search
	}
	return md
}

// modesFor is workspace id's modes, made on its first start from what the store holds.
func (m *Workspaces) modesFor(id int64, root string, list []string, search bool) *modes {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.modes[id]
	if !ok {
		h = &modes{m: m, root: root} // a workspace's root never changes
		m.modes[id] = h
	}
	h.set(list, search)
	return h
}

// todoExcluded are the exclude patterns that keep a todo store under root out of the index (ADR
// 1792015212 §2.3): its tasks open in no workspace, with no relations. A store the registry knows is
// checked by a task file in it; one no route has resolved yet is not known.
func (m *Workspaces) todoExcluded(root string, exclude []string) []string {
	list := m.stores.Load()
	if list == nil {
		return nil
	}
	var out []string
	for _, st := range *list {
		rel, err := filepath.Rel(root, st.Dir)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
			continue
		}
		sample := filepath.ToSlash(rel) + "/open/task.md"
		for _, p := range exclude {
			if workspace.NewMatcher(nil, []string{p}).Excluded(sample) && !slices.Contains(out, p) {
				out = append(out, p)
			}
		}
	}
	return out
}

// noteTodoExcluded logs, for each served workspace whose excludes keep a todo store out, an event
// and a warning naming the workspace and the patterns (ADR 1792015212 §2.3); its settings offer to
// index the tasks graph-only instead. Logged at every start while the exclusion stands.
func (m *Workspaces) noteTodoExcluded() {
	m.mu.Lock()
	type ws struct {
		id         int64
		name, root string
		exclude    []string
	}
	var all []ws
	for name, s := range m.served {
		all = append(all, ws{s.id, name, s.root, s.exclude})
	}
	m.mu.Unlock()
	for _, w := range all {
		if pats := m.todoExcluded(w.root, w.exclude); len(pats) > 0 {
			detail := "exclude " + strings.Join(pats, "; ") + " keeps a todo store out of the index; " +
				"its settings offer to index the tasks graph-only instead"
			logger.Warning(m.opts.Log, nil, logger.Fields{"event": "workspace.todo_excluded", "workspace": w.name,
				"exclude": pats})
			m.event(w.id, "workspace.todo_excluded", detail)
		}
	}
}
