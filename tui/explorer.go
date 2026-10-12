package tui

import (
	"context"
	"slices"
	"sort"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE EXPLORER — every workspace, and the folders and files in each, as a tree in a drawer. A
// workspace's files are listed the first time it is opened; a folder's rows come from that list.
// Enter on a file opens it (entering its workspace first when it is another), and the drawer
// closes; on a workspace or a folder, it opens or closes the row. The workspace in use's files are
// listed again whenever the host lists them (a scan's end, a file made): its own listing is only the
// first one.

// A row's key says what it is: the workspace, and for a folder or a file, its path in it.
const keySep = "\t"

func wsKey(ws string) string       { return "ws" + keySep + ws }
func dirKey(ws, dir string) string { return "dir" + keySep + ws + keySep + dir }
func fileKey(ws, p string) string  { return "file" + keySep + ws + keySep + p }
func splitKey(k string) (kind, ws, rest string) {
	parts := strings.SplitN(k, keySep, 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// explorerRows are the workspaces, as the explorer's top level.
func explorerRows(list []Workspace) []tuidecl.TreeRow {
	var rows []tuidecl.TreeRow
	for _, w := range list {
		label := w.Name
		if w.State != "ready" {
			label += "  (" + w.State + ")"
		}
		rows = append(rows, tuidecl.TreeRow{Row: rowOf{"key": wsKey(w.Name), "label": label}, HasChildren: w.State == "ready"})
	}
	return rows
}

// childrenOf are the rows under dir ("" the workspace's root) of paths: its folders, then its
// files, each by name.
func childrenOf(ws, dir string, paths []string) []tuidecl.TreeRow {
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	folders := map[string]bool{}
	var leaves []string
	for _, p := range paths {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := p[len(prefix):]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			folders[rest[:i]] = true
		} else if rest != "" {
			leaves = append(leaves, rest)
		}
	}
	names := make([]string, 0, len(folders))
	for f := range folders {
		names = append(names, f)
	}
	sort.Strings(names)
	sort.Strings(leaves)
	var rows []tuidecl.TreeRow
	for _, f := range names {
		rows = append(rows, tuidecl.TreeRow{Row: rowOf{"key": dirKey(ws, prefix+f), "label": f + "/"}, HasChildren: true})
	}
	for _, n := range leaves {
		rows = append(rows, tuidecl.TreeRow{Row: rowOf{"key": fileKey(ws, prefix+n), "label": n}})
	}
	return rows
}

// explorer is the explorer, a feature of its own: the tree, what it listed, and its cursor.
type explorer struct {
	c     Core
	model *tuidecl.TreeListModel
	top   []Workspace         // the workspaces the top level shows
	paths map[string][]string // by workspace: its files, once listed
	at    []string            // the row under the cursor, by its keys (FindPane reads it)
	gen   uint64              // the workspace's files last taken from Core.Files, by their Gen
}

func newExplorer(c Core) *explorer {
	e := &explorer{c: c, model: tuidecl.NewTreeListModel("key", "label"), paths: map[string][]string{}}
	e.model.OnFetch = e.fetch
	return e
}

func (*explorer) Name() string { return "explorer" }

func (e *explorer) State() map[string]any { return map[string]any{"App.explorer": e.model} }

func (e *explorer) Commands() map[string]Command {
	return map[string]Command{
		"App.toggleExplorer":    none(e.toggle),
		"App.explorerActivated": oneIndex(e.activated),
		"App.explorerMoved":     oneIndex(e.moved),
	}
}

// Catalog is the explorer's user command (catalog.go): it runs what App.toggleExplorer runs.
func (e *explorer) Catalog() []Entry {
	return []Entry{builtin("explorer.toggle", "the explorer", "App.toggleExplorer()", e.toggle)}
}

// toggle opens the drawer, or closes it.
func (e *explorer) toggle() { e.c.TogglePanel("explorer") }

// Listed shows the workspaces when they changed, and the workspace in use's files when the host
// listed them anew.
func (e *explorer) Listed() {
	e.showWorkspaces(e.c.Workspaces())
	if f := e.c.Files(); f.Loaded && f.Gen != e.gen {
		e.gen = f.Gen
		e.relist(e.c.Workspace(), f.Paths)
	}
}

// showWorkspaces puts the workspaces at the top level, forgetting what was listed under them —
// only when they changed: the same workspaces in the same states, over the same files, leave the
// tree as it is, its open rows open (the workspaces are listed again on every connect, create and
// manager change).
func (e *explorer) showWorkspaces(list []Workspace) {
	if e.top != nil && slices.EqualFunc(e.top, list, Workspace.same) {
		return
	}
	e.top = slices.Clone(list)
	e.paths = map[string][]string{}
	e.model.SetChildren(nil, explorerRows(list))
}

// fetch lists a row's children when the view first opens it: a folder's from its workspace's list,
// a workspace's from the daemon.
func (e *explorer) fetch(ix tuidecl.Index) {
	key := e.model.Key(ix)
	kind, ws, dir := splitKey(key)
	if kind == "dir" {
		e.model.SetChildren(&ix, childrenOf(ws, dir, e.paths[ws]))
		return
	}
	if kind != "ws" {
		return
	}
	tk := e.c.Ticket(nil, TicketSession)
	e.c.Go(func(ctx context.Context) any {
		paths, err := listAll(ctx, e.c.Call, ws)
		return answered(paths, err)
	}, func(v any) {
		a := v.(answerOf[[]string])
		if !tk.Current() {
			return
		}
		if a.err != nil {
			e.c.Failed("the files of "+ws, a.err)
			return
		}
		// the row may have moved while the list was coming: put the children under it only if it
		// is still the one asked about
		if e.model.Key(ix) != key {
			return
		}
		e.paths[ws] = a.v
		e.model.SetChildren(&ix, childrenOf(ws, "", a.v))
	})
}

// relist puts workspace ws's files, listed again, under its row, when the explorer has listed them,
// and only when they changed: the view closes a row whose children are replaced (a folder open
// under it).
func (e *explorer) relist(ws string, paths []string) {
	if old, listed := e.paths[ws]; !listed || slices.Equal(old, paths) {
		return
	}
	for i := range e.model.RowCount(nil) {
		if ix := (tuidecl.Index{Row: i}); e.model.Key(ix) == wsKey(ws) {
			e.paths[ws] = paths
			e.model.SetChildren(&ix, childrenOf(ws, "", paths))
			return
		}
	}
}

// activated is Enter on a row: a file opens, entering its workspace first when it is another, and
// the drawer closes; a workspace or a folder opens or closes.
func (e *explorer) activated(ix tuidecl.Index) error {
	kind, ws, p := splitKey(e.model.Key(ix))
	switch kind {
	case "file":
		err := e.c.View("explorer", "close")
		e.c.OpenRef(ws, p)
		return err
	case "ws", "dir":
		return e.c.View(explorerPanelSpec().View, "toggleExpanded", ix)
	}
	return nil
}

// moved is the cursor moving: the row under it, kept by its keys.
func (e *explorer) moved(ix tuidecl.Index) error {
	e.at = e.keys(ix)
	return nil
}

// keys is a row's identity in the tree: the keys from the top row down to it.
func (e *explorer) keys(ix tuidecl.Index) []string {
	var keys []string
	for p := &ix; p != nil; p = p.Parent {
		keys = append([]string{e.model.Key(*p)}, keys...)
	}
	return keys
}

// loaded are the loaded rows in the order the tree shows them, each with its Index: a row's loaded
// children follow it, whether or not its folder is open now.
func (e *explorer) loaded() ([]string, []tuidecl.Index) {
	m := e.model
	var labels []string
	var at []tuidecl.Index
	var walk func(parent *tuidecl.Index)
	walk = func(parent *tuidecl.Index) {
		for r := 0; r < m.RowCount(parent); r++ {
			ix := tuidecl.Index{Row: r, Parent: parent}
			labels = append(labels, m.Data(ix, "label").Raw)
			at = append(at, ix)
			if m.RowCount(&ix) > 0 && !m.CanFetchMore(ix) {
				walk(&ix)
			}
		}
	}
	walk(nil)
	return labels, at
}

// moveTo puts the cursor on row ix (a find's hit).
func (e *explorer) moveTo(ix tuidecl.Index) error {
	e.at = e.keys(ix)
	return e.c.View(explorerPanelSpec().View, "setCurrentIndex", ix)
}

// FindPane is the explorer's rows for find: those it has loaded, in the order the tree shows them, a
// closed folder's loaded rows included. A hit is moved to by the Index it was listed at, revealed, its
// folders opened.
func (e *explorer) FindPane() FindPane {
	return FindPane{Name: "explorer", View: explorerPanelSpec().View, Rows: func() ([]string, int, func(int, int) error) {
		rows, at := e.loaded()
		cur := slices.IndexFunc(at, func(ix tuidecl.Index) bool { return slices.Equal(e.keys(ix), e.at) })
		return rows, cur, func(row, _ int) error { return e.moveTo(at[row]) }
	}}
}

func (*explorer) Panels() []PanelSpec {
	return []PanelSpec{explorerPanelSpec()}
}

// listAll is every file of workspace ws, in path order.
func listAll(ctx context.Context, call func(context.Context, string, ...any) (any, error), ws string) ([]string, error) {
	var out []string
	after := ""
	for {
		res, err := call(ctx, "index.list", ws, after, int64(1000))
		if err != nil {
			return nil, err
		}
		m := asMap(res)
		for _, d := range asList(m["docs"]) {
			after = str(asMap(d), "path")
			out = append(out, after)
		}
		if more, _ := m["more"].(bool); !more {
			return out, nil
		}
	}
}
