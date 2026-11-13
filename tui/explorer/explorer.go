package explorer

import (
	"context"
	"slices"
	"sort"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui/feature"
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
func explorerRows(list []feature.Workspace) []tuidecl.TreeRow {
	var rows []tuidecl.TreeRow
	for _, w := range list {
		label := w.Name
		if w.State != "ready" {
			label += "  (" + w.State + ")"
		}
		rows = append(rows, tuidecl.TreeRow{Row: feature.RowOf{"key": wsKey(w.Name), "label": label}, HasChildren: w.State == "ready"})
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
		rows = append(rows, tuidecl.TreeRow{Row: feature.RowOf{"key": dirKey(ws, prefix+f), "label": f + "/"}, HasChildren: true})
	}
	for _, n := range leaves {
		rows = append(rows, tuidecl.TreeRow{Row: feature.RowOf{"key": fileKey(ws, prefix+n), "label": n}})
	}
	return rows
}

// explorer is the explorer, a feature of its own: the tree, what it listed, and its cursor; and the
// drawer it shares with the links, its tab shown.
type Explorer struct {
	c     feature.Core
	Tab   int // the tab shown: explorerTab or linksTab
	Model *tuidecl.TreeListModel
	top   []feature.Workspace // the workspaces the top level shows
	Paths map[string][]string // by workspace: its files, once listed
	At    []string            // the row under the cursor, by its keys (FindPane reads it)
	gen   uint64              // the workspace's files last taken from Core.Files, by their Gen
}

func NewExplorer(c feature.Core) *Explorer {
	e := &Explorer{c: c, Model: tuidecl.NewTreeListModel("key", "label"), Paths: map[string][]string{}}
	e.Model.OnFetch = e.fetch
	return e
}

func (*Explorer) Name() string { return "explorer" }

func (e *Explorer) State() map[string]any {
	return map[string]any{"App.explorer": e.Model, "App.explorerTab": feature.ExplorerFilesTab, "App.linksTabShown": false}
}

func (e *Explorer) Commands() map[string]feature.Command {
	return map[string]feature.Command{
		"App.toggleExplorer":    feature.None(e.toggle),
		"App.explorerTabShown":  feature.OneNumber("a tab", e.TabShown),
		"App.hideExplorer":      feature.None(e.hide),
		"App.closeExplorer":     feature.None(e.Close),
		"App.explorerActivated": feature.OneIndex(e.activated),
		"App.explorerMoved":     feature.OneIndex(e.moved),
	}
}

// Catalog is the explorer's user command (feature/catalog.go): it runs what App.toggleExplorer runs.
func (e *Explorer) Catalog() []feature.Entry {
	return []feature.Entry{feature.Builtin("explorer.toggle", "the explorer", "App.toggleExplorer()", e.toggle)}
}

// toggle is SPC e: the drawer opened on the Explorer tab, or switched to it, or closed when it
// shows it already (relations.go's toggle is SPC l's, for the Links tab).
func (e *Explorer) toggle() {
	e.c.TogglePanelTab(feature.ExplorerPanelSpec().ID, feature.ExplorerFilesTab)
}

// tabShown is tab i shown, however it was: a click on its title, Ctrl+PageDown in the view, or
// SPC e and SPC l (showTab). The heading is the tab's (main.qml), and on screen its list takes the
// keyboard once the tab is laid out; closed or hidden, the drawer gives it the list when it opens or
// is shown again (PanelSpec.Focus).
func (e *Explorer) TabShown(i int) {
	if i != feature.ExplorerFilesTab && i != feature.ExplorerLinksTab {
		return
	}
	e.Tab = i
	e.c.Set("App.explorerTab", i)
	e.c.Set("App.linksTabShown", i == feature.ExplorerLinksTab)
	e.c.Post(func() {
		if e.Tab != i || !e.c.PanelShown(feature.ExplorerPanelSpec().ID) {
			return // another tab since, or nowhere to give the keyboard to
		}
		if err := e.c.View(e.View(), "forceActiveFocus"); err != nil {
			e.c.Failed("explorer: the keyboard", err)
		}
	})
}

// showTab shows tab i: the panel's ShowTab, which SPC e and SPC l reach (togglePanelTab).
func (e *Explorer) ShowTab(i int) { e.TabShown(i) }

// view is what in the drawer takes the keyboard: the tab shown's tree or list.
func (e *Explorer) View() string {
	if e.Tab == feature.ExplorerLinksTab {
		return feature.RelationsView
	}
	return feature.ExplorerPanelSpec().View
}

// hide is the strip's _: the drawer hidden, as the dock's click hides it, its icon kept to show it
// again as it was, on its tab, scrolled where it was.
func (e *Explorer) hide() { e.c.HidePanel(feature.ExplorerPanelSpec().ID) }

// close is the strip's x: the drawer closed, and out of the dock; SPC e or SPC l opens it again.
func (e *Explorer) Close() {
	if err := e.c.View(feature.ExplorerPanelSpec().ID, "close"); err != nil {
		e.c.Failed("explorer: closing", err)
	}
}

// Listed shows the workspaces when they changed, and the workspace in use's files when the host
// listed them anew.
func (e *Explorer) Listed() {
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
func (e *Explorer) showWorkspaces(list []feature.Workspace) {
	if e.top != nil && slices.EqualFunc(e.top, list, feature.Workspace.Same) {
		return
	}
	e.top = slices.Clone(list)
	e.Paths = map[string][]string{}
	e.Model.SetChildren(nil, explorerRows(list))
}

// fetch lists a row's children when the view first opens it: a folder's from its workspace's list,
// a workspace's from the daemon.
func (e *Explorer) fetch(ix tuidecl.Index) {
	key := e.Model.Key(ix)
	kind, ws, dir := splitKey(key)
	if kind == "dir" {
		e.Model.SetChildren(&ix, childrenOf(ws, dir, e.Paths[ws]))
		return
	}
	if kind != "ws" {
		return
	}
	tk := e.c.Ticket(nil, feature.TicketSession)
	e.c.Go(func(ctx context.Context) any {
		paths, err := ListAll(ctx, e.c.Call, ws)
		return feature.Answered(paths, err)
	}, func(v any) {
		a := v.(feature.AnswerOf[[]string])
		if !tk.Current() {
			return
		}
		if a.Err() != nil {
			e.c.Failed("the files of "+ws, a.Err())
			return
		}
		// the row may have moved while the list was coming: put the children under it only if it
		// is still the one asked about
		if e.Model.Key(ix) != key {
			return
		}
		e.Paths[ws] = a.Value
		e.Model.SetChildren(&ix, childrenOf(ws, "", a.Value))
	})
}

// relist puts workspace ws's files, listed again, under its row, when the explorer has listed them,
// and only when they changed: the view closes a row whose children are replaced (a folder open
// under it).
func (e *Explorer) relist(ws string, paths []string) {
	if old, listed := e.Paths[ws]; !listed || slices.Equal(old, paths) {
		return
	}
	for i := range e.Model.RowCount(nil) {
		if ix := (tuidecl.Index{Row: i}); e.Model.Key(ix) == wsKey(ws) {
			e.Paths[ws] = paths
			e.Model.SetChildren(&ix, childrenOf(ws, "", paths))
			return
		}
	}
}

// activated is Enter on a row: a file opens, entering its workspace first when it is another, and
// the drawer closes; a workspace or a folder opens or closes.
func (e *Explorer) activated(ix tuidecl.Index) error {
	kind, ws, p := splitKey(e.Model.Key(ix))
	switch kind {
	case "file":
		err := e.c.View("explorer", "close")
		e.c.OpenRef(ws, p)
		return err
	case "ws", "dir":
		return e.c.View(feature.ExplorerPanelSpec().View, "toggleExpanded", ix)
	}
	return nil
}

// moved is the cursor moving: the row under it, kept by its keys.
func (e *Explorer) moved(ix tuidecl.Index) error {
	e.At = e.keys(ix)
	return nil
}

// keys is a row's identity in the tree: the keys from the top row down to it.
func (e *Explorer) keys(ix tuidecl.Index) []string {
	var keys []string
	for p := &ix; p != nil; p = p.Parent {
		keys = append([]string{e.Model.Key(*p)}, keys...)
	}
	return keys
}

// loaded are the loaded rows in the order the tree shows them, each with its Index: a row's loaded
// children follow it, whether or not its folder is open now.
func (e *Explorer) loaded() ([]string, []tuidecl.Index) {
	m := e.Model
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
func (e *Explorer) moveTo(ix tuidecl.Index) error {
	e.At = e.keys(ix)
	return e.c.View(feature.ExplorerPanelSpec().View, "setCurrentIndex", ix)
}

// FindPane is the explorer's rows for find: those it has loaded, in the order the tree shows them, a
// closed folder's loaded rows included. A hit is moved to by the Index it was listed at, revealed, its
// folders opened.
func (e *Explorer) FindPane() feature.FindPane {
	return feature.FindPane{Name: "explorer", View: feature.ExplorerPanelSpec().View, Scope: feature.ExplorerPanelSpec().ID, Rows: func() ([]string, int, func(int, int) error) {
		rows, at := e.loaded()
		cur := slices.IndexFunc(at, func(ix tuidecl.Index) bool { return slices.Equal(e.keys(ix), e.At) })
		return rows, cur, func(row, _ int) error { return e.moveTo(at[row]) }
	}}
}

func (e *Explorer) Panels() []feature.PanelSpec {
	s := feature.ExplorerPanelSpec()
	s.Focus = e.View
	s.Tab = func() int { return e.Tab }
	s.ShowTab = e.ShowTab
	s.GoTabs = []string{feature.ExplorerLinksTab: "relations"}
	return []feature.PanelSpec{s}
}

// listAll is every file of workspace ws, in path order.
func ListAll(ctx context.Context, call func(context.Context, string, ...any) (any, error), ws string) ([]string, error) {
	var out []string
	after := ""
	for {
		res, err := call(ctx, "index.list", ws, after, int64(1000))
		if err != nil {
			return nil, err
		}
		m := feature.AsMap(res)
		for _, d := range feature.AsList(m["docs"]) {
			after = feature.Str(feature.AsMap(d), "path")
			out = append(out, after)
		}
		if more, _ := m["more"].(bool); !more {
			return out, nil
		}
	}
}
