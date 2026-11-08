package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// embedsNote embeds a diagram, a chart, a table, a file too large to draw and a file that isn't
// there, each on a line of its own.
const embedsNote = "# Shelf\n\n![flow](flow.mmd)\n\n![sales](sales.chart.json)\n\n![stock](stock.csv)\n\n" +
	"![big](big.csv)\n\n![gone](gone.mmd)\n\nafter the embeds\n"

// embedsTUI runs the TUI, 100×70, on embedsNote and its files on disk, in theme.
func embedsTUI(t *testing.T, theme string) *running {
	t.Helper()
	chart := `{"title":"Fruit sold","mark":"bar","data":{"values":[{"k":"Apples","v":3},{"k":"Pears","v":5}]},` +
		`"encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}`
	big := strings.Repeat("a,b\n", (1<<20)/4+1) // over the 1 MiB an embed reads
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "n.md", embedsNote,
		"flow.mmd", "flowchart LR\n  Gull --> Tern\n", "sales.chart.json", chart,
		"stock.csv", "Item,Qty\napple,3\npear,5\n", "big.csv", big)}, tableInclude)
	h := newHost(NewSession(d.sock, nil), Options{})
	s := decltest.RunOn(t, tuicore.NewTestBackend(100, 70), h.attach, h.options(Options{})...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	r.waitAttached(t, "kb")
	storeTheme(t, r, theme)
	onLoop(r, func() bool { r.h.openPath("n.md"); return true })
	r.waitFile(t, "n.md")
	r.s.WaitFor(t, "the note rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	return r
}

// TestANotesEmbedsAreDrawnInTheRenderedView (docs/decisions/editor.md#a-terminal-notes-rendered-view):
// on a terminal, a line holding only an image of a diagram, a chart or a table file is drawn as the
// file, as the window and the HTML preview draw it: the flowchart's boxes, the chart with its title
// and labels, the table boxed, on the page's own background in sepia and in dark. A file too large
// to draw is a notice in its place; one that isn't there is its alt text, the view laid all the same.
// Typing on an embed's line shows its source, and Esc draws it again.
func TestANotesEmbedsAreDrawnInTheRenderedView(t *testing.T) {
	for _, theme := range []string{"sepia", "dark"} {
		t.Run(theme, func(t *testing.T) {
			r := embedsTUI(t, theme)
			var seen string
			t.Cleanup(func() {
				if t.Failed() {
					t.Logf("%s: %s\n%s", theme, seen, r.s.String())
				}
			})
			r.s.WaitFor(t, "each embed drawn as its file on "+theme+"'s page", func(sc string) bool {
				for _, want := range []string{"Gull", "Tern", "Fruit sold", "Apples", "Pears", "apple", "pear",
					"big.csv is too large to draw here", "gone", "after the embeds"} {
					if !strings.Contains(sc, want) {
						seen = "no " + want + " yet"
						return false
					}
				}
				for _, src := range []string{"](flow.mmd)", "](sales.chart.json)", "](stock.csv)", "](big.csv)", "flowchart"} {
					if strings.Contains(sc, src) {
						seen = src + " is shown as its source"
						return false
					}
				}
				snap := r.s.Backend.Snapshot()
				px, py, ok := find(snap, "Shelf")
				gx, gy, gok := find(snap, "Gull")
				if !ok || !gok || snap[py][px].Attrs.FG.Kind != tuicore.CellColorRGB {
					seen = "the note's text is not yet on the theme's colours"
					return false
				}
				if page, got := bgIs(snap[py][px].Attrs), bgIs(snap[gy][gx].Attrs); got != page {
					seen = "the flowchart's label is on " + got + ", the page on " + page
					return false
				}
				return true
			})
			// typed in, the diagram's line is its source; Esc, drawn again
			r.putCursor(2, 0)
			r.keys(t, key('i'))
			r.s.WaitFor(t, "the embed's source while typing on its line", func(sc string) bool {
				return strings.Contains(sc, "![flow](flow.mmd)") && !strings.Contains(sc, "Gull")
			})
			r.keys(t, esc())
			r.s.WaitFor(t, "the diagram drawn again in Normal", func(sc string) bool {
				return strings.Contains(sc, "Gull") && !strings.Contains(sc, "](flow.mmd)")
			})
		})
	}
}
