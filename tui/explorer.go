package tui

import (
	"context"
	"path"
	"slices"
	"sort"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE EXPLORER — every workspace, and the folders and notes in each, as a tree in a drawer. A
// workspace's notes are listed the first time it is opened; a folder's rows come from that list.
// Enter on a note opens it (entering its workspace first when it is another), and the drawer
// closes; on a workspace or a folder, it opens or closes the row.

// A row's key says what it is: the workspace, and for a folder or a note, its path in it.
const keySep = "\t"

func wsKey(ws string) string       { return "ws" + keySep + ws }
func dirKey(ws, dir string) string { return "dir" + keySep + ws + keySep + dir }
func noteKey(ws, p string) string  { return "note" + keySep + ws + keySep + p }
func splitKey(k string) (kind, ws, rest string) {
	parts := strings.SplitN(k, keySep, 3)
	for len(parts) < 3 {
		parts = append(parts, "")
	}
	return parts[0], parts[1], parts[2]
}

// explorerRows are the workspaces, as the explorer's top level.
func explorerRows(list []wsInfo) []tuidecl.TreeRow {
	var rows []tuidecl.TreeRow
	for _, w := range list {
		label := w.name
		if w.state != "ready" {
			label += "  (" + w.state + ")"
		}
		rows = append(rows, tuidecl.TreeRow{Row: rowOf{"key": wsKey(w.name), "label": label}, HasChildren: w.state == "ready"})
	}
	return rows
}

// childrenOf are the rows under dir ("" the workspace's root) of paths: its folders, then its
// notes, each by name.
func childrenOf(ws, dir string, paths []string) []tuidecl.TreeRow {
	prefix := ""
	if dir != "" {
		prefix = dir + "/"
	}
	folders := map[string]bool{}
	var notes []string
	for _, p := range paths {
		if !strings.HasPrefix(p, prefix) {
			continue
		}
		rest := p[len(prefix):]
		if i := strings.IndexByte(rest, '/'); i >= 0 {
			folders[rest[:i]] = true
		} else if rest != "" {
			notes = append(notes, rest)
		}
	}
	names := make([]string, 0, len(folders))
	for f := range folders {
		names = append(names, f)
	}
	sort.Strings(names)
	sort.Strings(notes)
	var rows []tuidecl.TreeRow
	for _, f := range names {
		rows = append(rows, tuidecl.TreeRow{Row: rowOf{"key": dirKey(ws, prefix+f), "label": f + "/"}, HasChildren: true})
	}
	for _, n := range notes {
		rows = append(rows, tuidecl.TreeRow{Row: rowOf{"key": noteKey(ws, prefix+n), "label": n}})
	}
	return rows
}

// showWorkspacesInExplorer puts the workspaces at the explorer's top level, forgetting what was
// listed under them — only when they changed: the same workspaces in the same states leave the
// tree as it is, its open rows open (the workspaces are listed again on every connect, create and
// manager change).
func (h *Host) showWorkspacesInExplorer(list []wsInfo) {
	if h.explorerTop != nil && slices.Equal(h.explorerTop, list) {
		return
	}
	h.explorerTop = slices.Clone(list)
	h.explorerPaths = map[string][]string{}
	h.explorer.SetChildren(nil, explorerRows(list))
}

// fetchExplorer loads a row's children when the view first opens it.
func (h *Host) fetchExplorer(ix tuidecl.Index) { h.listUnder(ix, false) }

// listUnder lists the children of row ix; unchanged, when only a change is wanted (a scan's end),
// it leaves them as they are — the view closes a row whose children are replaced, and the notes of
// a workspace being edited are listed again after every scan.
func (h *Host) listUnder(ix tuidecl.Index, onlyChanged bool) {
	key := h.explorer.Key(ix)
	kind, ws, dir := splitKey(key)
	if kind == "dir" {
		h.explorer.SetChildren(&ix, childrenOf(ws, dir, h.explorerPaths[ws]))
		return
	}
	if kind != "ws" {
		return
	}
	gen := h.session.Gen()
	type answer struct {
		paths []string
		err   error
	}
	do(h, func(ctx context.Context) answer {
		paths, err := h.listAll(ctx, ws)
		return answer{paths, err}
	}, func(a answer) {
		if gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			h.failed("the notes of "+ws, a.err)
			return
		}
		// the row may have moved while the list was coming: put the children under it only if it
		// is still the one asked about
		if h.explorer.Key(ix) != key {
			return
		}
		if old, listed := h.explorerPaths[ws]; onlyChanged && listed && slices.Equal(old, a.paths) {
			return
		}
		h.explorerPaths[ws] = a.paths
		h.explorer.SetChildren(&ix, childrenOf(ws, "", a.paths))
	})
}

// relistInExplorer lists workspace ws's notes again under its row, when the explorer has listed
// them, and replaces them only when they changed (a folder open under it then closes).
func (h *Host) relistInExplorer(ws string) {
	if _, listed := h.explorerPaths[ws]; !listed {
		return
	}
	for i := range h.explorer.RowCount(nil) {
		if ix := (tuidecl.Index{Row: i}); h.explorer.Key(ix) == wsKey(ws) {
			h.listUnder(ix, true)
			return
		}
	}
}

// listAll is every note of workspace ws, in path order.
func (h *Host) listAll(ctx context.Context, ws string) ([]string, error) {
	var out []string
	after := ""
	for {
		res, err := h.call(ctx, "index.list", ws, after, int64(1000))
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

// explorerActivated is Enter on a row: a note opens, entering its workspace first when it is
// another, and the drawer closes; a workspace or a folder opens or closes.
func (h *Host) explorerActivated(ix tuidecl.Index) error {
	kind, ws, p := splitKey(h.explorer.Key(ix))
	switch kind {
	case "note":
		h.keep(h.p.Call("explorer", "close"))
		h.openIn(ws, p)
		return nil
	case "ws", "dir":
		return h.p.Call("explorerTree", "toggleExpanded", ix)
	}
	return nil
}

// openIn opens note p of workspace ws, entering ws first when it is not the one in use; either
// way, unsaved changes are asked about first.
func (h *Host) openIn(ws, p string) {
	if ws == h.ws {
		h.openPath(p)
		return
	}
	h.guard("open "+path.Base(p)+" in "+ws, func() {
		h.enter(ws)
		h.load(p)
	})
}
