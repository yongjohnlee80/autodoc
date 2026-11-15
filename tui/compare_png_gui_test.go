//go:build gui

package tui

import (
	"bytes"
	"errors"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// compare_png_gui_test.go: pictures of a window's Compare in three themes, for a reader to judge the
// tints and the text on them. Set AUTODOC_COMPARE_PNG_DIR to write them there; it is skipped
// otherwise. The window's frame is its grid of cells (the merge view is drawn in cells), painted
// here cell by cell on golib's headless GPU in the cells' font.

const pngCellW, pngCellH = 9, 18 // dp, a window's usual cell

// pagesOf are each theme's page colours (core/export's table): what a cell with the default colour
// is drawn in.
var pagesOf = map[string][2]string{
	"sepia": {"#e6d9b9", "#5b4636"},
	"dark":  {"#1c1c1c", "#d0d0d0"},
	"light": {"#f6f2e7", "#2e2a24"},
}

func hexColor(s string) color.NRGBA {
	var c [3]uint8
	for i := range c {
		var v uint8
		for _, ch := range s[1+2*i : 3+2*i] {
			v <<= 4
			switch {
			case ch >= '0' && ch <= '9':
				v |= uint8(ch - '0')
			default:
				v |= uint8(ch-'a') + 10
			}
		}
		c[i] = v
	}
	return color.NRGBA{R: c[0], G: c[1], B: c[2], A: 0xff}
}

// cellsView paints a frame's cells: each one's background, then its text in its foreground.
type cellsView struct {
	cells  [][]tuicore.Cell
	fg, bg color.NRGBA
}

func (v cellsView) colour(c tuicore.CellColor, def color.NRGBA) color.NRGBA {
	if c.Kind == tuicore.CellColorRGB {
		return color.NRGBA{R: c.R, G: c.G, B: c.B, A: 0xff}
	}
	return def
}

func (v cellsView) Paint(c gui.Canvas) {
	f := gui.Font{Family: gui.CellFamily, Size: pngCellH / gui.LineHeight}
	for y, row := range v.cells {
		for x, cell := range row {
			fg, bg := v.colour(cell.Attrs.FG, v.fg), v.colour(cell.Attrs.BG, v.bg)
			if cell.Attrs.Mask&tuicore.AttrReverse != 0 {
				fg, bg = bg, fg
			}
			r := gui.Rect{X: float32(x * pngCellW), Y: float32(y * pngCellH), W: pngCellW, H: pngCellH}
			c.FillRect(r, gui.Solid(bg))
			if t := strings.TrimSpace(cell.Content); t != "" {
				c.DrawText(c.Text().Layout(cell.Content, gui.Font{Family: f.Family, Size: f.Size, Bold: cell.Attrs.Mask&tuicore.AttrBold != 0}, 0),
					gui.Point{X: r.X, Y: r.Y}, gui.Solid(fg))
			}
		}
	}
}

// TestPicturesOfAWindowsCompare writes a window's Compare, a shopping list's merge with a conflict,
// a change on each side and a line added, in sepia, dark and light.
func TestPicturesOfAWindowsCompare(t *testing.T) {
	dir := os.Getenv("AUTODOC_COMPARE_PNG_DIR")
	if dir == "" {
		t.Skip("AUTODOC_COMPARE_PNG_DIR is not set")
	}
	opened := "# Shopping list\nApples\nBread\nCheese\nDates\nEggs\n"
	for _, theme := range []string{"sepia", "dark", "light"} {
		t.Run(theme, func(t *testing.T) {
			r, root := onWideDiskAs(t, opened, asWindow)
			onLoop(r, func() bool { r.h.switchTheme(theme); return true })
			r.openOnDisk(t, "a.md")
			onLoop(r, func() bool {
				c := r.h.core
				_, _ = c.Replace(editor.TextRange{From: editor.TextPos{Line: 3}, To: editor.TextPos{Line: 3, Col: 6}}, "Cheddar cheese")
				_, _ = c.Replace(editor.TextRange{From: editor.TextPos{Line: 5, Col: 4}, To: editor.TextPos{Line: 5, Col: 4}}, "\nFlour")
				return true
			})
			r.s.WaitFor(t, "the edits, unsaved", func(string) bool { return r.file().dirty })
			rewrite(t, root, "a.md", "# Shopping list\nGreen apples\nBread\nBlue cheese\nDates\nEggs\n")
			r.s.WaitFor(t, "a.md badged", func(string) bool { return diskOf(r, "a.md") == diskChanged })
			r.openOnDisk(t, "b.md")
			r.openByPicker(t, "a.md")
			r.s.WaitForText(t, "while it had unsaved changes")
			r.compareOpen(t)
			r.s.WaitForText(t, "Blue cheese")
			cells := r.s.Backend.Snapshot()
			pages := pagesOf[theme]
			v := cellsView{cells: cells, bg: hexColor(pages[0]), fg: hexColor(pages[1])}
			img, err := gui.Snapshot(v, gui.Size{W: float32(len(cells[0]) * pngCellW), H: float32(len(cells) * pngCellH)}, 1)
			if errors.Is(err, gui.ErrNoGPU) {
				t.Skip("no headless GPU")
			}
			if err != nil {
				t.Fatal(err)
			}
			var b bytes.Buffer
			if err := png.Encode(&b, img); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "compare-"+theme+".png")
			if err := os.WriteFile(out, b.Bytes(), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Logf("wrote %s", out)
		})
	}
}
