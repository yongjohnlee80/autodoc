package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// findLast is find from the screen's bottom: the legend's row, under any text of the map's.
func findLast(cells [][]tuicore.Cell, text string) (int, int, bool) {
	for y := len(cells) - 1; y >= 0; y-- {
		if x, _, ok := find(cells[y:y+1], text); ok {
			return x, y, true
		}
	}
	return 0, 0, false
}

// TestTheTerminalsMindmapHasItsLegend: a terminal's Mindmap says what its outlines and colours
// stand for, as the window's does: its group (notes/, a yellow hexagon) as a hexagon's top edge in
// the colour its boxes wear, made legible on the page (onGround), and any other document as a
// rounded box's top edge in the colour an ungrouped box (Gamma's) wears. It lies under the map,
// below every box. In sepia, then in dark switched to with the map open; each theme's rows logged.
func TestTheTerminalsMindmapHasItsLegend(t *testing.T) {
	const yellow = 0xedc948 // illegible on sepia's paper as it is: onGround darkens it there
	kb := groupsKB(t, "[[mindmap.groups]]\nquery = \"path:notes/\"\ncolour = \"#edc948\"\nshape = \"hexagon\"\n")
	if err := os.WriteFile(filepath.Join(kb, "a.md"), []byte("# Alpha\n\n[[b]] [[c]]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kb, "c.md"), []byte("# Gamma\n\nccc\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, _ := openCellMindmapOf(t, kb, 3, func(r *running) { // Alpha, Beta and Gamma
		onLoop(r, func() bool { r.h.switchTheme("sepia"); return true })
		r.s.WaitFor(t, "sepia", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == "sepia" })
	})
	var grounds []tuicore.CellColor
	for _, theme := range []string{"sepia", "dark"} {
		if theme != "sepia" {
			onLoop(r, func() bool { r.h.switchTheme(theme); return true })
			r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		}
		var why string
		r.s.WaitFor(t, theme+": the legend under the map, in the boxes' colours", func(string) bool {
			snap := r.s.Backend.Snapshot()
			gx, gy, gok := findLast(snap, "╱─╲ notes/")
			ox, oy, ook := findLast(snap, "╭─╮ any other document")
			bx, by, bok := find(snap, "Beta")
			cx, cy, cok := find(snap, "Gamma")
			switch {
			case !gok || !ook:
				why = "no legend rows"
				return false
			case !bok || !cok || by == 0 || cy == 0:
				why = "no Beta or Gamma box"
				return false
			case gy <= by || gy <= cy || oy < gy:
				why = "the legend is not under the map"
				return false
			}
			ground := snap[gy][gx].Attrs.BG
			want := onGround(hexColour(yellow), color.NRGBA{R: ground.R, G: ground.G, B: ground.B, A: 0xff})
			group, betaEdge := snap[gy][gx].Attrs.FG, snap[by-1][bx].Attrs.FG
			other, gammaEdge := snap[oy][ox].Attrs.FG, snap[cy-1][cx].Attrs.FG
			switch {
			case ground.Kind != tuicore.CellColorRGB || len(grounds) > 0 && ground == grounds[0]:
				why = "not yet the theme's page"
			case group != (tuicore.CellColor{Kind: tuicore.CellColorRGB, R: want.R, G: want.G, B: want.B}) || group != betaEdge:
				why = "the group's mark is not its boxes' colour"
			case other != gammaEdge || other == group:
				why = "any other document's mark is not an ungrouped box's colour"
			default:
				grounds = append(grounds, ground)
				return true
			}
			return false
		})
		if why != "" && len(grounds) == 0 {
			t.Fatalf("%s: %s", theme, why)
		}
		var rows strings.Builder
		for _, row := range screenRows(r) {
			if strings.ContainsAny(row, "─╱╲╭╰") {
				rows.WriteString(row + "\n")
			}
		}
		t.Logf("%s, on %v: the screen's drawn rows:\n%s", theme, grounds[len(grounds)-1], rows.String())
	}
}

