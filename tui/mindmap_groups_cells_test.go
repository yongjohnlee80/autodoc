package tui

import (
	"context"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuimermaid "github.com/yongjohnlee80/golib/tui/mermaid"
)

// groupsKB is a workspace whose a.md (Alpha) links notes/b.md (Beta), with graph, its graph file's
// text ("" for none).
func groupsKB(t *testing.T, graph string) string {
	t.Helper()
	kb := fileDir(t, "a.md", "# Alpha\n\n[[b]]\n")
	if err := os.MkdirAll(filepath.Join(kb, "notes"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kb, "notes", "b.md"), []byte("# Beta\n\nbbb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if graph != "" {
		if err := os.MkdirAll(filepath.Join(kb, ".autodoc"), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(graphFile(kb), []byte(graph), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return kb
}

// openCellMindmap runs a terminal on kb, opens a.md and the Mindmap, and waits for its build in
// the workspace's groups.
func openCellMindmap(t *testing.T, kb string, before func(*running)) (*running, *cellMindmap) {
	t.Helper()
	d := startManaged(t, map[string]string{"kb": kb})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "kb")
	r.waitListed(t, 2)
	if before != nil {
		before(r)
	}
	m := onLoop(r, func() *cellMindmap {
		for _, f := range r.h.reg.features {
			if m, ok := f.(*cellMindmap); ok {
				return m
			}
		}
		return nil
	})
	if m == nil {
		t.Fatal("a terminal host has no cell Mindmap registered")
	}
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { m.toggle(); return true })
	r.s.WaitFor(t, "the Mindmap built in the workspace's groups", func(string) bool {
		return onLoop(r, func() bool {
			return m.open && m.cur != nil && m.groupsWS == "kb" && m.groups != nil && len(m.cur.tints) > 0
		})
	})
	return r, m
}

// boxOf is the box of the build shown labelled label.
func boxOf(r *running, m *cellMindmap, label string) (tuimermaid.Box, bool) {
	i := onLoop(r, func() int {
		for i, b := range m.cur.grid.Boxes {
			if b.Label == label {
				return i
			}
		}
		return -1
	})
	if i < 0 {
		return tuimermaid.Box{}, false
	}
	return onLoop(r, func() tuimermaid.Box { return m.cur.grid.Boxes[i] }), true
}

// TestTheTerminalsMindmapWearsItsGroups: a terminal's Mindmap draws each document in its group,
// the window's groups from the workspace's graph file: Beta, in notes/, a hexagon whose outline is
// the group's colour made legible on the page (onGround), the root, in no group, a bang in the
// accent. In sepia, then in dark switched to with the map open, the outline reads at 3:1 at least
// on its page; the map's rows are logged, each theme's, for review.
func TestTheTerminalsMindmapWearsItsGroups(t *testing.T) {
	const yellow = 0xedc948 // illegible on sepia's paper as it is: onGround darkens it there
	kb := groupsKB(t, "[[mindmap.groups]]\nquery = \"path:notes/\"\ncolour = \"#edc948\"\nshape = \"hexagon\"\n")
	r, m := openCellMindmap(t, kb, func(r *running) {
		onLoop(r, func() bool { r.h.switchTheme("sepia"); return true })
		r.s.WaitFor(t, "sepia", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == "sepia" })
	})
	beta, ok := boxOf(r, m, "Beta")
	alpha, aok := boxOf(r, m, "Alpha")
	if !ok || !aok {
		t.Fatalf("the map has no Beta or Alpha box: %+v", onLoop(r, func() []tuimermaid.Box { return m.cur.grid.Boxes }))
	}
	if beta.Form != tuimermaid.FormHexagon || alpha.Form != tuimermaid.FormBang {
		t.Fatalf("Beta's outline %v, Alpha's %v; want the group's hexagon, and the root's bang (in no group)", beta.Form, alpha.Form)
	}
	var seen []tuicore.CellColor
	for _, theme := range []string{"sepia", "dark"} {
		if theme != "sepia" {
			onLoop(r, func() bool { r.h.switchTheme(theme); return true })
			r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		}
		// Beta's top edge, over its label: the group's colour, legible on the page under it
		var fg, bg tuicore.CellColor
		r.s.WaitFor(t, theme+": Beta's outline in its group's colour", func(string) bool {
			snap := r.s.Backend.Snapshot()
			x, y, ok := find(snap, "Beta")
			if !ok || y == 0 {
				return false
			}
			edge := snap[y-1][x]
			fg, bg = edge.Attrs.FG, snap[y][x].Attrs.BG
			if bg.Kind != tuicore.CellColorRGB || !strings.ContainsRune("─╱╲", []rune(edge.Content + " ")[0]) {
				return false
			}
			want := onGround(hexColour(yellow), color.NRGBA{R: bg.R, G: bg.G, B: bg.B, A: 0xff})
			return fg == tuicore.CellColor{Kind: tuicore.CellColorRGB, R: want.R, G: want.G, B: want.B} &&
				(len(seen) == 0 || bg != seen[0])
		})
		if c := contrast(color.NRGBA{R: fg.R, G: fg.G, B: fg.B, A: 0xff}, color.NRGBA{R: bg.R, G: bg.G, B: bg.B, A: 0xff}); c < 3 {
			t.Errorf("%s: Beta's outline %v on its page %v is %.2f:1; want 3:1 at least", theme, fg, bg, c)
		}
		seen = append(seen, bg)
		var rows strings.Builder
		for _, row := range r.s.Backend.Snapshot() {
			var b strings.Builder
			for _, c := range row {
				b.WriteString(c.Content)
			}
			if s := strings.TrimRight(b.String(), " "); strings.ContainsAny(s, "─╱╲╭╰") {
				rows.WriteString(s + "\n")
			}
		}
		t.Logf("%s: Beta's outline %v on %v; the screen's drawn rows:\n%s", theme, fg, bg, rows.String())
	}
}

// TestATerminalsMindmapWritesItsFirstGroups: with no graph file, a terminal's Mindmap makes the
// workspace's first groups (a folder's, notes/, a hexagon) and writes them to the graph file, as the
// window's does; and it opens at the depth its settings choose.
func TestATerminalsMindmapWritesItsFirstGroups(t *testing.T) {
	kb := groupsKB(t, "")
	r, m := openCellMindmap(t, kb, func(r *running) {
		onLoop(r, func() bool { core{r.h}.SetPref(prefMindDepth, "2"); return true })
	})
	file := func() string { b, _ := os.ReadFile(graphFile(kb)); return string(b) }
	r.s.WaitFor(t, "the graph file written", func(string) bool { return strings.Contains(file(), `query = "path:notes/"`) })
	if f := file(); !strings.Contains(f, "[[mindmap.groups]]") || !strings.Contains(f, `shape = "hexagon"`) || strings.Contains(f, "[[globemap.groups]]") {
		t.Fatalf("the graph file:\n%s", f)
	}
	if beta, ok := boxOf(r, m, "Beta"); !ok || beta.Form != tuimermaid.FormHexagon {
		t.Errorf("Beta's box %+v (%v); want its first group's hexagon", beta, ok)
	}
	if d := onLoop(r, func() int { return m.depth }); d != 2 {
		t.Errorf("the map opened at depth %d; want its settings' 2", d)
	}
}

// screenRows is the screen's rows as text.
func screenRows(r *running) []string {
	var out []string
	for _, row := range r.s.Backend.Snapshot() {
		var b strings.Builder
		for _, c := range row {
			b.WriteString(c.Content)
		}
		out = append(out, strings.TrimRight(b.String(), " "))
	}
	return out
}

// TestATerminalsMindmapSettingsEditItsGroups: Relations › Mindmap settings… opens in a terminal, as
// the window's dialog less its theme row (the map is drawn in the page's): its groups listed, each a
// query, a colour and an outline, and the depth it opens at. An edit there is written to the graph
// file and drawn at once; the menu has the item. The dialog's rows are logged in sepia and dark.
func TestATerminalsMindmapSettingsEditItsGroups(t *testing.T) {
	kb := groupsKB(t, "[[mindmap.groups]]\nquery = \"path:notes/\"\ncolour = \"#edc948\"\nshape = \"hexagon\"\n")
	r, m := openCellMindmap(t, kb, nil)
	if !strings.Contains(string(withRelationViews(layout, false)), `onTriggered: App.openMindmapSettings()`) {
		t.Fatal("a terminal's Relations menu has no Mindmap settings… item")
	}
	for _, theme := range []string{"sepia", "dark"} {
		onLoop(r, func() bool { r.h.switchTheme(theme); return true })
		r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		onLoop(r, func() bool { m.openSettings(); return true })
		r.s.WaitFor(t, theme+": the settings, the group listed", func(s string) bool {
			return strings.Contains(s, "Mindmap settings") && strings.Contains(s, "path:notes/  #edc948  hexagon") && strings.Contains(s, "opens at")
		})
		rows := screenRows(r)
		for y, row := range rows {
			if strings.Contains(row, "opens at") && y > 0 && strings.Contains(rows[y-1], "theme") {
				t.Fatalf("%s: a terminal's Mindmap settings show a theme row:\n%s", theme, strings.Join(rows, "\n"))
			}
		}
		var dialog []string
		for _, row := range rows {
			if strings.Contains(row, "│") {
				dialog = append(dialog, row)
			}
		}
		t.Logf("%s: the settings:\n%s", theme, strings.Join(dialog, "\n"))
		r.keys(t, key('q'))
		r.s.WaitFor(t, theme+": the settings closed", func(s string) bool { return !strings.Contains(s, "Mindmap settings") })
	}
	onLoop(r, func() bool { m.editor.setColour("#123456"); return true })
	file := func() string { b, _ := os.ReadFile(graphFile(kb)); return string(b) }
	r.s.WaitFor(t, "the edit written", func(string) bool { return strings.Contains(file(), `colour = "#123456"`) })
	r.s.WaitFor(t, "the map drawn in the edit", func(string) bool {
		return onLoop(r, func() bool {
			for _, c := range m.cur.tints {
				if c == hexColour(0x123456) {
					return true
				}
			}
			return false
		})
	})
}

// gatedFiles reads a file only once open is closed: a daemon slow to answer.
type gatedFiles struct {
	absoluteFiles
	open chan struct{}
}

func (f gatedFiles) ReadAbsolute(ctx context.Context, path string) ([]byte, string, error) {
	<-f.open
	return f.absoluteFiles.ReadAbsolute(ctx, path)
}

// TestAGroupsRequestKeepsToItsWorkspace: a request for workspace a's first groups whose graph file is
// read only after b has become the workspace in use makes a's groups of a's folders, never b's, and
// writes them into a's graph file alone; a request for c, a workspace not in use, takes no files, so
// none of b's folders either.
func TestAGroupsRequestKeepsToItsWorkspace(t *testing.T) {
	dir := func(folder, name string) string {
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, folder), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, folder, name), []byte("# "+name+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return root
	}
	a, b, c := dir("alpha", "x.md"), dir("beta", "y.md"), dir("gamma", "z.md")
	d := startManaged(t, map[string]string{"a": a, "b": b, "c": c})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "a")
	r.h.p.Post(func() { r.h.enter("a") })
	listed := func(ws, path string) func() bool {
		return func() bool {
			return onLoop(r, func() bool { return r.h.ws == ws && slices.Contains(r.h.files.Paths, path) })
		}
	}
	r.waitWithin(t, "a's files listed", daemonWait, listed("a", "alpha/x.md"))
	open := make(chan struct{})
	store := onLoop(r, func() *graphStore { return newGraphStore(core{r.h}, gatedFiles{core{r.h}, open}, "") })
	got := make(chan []relGroup, 1)
	onLoop(r, func() bool {
		store.groups("a", viewMindmap, nil, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, func(l []relGroup) { got <- l })
		return true
	})
	r.h.p.Post(func() { r.h.enter("b") })
	r.waitWithin(t, "b in use, its files listed", daemonWait, listed("b", "beta/y.md"))
	close(open) // a's graph file read only now
	var list []relGroup
	select {
	case list = <-got:
	case <-time.After(daemonWait):
		t.Fatal("a's groups never came")
	}
	var queries []string
	for _, g := range list {
		queries = append(queries, g.Query)
	}
	if !slices.Contains(queries, "path:alpha/") || slices.Contains(queries, "path:beta/") {
		t.Fatalf("a's groups %q; want a's folder alone, never b's", queries)
	}
	file := func(root string) string { b, _ := os.ReadFile(graphFile(root)); return string(b) }
	r.waitWithin(t, "a's graph file written", daemonWait, func() bool { return strings.Contains(file(a), "path:alpha/") })
	if f := file(a); strings.Contains(f, "beta") {
		t.Fatalf("a's graph file holds b's folder:\n%s", f)
	}
	if f := file(b); f != "" {
		t.Fatalf("b's graph file was written by a's request:\n%s", f)
	}

	// c, not in use: its request has no files of its own to hand, and never b's
	onLoop(r, func() bool {
		store.groups("c", viewMindmap, nil, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, func(l []relGroup) { got <- l })
		return true
	})
	select {
	case list = <-got:
	case <-time.After(daemonWait):
		t.Fatal("c's groups never came")
	}
	for _, g := range list {
		if g.Query == "path:beta/" {
			t.Fatalf("c's groups %+v hold b's folder, the workspace in use's", list)
		}
	}
	if f := file(c); strings.Contains(f, "beta") {
		t.Fatalf("c's graph file holds b's folder:\n%s", f)
	}
}

