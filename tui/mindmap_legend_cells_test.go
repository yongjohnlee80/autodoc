package tui

import (
	"image/color"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/relations"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
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
		legendDrawn := func() bool {
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
			want := relations.OnGround(relations.HexColour(yellow), color.NRGBA{R: ground.R, G: ground.G, B: ground.B, A: 0xff})
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
		}
		// waited for as decltest's WaitFor waits, failing with which check missed
		for end := time.Now().Add(decltest.WaitTimeout); !legendDrawn(); time.Sleep(5 * time.Millisecond) {
			if time.Now().After(end) {
				t.Fatalf("%s: the legend under the map, in the boxes' colours: %s:\n%s", theme, why, strings.Join(screenRows(r), "\n"))
			}
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
