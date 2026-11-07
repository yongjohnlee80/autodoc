package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	const red = 0xe15759
	kb := groupsKB(t, "[[mindmap.groups]]\nquery = \"path:notes/\"\ncolour = \"#e15759\"\nshape = \"hexagon\"\n")
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
			want := onGround(hexColour(red), color.NRGBA{R: bg.R, G: bg.G, B: bg.B, A: 0xff})
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
