package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuimermaid "github.com/yongjohnlee80/golib/tui/mermaid"
	"github.com/yongjohnlee80/golib/tui/style"
)

// smallMind is a small graph.map answer: a.md links b, c and d; b links e; c links d.
func smallMind() relMap {
	return relMap{
		Nodes: []mapNode{{Path: "a.md", Title: "Alpha"}, {Path: "b.md", Title: "Beta"}, {Path: "c.md", Title: "Gamma"},
			{Path: "d.md", Title: "Delta"}, {Path: "e.md", Title: "Epsilon"}},
		Edges: []mapEdge{{Src: 0, Dst: 1}, {Src: 0, Dst: 2}, {Src: 0, Dst: 3}, {Src: 1, Dst: 4}, {Src: 2, Dst: 3}},
	}
}

// TestTheCellMindmapsArrowsReachEveryBox: from the root, the arrows,
// and h j k l alike, reach every box of the map; a key with no box that way keeps the selection.
func TestTheCellMindmapsArrowsReachEveryBox(t *testing.T) {
	m, v := builtMindmap(t, smallMind(), "a.md", 80, 30)
	n := len(m.cur.grid.Boxes)
	if n != 5 {
		t.Fatalf("the map has %d boxes, want its 5 documents", n)
	}
	for _, keys := range [][]tuicore.KeyEvent{
		{press(tuicore.KeyLeft, ""), press(tuicore.KeyRight, ""), press(tuicore.KeyUp, ""), press(tuicore.KeyDown, "")},
		{press('h', "h"), press('l', "l"), press('k', "k"), press('j', "j")},
	} {
		seen := map[int]bool{0: true}
		for queue := []int{0}; len(queue) > 0; queue = queue[1:] {
			for _, k := range keys {
				m.selected = queue[0]
				if !v.HandleEvent(k) {
					t.Fatalf("%+v was not taken", k)
				}
				if s := m.selected; !seen[s] {
					seen[s] = true
					queue = append(queue, s)
				}
			}
		}
		if len(seen) != n {
			t.Errorf("keys %q reach %d of the %d boxes from the root", keys[0].Text, len(seen), n)
		}
	}
	// the leftmost box has nothing further left: h keeps it
	left := 0
	for i, b := range m.cur.grid.Boxes {
		if x, _ := b.Centre(); x < func() int { x, _ := m.cur.grid.Boxes[left].Centre(); return x }() {
			left = i
		}
	}
	m.selected = left
	v.HandleEvent(press('h', "h"))
	if m.selected != left {
		t.Errorf("h from the leftmost box moved the selection to %d", m.selected)
	}
}

// TestTheCellMindmapsEnterOpensTheSelectedDocument (§5 case 4): Enter on each box opens the
// document its ID names (mindIndex), in the map's workspace: the one the box is labelled with.
func TestTheCellMindmapsEnterOpensTheSelectedDocument(t *testing.T) {
	rm := smallMind()
	m, v := builtMindmap(t, rm, "a.md", 80, 30)
	var opened []string
	m.opens = func(ws, path string) { opened = append(opened, ws+":"+path) }
	for i, b := range m.cur.grid.Boxes {
		m.selected = i
		opened = nil
		if !v.HandleEvent(press(tuicore.KeyEnter, "")) {
			t.Fatal("Enter was not taken")
		}
		var want string
		for j := range rm.Nodes {
			if rm.label(j) == b.Label {
				want = "kb:" + rm.Nodes[j].Path
			}
		}
		if len(opened) != 1 || opened[0] != want {
			t.Errorf("Enter on the box %q opened %v, want %s", b.Label, opened, want)
		}
	}
	m.selected = 0
	opened = nil
	v.HandleEvent(press(tuicore.KeyEnter, ""))
	if len(opened) != 1 || opened[0] != "kb:a.md" {
		t.Errorf("Enter on the root opened %v, want kb:a.md", opened)
	}
}

// cellMindSurface records what a view paints: each cell's first cluster and its style.
type cellMindSurface struct {
	w, h  int
	text  [][]string
	style [][]style.Style
}

