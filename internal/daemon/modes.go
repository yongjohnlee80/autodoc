package daemon

import (
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/paths"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// TodoStore is a todo store as the workspaces read the todo registry (ADR 1792015212 §2.2).
type TodoStore struct {
	ID  string // its stable id in the registry
	Dir string // its directory, absolute
	KB  string // the root of the workspace it belongs to (ADR 1792132105 §2.4); "" for none
}

// TodoRegistry is what the workspaces read of the todo system: its stores, and the variables of
// every workspace (ADR 1792132105 §2.5), by root, "" holding a path's in no workspace. A name
// without a value is left out. Files are where the variables are read from: an edit to one, by hand
// or a git pull, is polled for (varsPoll) and reads the registry again.
type TodoRegistry struct {
	Stores []TodoStore
	Vars   map[string]map[string]string
	Files  []string
}

// varsPoll is how often the registry's variable files are checked for an edit.
const varsPoll = 2 * time.Second

// TodoStores reads the todo registry: the daemon's todo system, wired by the app.
type TodoStores func(ctx context.Context) (TodoRegistry, error)

// registry is the snapshot every workspace's modes read: the stores, longest directory first, and
// each workspace's variables, made once per read so a document's fingerprint is never recomputed.
type registry struct {
	stores []TodoStore
	vars   map[string]map[string]string
	files  []string
	roots  []string               // the workspace roots of vars, longest first
	modes  map[string]*index.Vars // vars as a mode's, by root
}

func newRegistry(r TodoRegistry) *registry {
	g := &registry{stores: r.Stores, vars: r.Vars, files: r.Files, modes: map[string]*index.Vars{}}
	sort.Slice(g.stores, func(i, j int) bool { return len(g.stores[i].Dir) > len(g.stores[j].Dir) })
	for root, vs := range r.Vars {
		g.modes[root] = index.NewVars(vs)
		if root != "" {
			g.roots = append(g.roots, root)
		}
	}
	sort.Slice(g.roots, func(i, j int) bool { return len(g.roots[i]) > len(g.roots[j]) })
	return g
}

// same reports whether g and o say the same to every workspace.
func (g *registry) same(o *registry) bool {
	return slices.Equal(g.stores, o.stores) && slices.Equal(g.files, o.files) &&
		maps.EqualFunc(g.vars, o.vars, func(a, b map[string]string) bool { return maps.Equal(a, b) })
}

// varsOf are the variables a document at abs, in store st (nil for none), resolves with: its store's
// workspace's, or the deepest workspace's holding it, or those of no workspace (ADR 1792132105 §2.5,
// the owner rule workspace.of answers). nil when the registry gives no variables.
func (g *registry) varsOf(abs string, st *TodoStore) *index.Vars {
	if g.vars == nil {
		return nil
	}
	if st != nil {
		return g.modes[st.KB]
	}
	for _, root := range g.roots {
		if paths.Under(abs, root) {
			return g.modes[root]
		}
	}
	return g.modes[""]
}

// SetTodoStores gives the workspaces the todo registry and reads it at once, so a workspace started
// after it indexes its stores' tasks in their mode from its first pass. Call it once, before OpenAll:
// it starts the reader TodoStoresChanged wakes, which runs until the workspaces' context ends.
func (m *Workspaces) SetTodoStores(list TodoStores) {
	m.mu.Lock()
	m.todoStores = list
	m.mu.Unlock()
	m.readTodoStores()
	m.varsFilesChanged()
	go func() {
		// NOTE: polled, not watched: a workspace's variable file is a dotfile its patterns may
		// exclude, so the follower may never see it change; the poll stats a few small files.
		poll := time.NewTicker(varsPoll)
		defer poll.Stop()
		for {
			select {
			case <-m.ctx.Done():
				return
			case <-m.storesDirty:
				m.rereadTodoStores()
			case <-poll.C:
				if m.varsFilesChanged() {
					m.rereadTodoStores()
				}
			}
		}
	}()
}

// varsFilesChanged stats the registry's variable files and reports whether one changed since the
// last call: written, removed or made. The first call records them.
func (m *Workspaces) varsFilesChanged() bool {
	g := m.stores.Load()
	if g == nil {
		return false
	}
	stamps := make(map[string]string, len(g.files))
	for _, f := range g.files {
		if fi, err := os.Stat(f); err == nil {
			stamps[f] = fmt.Sprintf("%d:%d", fi.Size(), fi.ModTime().UnixNano())
		}
	}
	first := m.varsStamps == nil
	changed := !first && !maps.Equal(stamps, m.varsStamps)
	m.varsStamps = stamps
	return changed
}

// TodoStoresChanged says a store was registered, moved, linked or removed, or a variable changed. It never
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

// readTodoStores reads the registry into the snapshot every workspace's modes read, and reports
// whether it changed.
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
	g := newRegistry(got)
	if old := m.stores.Load(); old != nil && old.same(g) {
		return false
	}
	m.stores.Store(g)
	return true
}

// storeAt is the todo store holding abs, the innermost one, and the variables abs resolves with;
// nil for none.
func (m *Workspaces) storeAt(abs string) (*TodoStore, *index.Vars) {
	g := m.stores.Load()
	if g == nil {
		return nil, nil
	}
	for i, st := range g.stores {
		if paths.Under(abs, st.Dir) {
			return &g.stores[i], g.varsOf(abs, &g.stores[i])
		}
	}
	return nil, g.varsOf(abs, nil)
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
// by the registry or the default entry, is graph-only when todo_search is off (it is on unless the
// workspace turned it off); anything else is full. A path in a store has the store, whatever its
// mode, and every path the variables of the workspace it belongs to.
func (h *modes) mode(p string) index.Mode {
	c := h.cfg.Load()
	var md index.Mode
	st, vars := h.m.storeAt(filepath.Join(h.root, filepath.FromSlash(p)))
	if st != nil {
		md.Store = &index.TodoStore{ID: st.ID}
	}
	md.Vars = vars
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
	g := m.stores.Load()
	if g == nil {
		return nil
	}
	var out []string
	for _, st := range g.stores {
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