// TestTheMindmapSettingsEditOnlyTheirWorkspace: the settings opened in b while b's groups are still
// being read show none and refuse every edit, so nothing lands in a's graph file (the editor's last
// workspace) nor over b's before b's are read; once b's are listed, an edit is b's alone.
func TestTheMindmapSettingsEditOnlyTheirWorkspace(t *testing.T) {
	kb := func(folder, colour string) string {
		root := t.TempDir()
		for _, p := range []string{folder, ".autodoc"} {
			if err := os.MkdirAll(filepath.Join(root, p), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(root, folder, "x.md"), []byte("# x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		graph := "[[mindmap.groups]]\nquery = \"path:" + folder + "/\"\ncolour = \"" + colour + "\"\nshape = \"hexagon\"\n"
		if err := os.WriteFile(graphFile(root), []byte(graph), 0o600); err != nil {
			t.Fatal(err)
		}
		return root
	}
	a, b := kb("alpha", "#4e79a7"), kb("beta", "#e15759")
	d := startManaged(t, map[string]string{"a": a, "b": b})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 36)
	r.waitAttached(t, "a")
	r.h.p.Post(func() { r.h.enter("a") })
	r.waitWithin(t, "a in use", daemonWait, func() bool { return onLoop(r, func() bool { return r.h.ws == "a" }) })
	var m *cellMindmap
	onLoop(r, func() bool {
		for _, f := range r.h.reg.features {
			if c, ok := f.(*cellMindmap); ok {
				m = c
			}
		}
		m.openSettings()
		return true
	})
	r.waitWithin(t, "a's groups listed", daemonWait, func() bool {
		return onLoop(r, func() bool { return m.editor.ws == "a" && !m.editor.loading && len(m.editor.list) == 1 })
	})
	r.keys(t, key('q'))
	open := make(chan struct{})
	onLoop(r, func() bool {
		s := newGraphStore(core{r.h}, gatedFiles{core{r.h}, open}, "") // b's file read only once open closes
		m.store, m.editor.store = s, s
		return true
	})
	r.h.p.Post(func() { r.h.enter("b") })
	r.waitWithin(t, "b in use", daemonWait, func() bool { return onLoop(r, func() bool { return r.h.ws == "b" }) })
	onLoop(r, func() bool { m.openSettings(); return true })
	r.waitWithin(t, "the settings bound to b, reading", daemonWait, func() bool {
		return onLoop(r, func() bool { return m.editor.ws == "b" && m.editor.loading && len(m.editor.list) == 0 })
	})
	file := func(root string) string { b, _ := os.ReadFile(graphFile(root)); return string(b) }
	beforeA, beforeB := file(a), file(b)
	onLoop(r, func() bool { m.editor.setColour("#123456"); m.editor.add(); m.editor.addMissing(); return true })
	if file(a) != beforeA || strings.Contains(file(a), "#123456") {
		t.Fatalf("an edit while b's groups were read changed a's graph file:\n%s", file(a))
	}
	close(open)
	r.waitWithin(t, "b's groups listed", daemonWait, func() bool {
		return onLoop(r, func() bool { return m.editor.ws == "b" && !m.editor.loading && len(m.editor.list) == 1 })
	})
	if file(b) != beforeB {
		t.Fatalf("an edit while b's groups were read was written over b's:\n%s", file(b))
	}
	onLoop(r, func() bool { m.editor.setColour("#123456"); return true })
	r.waitWithin(t, "the edit written to b", daemonWait, func() bool { return strings.Contains(file(b), `colour = "#123456"`) })
	if strings.Contains(file(a), "#123456") {
		t.Fatalf("b's edit reached a's graph file:\n%s", file(a))
	}
}