func newCellMindSurface(w, h int) *cellMindSurface {
	s := &cellMindSurface{w: w, h: h, text: make([][]string, h), style: make([][]style.Style, h)}
	for y := range h {
		s.text[y], s.style[y] = make([]string, w), make([]style.Style, w)
		for x := range w {
			s.text[y][x] = " "
		}
	}
	return s
}

func (s *cellMindSurface) SetCell(x, y int, content string, st style.Style) {
	for g := range tuicore.Graphemes(content) {
		content = g
		break
	}
	if x >= 0 && x < s.w && y >= 0 && y < s.h {
		s.text[y][x], s.style[y][x] = content, st
	}
}

func (s *cellMindSurface) Fill(r tuicore.Rect, content string, st style.Style) {
	for y := r.Y; y < r.Y+r.H; y++ {
		for x := r.X; x < r.X+r.W; x++ {
			s.SetCell(x, y, content, st)
		}
	}
}

func (s *cellMindSurface) Sub(tuicore.Rect) tuicore.Surface { return s }

func (s *cellMindSurface) Size() tuicore.Size { return tuicore.Size{W: s.w, H: s.h} }

func (s *cellMindSurface) StringWidth(t string) int { return tuicore.StringWidth(t) }

func (s *cellMindSurface) Theme() *style.Theme { th := style.DefaultTheme(); return &th }

func (s *cellMindSurface) Caps() tuicore.Capabilities { return tuicore.Capabilities{} }

// TestTheCellMindmapGolden (§5 case 4): a small map as the terminal draws it, testdata/mindmap_tui
// (-update rewrites it): the root a bang in the middle, its documents in rounded boxes either side,
// joined by box-drawing lines; the selected box (here the root's first child, by l) marked; the
// legend along the bottom (in no groups: the row saying so, then any other document's box).
func TestTheCellMindmapGolden(t *testing.T) {
	m, v := builtMindmap(t, smallMind(), "a.md", 72, 20)
	v.HandleEvent(press('l', "l"))
	s := newCellMindSurface(72, 20)
	v.Render(s)
	var b strings.Builder
	for y := range s.h {
		b.WriteString(strings.TrimRight(strings.Join(s.text[y], ""), " "))
		b.WriteByte('\n')
	}
	sel := m.cur.grid.Boxes[m.selected]
	b.WriteString("selected: " + sel.Label + "\n")
	got := b.String()
	path := filepath.Join("testdata", "mindmap_tui", "small.golden")
	if *updateKeys {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (go test ./tui -run %s -update writes it)", err, t.Name())
	}
	if got != string(want) {
		t.Errorf("the map draws otherwise:\n--- got\n%s--- want\n%s", got, want)
	}
	// where the box is drawn: the grid centred across the view (left) and scrolled (x0, y0)
	x, y := sel.Rect.X+v.left()-m.x0, sel.Rect.Y-m.y0
	if reverse, _ := s.style[y][x].GetReverse(); !reverse || s.text[y][x] == " " {
		t.Errorf("the selected box %q is not drawn in the selection's style at (%d, %d): %q", sel.Label, x, y, s.text[y][x])
	}
}

// builtMindmap is a terminal's Mindmap with rm rooted at root installed, its view w×h cells, as a
// load installs a build.
func builtMindmap(t *testing.T, rm relMap, root string, w, h int) (*cellMindmap, *cellMindView) {
	t.Helper()
	host := newHost(NewSession("unused", nil), Options{})
	var m *cellMindmap
	for _, f := range host.relationViewFeatures() { // the GlobeMap's and the Mindmap's, in cells
		if c, ok := f.(*cellMindmap); ok {
			m = c
		}
	}
	if m == nil {
		t.Fatal("a terminal host has no cell Mindmap")
	}
	v := m.newView().(*cellMindView)
	v.cells = tuicore.Size{W: w, H: h}
	grid, err := tuimermaid.LayMindmap(context.Background(), mindmapModel(rm, root, cellMindShape(root)), cellMeasure{})
	if err != nil {
		t.Fatal(err)
	}
	m.open, m.cur, m.selected = true, &cellMind{root: feature.FileRef{WS: "kb", Path: root}, m: rm, grid: grid}, 0
	return m, v
}

var _ tuicore.Surface = (*cellMindSurface)(nil)