// legendWorn is n documents, each in a group of its own (a folder each), and the groups.
func legendWorn(n int) (relMap, *relGroups) {
	var rm relMap
	var list []relGroup
	rm.Nodes = append(rm.Nodes, mapNode{Path: "root.md", Title: "Root"})
	for i := range n {
		dir := string(rune('a'+i)) + "/"
		rm.Nodes = append(rm.Nodes, mapNode{Path: dir + "x.md", Title: "X" + dir})
		rm.Edges = append(rm.Edges, mapEdge{Src: 0, Dst: i + 1})
		list = append(list, relGroup{Query: "path:" + dir, Colour: hexColour(0x4e79a7), Shape: pm.MindSquare})
	}
	gs := newRelGroups(list)
	return rm, &gs
}

// TestTheCellLegendListsWrapsAndCaps: the legend lists the groups the map's documents wear, at most
// groupLegendRows of them and a row saying how many more, then any other document; its entries
// wrap in the view's width, cut with an ellipsis when one is too wide alone; a view too short
// draws none and gives the map every row; and the map is scrolled in the rows above it, so the
// selected box is never under it (the mutants: the cap off by one; the map's rows not narrowed).
func TestTheCellLegendListsWrapsAndCaps(t *testing.T) {
	rm, gs := legendWorn(groupLegendRows + 2)
	const h = 24 // shorter than the map: it scrolls
	m, v := builtMindmap(t, rm, "root.md", 80, h)
	m.cur.groups = gs
	ground := themeGround(nil)
	var texts []string
	for _, e := range m.legendEntries(m.cur, ground) {
		texts = append(texts, e.text)
	}
	if len(texts) != groupLegendRows+2 || texts[0] != "a/" || texts[groupLegendRows-1] != string(rune('a'+groupLegendRows-1))+"/" ||
		texts[groupLegendRows] != "… 2 more groups" || texts[groupLegendRows+1] != "any other document" {
		t.Fatalf("the legend's entries: %q; want the first %d groups, then 2 more, then any other document", texts, groupLegendRows)
	}
	for _, e := range m.legendEntries(m.cur, ground)[:groupLegendRows] {
		if e.mark != "┌─┐" {
			t.Fatalf("a square group's mark %q; want ┌─┐", e.mark)
		}
	}
	rows := v.legend(ground)
	if len(rows) < 2 {
		t.Fatalf("%d entries in 80 cells: %d rows; want them wrapped", len(texts), len(rows))
	}
	for i, row := range rows {
		w := 0
		for j, e := range row {
			w += entryWidth(e)
			if j > 0 {
				w += cellLegendGap
			}
		}
		if w > 78 {
			t.Errorf("row %d is %d cells; want 78 at most (80 less a margin each side)", i, w)
		}
	}
	if got := legendRows([]cellLegendEntry{{mark: "╭─╮", text: strings.Repeat("long words ", 4)}}, 20); len(got) != 1 ||
		entryWidth(got[0][0]) > 18 || !strings.HasSuffix(got[0][0].text, "…") {
		t.Errorf("an entry too wide alone: %+v; want it cut to 18 cells, an ellipsis ending it", got)
	}
	if v.mapRows() != h-len(rows)-1 {
		t.Errorf("the map's rows: %d; want %d (%d less the legend's %d and the blank row)", v.mapRows(), h-len(rows)-1, h, len(rows))
	}
	if m.cur.grid.Size.H <= v.mapRows() {
		t.Fatalf("fixture: the map is %d rows, within the view's %d: nothing scrolls", m.cur.grid.Size.H, v.mapRows())
	}
	// down to the last box: it is drawn in the map's rows, above the legend
	for range len(rm.Nodes) {
		v.HandleEvent(press(tuicore.KeyDown, ""))
	}
	b := m.cur.grid.Boxes[m.selected]
	if y := b.Rect.Y - m.y0; y < 0 || y+b.Rect.H > v.mapRows() {
		t.Errorf("the selected box %q at rows %d to %d; want it within the map's %d rows, above the legend", b.Label, y, y+b.Rect.H-1, v.mapRows())
	}
	v.cells.H = 6
	if v.legend(ground) != nil || v.mapRows() != 6 {
		t.Errorf("a view 6 rows high: legend %v, map rows %d; want none, and all 6 the map's", v.legend(ground), v.mapRows())
	}
}
