package relations

import (
	"strings"
	"testing"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// legendWorn is n documents, each in a group of its own (a folder each), and the groups.
func legendWorn(n int) (relMap, *relGroups) {
	var rm relMap
	var list []Group
	rm.Nodes = append(rm.Nodes, mapNode{Path: "root.md", Title: "Root"})
	for i := range n {
		dir := string(rune('a'+i)) + "/"
		rm.Nodes = append(rm.Nodes, mapNode{Path: dir + "x.md", Title: "X" + dir})
		rm.Edges = append(rm.Edges, mapEdge{Src: 0, Dst: i + 1})
		list = append(list, Group{Query: "path:" + dir, Colour: HexColour(0x4e79a7), Shape: pm.MindSquare})
	}
	gs := newRelGroups(list)
	return rm, &gs
}

// TestTheCellLegendListsWrapsAndCaps: the legend lists the groups the map's documents wear, at most
// groupLegendRows of them and a row saying how many more, then any other document, each mark a whole
// box (its top edge and its bottom edge); its entries wrap in bands within the view's width, more
// bands in a narrower view, cut with an ellipsis when one is too wide alone; a view too short draws
// none and gives the map every row; and the map is scrolled in the rows above it, two rows a band and
// a blank one, so the selected box is never under it (the mutants: the cap off by one; the map's
// rows not narrowed).
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
		if e.mark != "┌─┐" || e.markBottom != "└─┘" {
			t.Fatalf("a square group's mark %q over %q; want ┌─┐ over └─┘, a whole box", e.mark, e.markBottom)
		}
	}
	rows := v.legend(ground)
	if len(rows) < 2 {
		t.Fatalf("%d entries in 80 cells: %d rows; want them wrapped", len(texts), len(rows))
	}
	for i, row := range rows {
		if w := bandWidth(row); w > 78 {
			t.Errorf("band %d is %d cells; want 78 at most (80 less a margin each side)", i, w)
		}
	}
	// narrower, the same entries take more bands, each still inside it
	if narrow := legendRows(m.legendEntries(m.cur, ground), 30); len(narrow) <= len(rows) {
		t.Errorf("in 30 cells, %d bands; want more than the %d of 80 cells", len(narrow), len(rows))
	} else {
		for i, row := range narrow {
			if w := bandWidth(row); w > 28 {
				t.Errorf("in 30 cells, band %d is %d cells; want 28 at most", i, w)
			}
		}
	}
	if got := legendRows([]cellLegendEntry{{mark: "╭─╮", text: strings.Repeat("long words ", 4)}}, 20); len(got) != 1 ||
		entryWidth(got[0][0]) > 18 || !strings.HasSuffix(got[0][0].text, "…") {
		t.Errorf("an entry too wide alone: %+v; want it cut to 18 cells, an ellipsis ending it", got)
	}
	if want := h - 2*len(rows) - 1; v.mapRows() != want {
		t.Errorf("the map's rows: %d; want %d (%d less the legend's %d bands of two rows and the blank row)", v.mapRows(), want, h, len(rows))
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
