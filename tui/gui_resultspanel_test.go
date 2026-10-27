//go:build gui

package tui

import (
	"image/color"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
	tuicore "github.com/yongjohnlee80/golib/tui"
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
	r.connected(t)
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

// nativePointer is the pointer over the native table: its layout painted once, 1200 by 400 pixels,
// a row found down the row numbers and a field along the titles by the layout's own HitAt, and a
// cell where the two cross. The left button's press, drag and release go to the table's core as
// DataTable.HandleEvent hands them on (the pointer's capture is the App's, which a test backend
// with no pixels cannot route by); the right press goes through HandleEvent, in the cells and
// fractions of a cell the backend's pixels make of it.
type nativePointer struct {
	t *testing.T
	r *running
}

const nativeW, nativeH = 1200, 400

func newNativePointer(t *testing.T, r *running) *nativePointer {
	t.Helper()
	onLoop(r, func() bool {
		c := gui.NewRecordingCanvas(gui.Size{W: nativeW, H: nativeH}, gui.Size{W: 8, H: 16})
		c.TextPx = 14
		c.Colors = func(int, int) (color.NRGBA, color.NRGBA) {
			return color.NRGBA{R: 0xee, G: 0xee, B: 0xee, A: 0xff}, color.NRGBA{R: 0x11, G: 0x11, B: 0x11, A: 0xff}
		}
		r.h.plugins.c.ResultsTable(resultsView).BoundLayout().(gui.View).Paint(c)
		return true
	})
	return &nativePointer{t: t, r: r}
}

// point is the pixel over (row, col, hit), and what the layout says is there.
func (p *nativePointer) point(row, col int, hit widget.TableHit) (x, y float32, at widget.TablePos) {
	p.t.Helper()
	var ok bool
	onLoop(p.r, func() bool {
		hitter := p.r.h.plugins.c.ResultsTable(resultsView).BoundLayout().(widget.TableLayoutHitter)
		rowY := func() (float32, bool) {
			for y := float32(1); y < nativeH; y++ {
				if at, got := hitter.HitAt(2, y); got == widget.TableHitGutter && at.Row == row {
					return y + 4, true
				}
			}
			return 0, false
		}
		colX := func() (float32, bool) {
			for x := float32(1); x < nativeW; x++ {
				if at, got := hitter.HitAt(x, 2); got == widget.TableHitHeader && at.Col == col {
					return x + 4, true
				}
			}
			return 0, false
		}
		var okx, oky bool
		switch hit {
		case widget.TableHitGutter:
			x, okx = 2, true
			y, oky = rowY()
		case widget.TableHitHeader:
			y, oky = 2, true
			x, okx = colX()
		default:
			y, oky = rowY()
			x, okx = colX()
		}
		var got widget.TableHit
		at, got = hitter.HitAt(x, y)
		ok = okx && oky && got == hit
		return true
	})
	if !ok {
		p.t.Fatalf("the native table has no point for row %d, field %d, %v", row, col, hit)
	}
	return x, y, at
}

func (p *nativePointer) press(row, col int, hit widget.TableHit) {
	_, _, at := p.point(row, col, hit)
	onLoop(p.r, func() bool { return p.r.resultsCore().PressAt(at, hit) })
}

func (p *nativePointer) dragTo(row, col int, hit widget.TableHit) {
	_, _, at := p.point(row, col, hit)
	onLoop(p.r, func() bool { p.r.resultsCore().DragTo(at); return true })
}

func (p *nativePointer) release() {
	onLoop(p.r, func() bool { p.r.resultsCore().EndDrag(); return true })
}

func (p *nativePointer) rightPress(row, col int, hit widget.TableHit) {
	x, y, _ := p.point(row, col, hit)
	onLoop(p.r, func() bool {
		dt := p.r.h.plugins.c.ResultsTable(resultsView)
		if cp := dt.Context().CellPixels(); cp.W > 0 && cp.H > 0 {
			x, y = x/cp.W, y/cp.H
		}
		return dt.HandleEvent(tuicore.MouseEvent{Kind: tuicore.MousePress,
			Button: tuicore.MouseRight, X: int(x), Y: int(y), SubX: x - float32(int(x)), SubY: y - float32(int(y))})
	})
	waitHandled(p.t, p.r, "the right press", func(*widget.TableCore) bool {
		return p.r.h.plugins.c.ResultsTable(resultsView).Menu().IsOpen()
	})
}

// TestTheGUIPointerSelectsWhatIsCopied: the terminal's pointer cell, over the native table's
// pixels: the drags, the title clicked and the right press select and copy as they do there.
func TestTheGUIPointerSelectsWhatIsCopied(t *testing.T) {
	r := guiResultsTUI(t)
	wantPointerSelections(t, r, newNativePointer(t, r))
}
