package tui

import (
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/plugin"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/style"
)

// cellsSurface is a tuicore.Surface that records what each SetCell put where, measuring widths
// as golib's surfaces do: it shows the columns the host's renderer chose.
type cellsSurface struct {
	w, h  int
	cells map[[2]int]string
}

func (s *cellsSurface) SetCell(x, y int, content string, _ style.Style) {
	s.cells[[2]int{x, y}] = content
}
func (s *cellsSurface) Fill(tuicore.Rect, string, style.Style) {}
func (s *cellsSurface) Sub(tuicore.Rect) tuicore.Surface       { return s }
func (s *cellsSurface) Size() tuicore.Size                     { return tuicore.Size{W: s.w, H: s.h} }
func (s *cellsSurface) StringWidth(str string) int             { return tuicore.StringWidth(str) }
func (s *cellsSurface) Theme() *style.Theme                    { return nil }
func (s *cellsSurface) Caps() tuicore.Capabilities             { return tuicore.Capabilities{} }

// placed is where the host's renderer drew row 0 of f, w columns wide: column → cluster.
func placed(t *testing.T, f *plugin.Frame, w int) map[int]string {
	t.Helper()
	frame, bad := resolveFrame(f.Rows())
	if bad != 0 {
		t.Fatalf("%d colours unknown", bad)
	}
	v := &pluginView{run: &pluginRun{frame: frame}}
	s := &cellsSurface{w: w, h: 1, cells: map[[2]int]string{}}
	v.Render(s)
	out := map[int]string{}
	for at, g := range s.cells {
		if at[1] == 0 && g != " " {
			out[at[0]] = g
		}
	}
	return out
}

// TestTheHostDrawsAFramesClustersInTheirColumns: the host's renderer draws a plugin.Frame a grapheme
// cluster at a time, so what follows a decomposed accent, a ZWJ emoji, a flag or a CJK character
// lands in the column the Frame gave it, across adjacent styles too; ASCII is as before, and a row
// wider than the view is clipped.
func TestTheHostDrawsAFramesClustersInTheirColumns(t *testing.T) {
	red, blue := plugin.Style{FG: "red"}, plugin.Style{FG: "blue"}
	for _, c := range []struct {
		name string
		draw func(f *plugin.Frame)
		w    int
		want map[int]string
	}{
		{"ascii", func(f *plugin.Frame) { f.Text(0, 0, "abc", red) }, 8,
			map[int]string{0: "a", 1: "b", 2: "c"}},
		{"decomposed accent", func(f *plugin.Frame) { f.Text(0, 0, "éa", red) }, 8,
			map[int]string{0: "é", 1: "a"}},
		{"zwj emoji", func(f *plugin.Frame) { f.Text(0, 0, "\U0001f468‍\U0001f469‍\U0001f467b", red) }, 8,
			map[int]string{0: "\U0001f468‍\U0001f469‍\U0001f467", 2: "b"}},
		{"flag", func(f *plugin.Frame) { f.Text(0, 0, "\U0001f1f0\U0001f1f7c", red) }, 8,
			map[int]string{0: "\U0001f1f0\U0001f1f7", 2: "c"}},
		{"cjk, then a style", func(f *plugin.Frame) { f.Text(0, 0, "表", red); f.Text(2, 0, "a", blue) }, 8,
			map[int]string{0: "表", 2: "a"}},
		{"styles meeting at a cluster", func(f *plugin.Frame) {
			f.Text(0, 0, "é", red)
			f.Text(1, 0, "\U0001f1f0\U0001f1f7", blue)
			f.Text(3, 0, "z", red)
		}, 8,
			map[int]string{0: "é", 1: "\U0001f1f0\U0001f1f7", 3: "z"}},
		{"clipped at the view's edge", func(f *plugin.Frame) { f.Text(0, 0, "表表表", red) }, 3,
			map[int]string{0: "表", 2: "表"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := plugin.NewFrame(8, 1)
			c.draw(f)
			if got := placed(t, f, c.w); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("columns = %v, want %v", got, c.want)
			}
		})
	}
}
