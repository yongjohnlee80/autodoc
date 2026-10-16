//go:build gui

package tui

import (
	"image/color"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// guiResultsTUI is a TUI under --gui's style whose plugin "kinds" has shown the kinds table, its
// DataTable laid out natively.
func guiResultsTUI(t *testing.T) *running {
	t.Helper()
	dir := t.TempDir()
	installPlugin5(t, dir, "kinds", "service", "kinds", tableManifest)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: testPrefs})
	logs := t.TempDir()
	r := runTUI(t, NewSession(d.sock, nil), Options{Plugins: Plugins{Dir: dir, LogDir: logs, Socket: d.sock},
		ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	t.Cleanup(func() {
		if p := onLoop(r, func() *pluginRun { return r.h.plugins.running["kinds"] }); p != nil {
			p.shutdown()
		}
	})
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("kinds", "run"); return true })
	r.waitResults(t, "k1", len(kindsRows))
	if bound := onLoop(r, func() bool {
		dt := r.h.plugins.c.ResultsTable(resultsView)
		return dt != nil && dt.BoundLayout() != widget.TableLayout(dt)
	}); !bound {
		t.Fatal("the results' table is not laid out natively under the GUI style")
	}
	return r
}

// Under --gui's style the results' DataTable is laid out and drawn natively, and Ctrl+V still
// reaches it: a block from the first cell, j and l growing it, the page untouched. Esc ends a
// selection or a count first, and then closes the panel, as in the terminal.
func TestTheGUIResultsTablesKeys(t *testing.T) {
	r := guiResultsTUI(t)
	wantCtrlVBlock(t, r)
	onLoop(r, func() bool { r.resultsCore().SetAnchor(nil); return true })
	wantEscCloses(t, r, func() {
		onLoop(r, func() bool { r.h.plugins.runPluginCommand("kinds", "run"); return true })
		r.waitResults(t, "k1", len(kindsRows))
	})
}

// guiPoint is pointAt for the native table: its layout painted once, 1200 by 400 pixels, a row is
// found down the row numbers and a field along the titles, each by the layout's own HitAt, and a
// cell where the two cross; the point is given in the cells, and the fractions of a cell, a
// pointer event carries.
func guiPoint(t *testing.T, r *running) pointAt {
	t.Helper()
	const w, h = 1200, 400
	onLoop(r, func() bool {
		c := gui.NewRecordingCanvas(gui.Size{W: w, H: h}, gui.Size{W: 8, H: 16})
		c.TextPx = 14
		c.Colors = func(int, int) (color.NRGBA, color.NRGBA) {
			return color.NRGBA{R: 0xee, G: 0xee, B: 0xee, A: 0xff}, color.NRGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff}
		}
		r.h.plugins.c.ResultsTable(resultsView).BoundLayout().(gui.View).Paint(c)
		return true
	})
	type found struct {
		x, y float32
		ok   bool
	}
	return func(row, col int, hit widget.TableHit) (int, int, float32, float32) {
		t.Helper()
		var f found
		var cw, ch float32
		onLoop(r, func() bool {
			dt := r.h.plugins.c.ResultsTable(resultsView)
			hitter := dt.BoundLayout().(widget.TableLayoutHitter)
			cw, ch = 1, 1
			if cp := dt.Context().CellPixels(); cp.W > 0 && cp.H > 0 {
				cw, ch = cp.W, cp.H
			}
			// the row's middle, down the row numbers; the field's, along the titles
			rowY := func() (float32, bool) {
				for y := float32(1); y < h; y++ {
					if at, got := hitter.HitAt(2, y); got == widget.TableHitGutter && at.Row == row {
						return y + 4, true
					}
				}
				return 0, false
			}
			colX := func() (float32, bool) {
				for x := float32(1); x < w; x++ {
					if at, got := hitter.HitAt(x, 2); got == widget.TableHitHeader && at.Col == col {
						return x + 4, true
					}
				}
				return 0, false
			}
			switch hit {
			case widget.TableHitGutter:
				y, ok := rowY()
				f = found{2, y, ok}
			case widget.TableHitHeader:
				x, ok := colX()
				f = found{x, 2, ok}
			default:
				y, oky := rowY()
				x, okx := colX()
				at, got := hitter.HitAt(x, y)
				f = found{x, y, oky && okx && got == hit && at == widget.TablePos{Row: row, Col: col}}
			}
			return true
		})
		if !f.ok {
			t.Fatalf("the native table has no point for row %d, field %d, %v", row, col, hit)
		}
		fx, fy := f.x/cw, f.y/ch
		return int(fx), int(fy), fx - float32(int(fx)), fy - float32(int(fy))
	}
}

// TestTheGUIPointerSelectsWhatIsCopied: the terminal's pointer cell, over the native table's
// pixels: the drags, the title clicked and the right press select and copy as they do there.
func TestTheGUIPointerSelectsWhatIsCopied(t *testing.T) {
	r := guiResultsTUI(t)
	wantPointerSelections(t, r, guiPoint(t, r))
}
