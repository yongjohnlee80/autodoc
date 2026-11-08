package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// embedsNote embeds a diagram, a chart, a table, a file too large to draw and a file that isn't
// there, each on a line of its own.
const embedsNote = "# Shelf\n\n![flow](flow.mmd)\n\n![sales](sales.chart.json)\n\n![stock](stock.csv)\n\n" +
	"![big](big.csv)\n\n![gone](gone.mmd)\n\nafter the embeds\n"

// embedsChart is the chart sales.chart.json holds: its title and its two bars' labels.
func embedsChart(title, a, b string) string {
	return `{"title":"` + title + `","mark":"bar","data":{"values":[{"k":"` + a + `","v":3},{"k":"` + b + `","v":5}]},` +
		`"encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}`
}

// embedsTUI runs the TUI, 100×70, on embedsNote and its files on disk (in dir), in theme.
func embedsTUI(t *testing.T, theme string) (r *running, dir string) {
	t.Helper()
	big := strings.Repeat("a,b\n", (1<<20)/4+1) // over the 1 MiB an embed reads
	dir = fileDir(t, "n.md", embedsNote, "flow.mmd", "flowchart LR\n  Gull --> Tern\n",
		"sales.chart.json", embedsChart("Fruit sold", "Apples", "Pears"), "stock.csv", "Item,Qty\napple,3\npear,5\n", "big.csv", big,
		"other.md", "# Other\n\nanother note\n")
	d := startManagedIncluding(t, map[string]string{"kb": dir}, tableInclude)
	h := newHost(NewSession(d.sock, nil), Options{seams: seamsFor(t)})
	s := decltest.RunOn(t, tuicore.NewTestBackend(100, 70), h.attach, h.options(Options{})...)
	t.Cleanup(h.cancel)
	r = &running{h: h, s: s}
	r.waitAttached(t, "kb")
	storeTheme(t, r, theme)
	onLoop(r, func() bool { r.h.openPath("n.md"); return true })
	r.waitFile(t, "n.md")
	r.s.WaitFor(t, "the note rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	return r, dir
}

// TestANotesEmbedsAreDrawnInTheRenderedView (docs/decisions/editor.md#a-terminal-notes-rendered-view):
// on a terminal, a line holding only an image of a diagram, a chart or a table file is drawn as the
// file, as the window and the HTML preview draw it: the flowchart's boxes, the chart with its title
// and labels, the table boxed, on the page's own background in sepia and in dark. A file too large
// to draw is a notice in its place; one that isn't there is its alt text, the view laid all the same.
// Typing on an embed's line shows its source, and Esc draws it again. A file written again while
// the note is open is drawn anew once the note is shown again.
func TestANotesEmbedsAreDrawnInTheRenderedView(t *testing.T) {
	for _, theme := range []string{"sepia", "dark"} {
		t.Run(theme, func(t *testing.T) {
			r, dir := embedsTUI(t, theme)
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
			// the chart's file is written again while the note is open: shown again, the note draws it
			chart := filepath.Join(dir, "sales.chart.json")
			if err := os.WriteFile(chart, []byte(embedsChart("Fruit sold again", "Plums", "Figs")), 0o600); err != nil {
				t.Fatal(err)
			}
			later := time.Now().Add(time.Minute) // a time of its own, whatever the file system's resolution
			if err := os.Chtimes(chart, later, later); err != nil {
				t.Fatal(err)
			}
			// another note, then this one again: the same renderer, and this note's layout kept from
			// before, which only the note being shown again (RelayRendered) has laid anew
			onLoop(r, func() bool { r.h.openPath("other.md"); return true })
			r.waitFile(t, "other.md")
			r.s.WaitForText(t, "another note")
			onLoop(r, func() bool { r.h.openPath("n.md"); return true })
			r.waitFile(t, "n.md")
			r.s.WaitFor(t, "the written chart drawn once the note is shown again", func(sc string) bool {
				return strings.Contains(sc, "Plums") && strings.Contains(sc, "Figs") && !strings.Contains(sc, "Apples")
			})
		})
	}
}
