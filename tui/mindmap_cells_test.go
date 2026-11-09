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
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// smallMind is a small graph.map answer: a.md links b, c and d; b links e; c links d.
func smallMind() relMap {
	return relMap{
		Nodes: []mapNode{{Path: "a.md", Title: "Alpha"}, {Path: "b.md", Title: "Beta"}, {Path: "c.md", Title: "Gamma"},
			{Path: "d.md", Title: "Delta"}, {Path: "e.md", Title: "Epsilon"}},
		Edges: []mapEdge{{Src: 0, Dst: 1}, {Src: 0, Dst: 2}, {Src: 0, Dst: 3}, {Src: 1, Dst: 4}, {Src: 2, Dst: 3}},
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

func press(code rune, text string) tuicore.KeyEvent {
	return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: code, Text: text}
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
func (s *cellMindSurface) Size() tuicore.Size               { return tuicore.Size{W: s.w, H: s.h} }
func (s *cellMindSurface) StringWidth(t string) int         { return tuicore.StringWidth(t) }
func (s *cellMindSurface) Theme() *style.Theme              { th := style.DefaultTheme(); return &th }
func (s *cellMindSurface) Caps() tuicore.Capabilities       { return tuicore.Capabilities{} }

var _ tuicore.Surface = (*cellMindSurface)(nil)

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

// TestATerminalRunOpensTheCellMindmap: on a terminal, in either build (go test -tags gui runs it as a
// GUI build's --ui), main.qml has the Relations › Mindmap item, and App.toggleMindmap opens the
// cell Mindmap's panel, its view the cell one.
func TestATerminalRunOpensTheCellMindmap(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	if !strings.Contains(string(withRelationViews(layout, false)), `onTriggered: App.toggleMindmap()`) {
		t.Fatal("a terminal's main.qml has no Relations › Mindmap item")
	}
	m := onLoop(r, func() *cellMindmap {
		for _, f := range r.h.reg.Features {
			if m, ok := f.(*cellMindmap); ok {
				return m
			}
		}
		return nil
	})
	if m == nil {
		t.Fatal("a terminal host has no cell Mindmap registered")
	}
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { m.toggle(); return true })
	r.s.WaitFor(t, "the cell Mindmap open", func(string) bool {
		return onLoop(r, func() bool { return m.open && m.view != nil })
	})
}

// TestSPCMOpensTheMindmap: SPC M (the leader's M, on SPC's card under THIS FILE) opens the Mindmap
// of the document shown, and again closes it. Maximize is SPC z, never SPC M.
func TestSPCMOpensTheMindmap(t *testing.T) {
	d := startManagedIncluding(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")}, tableInclude)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
	m := onLoop(r, func() *cellMindmap {
		for _, f := range r.h.reg.Features {
			if m, ok := f.(*cellMindmap); ok {
				return m
			}
		}
		return nil
	})
	if m == nil {
		t.Fatal("a terminal host has no cell Mindmap registered")
	}
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.leader(t, 'M')
	r.s.WaitFor(t, "SPC M opened the Mindmap", func(string) bool {
		return onLoop(r, func() bool { return m.open && m.view != nil })
	})
	if onLoop(r, func() bool { return r.h.panelMax["terminal"] || r.h.panelMax["agent"] }) {
		t.Error("SPC M maximized a panel")
	}
	r.leader(t, 'M')
	r.s.WaitFor(t, "SPC M closed it", func(string) bool { return !onLoop(r, func() bool { return m.open }) })
}

// TestTheCellMindmapTakesTheKeyboard: Relations › Mindmap opened over a note's page (Rendered) or
// over its text (Raw), from the menu (Alt+L, m, as a reader opens it) or by its command, takes the
// keyboard, as the window's Mindmap does: its next key (d: two links out) is the map's, and the note
// is as it was. Closed, it gives the keyboard back to the editor.
func TestTheCellMindmapTakesTheKeyboard(t *testing.T) {
	for _, c := range []struct {
		name      string
		raw, menu bool
	}{{"the menu, from the page", false, true}, {"the menu, from the text", true, true},
		{"its command, from the page", false, false}, {"its command, from the text", true, false}} {
		raw := c.raw
		t.Run(c.name, func(t *testing.T) {
			r := openRenderedNote(t, "# Pantry\n\nshelf\n", "Pantry")
			if raw {
				r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 't', Mods: tuicore.ModCtrl})
				r.s.WaitFor(t, "the note's text", func(string) bool { return shownView(r) == editor.ViewRaw })
			}
			m := onLoop(r, func() *cellMindmap {
				for _, f := range r.h.reg.Features {
					if m, ok := f.(*cellMindmap); ok {
						return m
					}
				}
				return nil
			})
			before := r.editorText()
			if c.menu {
				r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: 'l', Mods: tuicore.ModAlt})
				r.s.WaitFor(t, "the Relations menu", func(string) bool { return r.focused("menuBar") || strings.Contains(r.s.String(), "Mindmap") })
				r.keys(t, key('m'))
			} else {
				onLoop(r, func() bool { m.toggle(); return true })
			}
			r.s.WaitFor(t, "the Mindmap built", func(string) bool {
				return onLoop(r, func() bool { return m.open && m.cur != nil })
			})
			r.s.WaitFor(t, "the Mindmap has the keyboard", func(string) bool {
				return onLoop(r, func() bool { return r.h.paneHasKeyboard(mindmapViewID) })
			})
			r.keys(t, key('d'))
			r.s.WaitFor(t, "d reached the Mindmap (two links out)", func(string) bool {
				return onLoop(r, func() int { return m.depth }) == 2
			})
			if got := r.editorText(); got != before {
				t.Fatalf("a key for the Mindmap changed the note:\n%q\nwant\n%q", got, before)
			}
			onLoop(r, func() bool { m.toggle(); return true })
			r.s.WaitFor(t, "the editor has the keyboard again", func(string) bool {
				return onLoop(r, func() bool { return !m.open && r.h.paneHasKeyboard("editor") })
			})
		})
	}
}
