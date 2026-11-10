//go:build gui

package tui

import (
	"image/color"
	"slices"

	"github.com/yongjohnlee80/golib/gui"
)

// GlobeState is the window GlobeMap's state: the view's, then its camera, its load, its legend and
// its look.
type GlobeState struct {
	ViewState
	Counts       [2]int    // the load's documents and links, once its map is read
	LegendHidden bool      // l hid the legend
	Zoom         float32   // the view asked for: view pixels per layout unit
	Offset       gui.Point // where the layout's origin is asked to be in the view
	Moved        bool      // the user panned or zoomed: no longer fitted
	Filter       int       // the links shown: globeFilters' index
	Dragging     bool      // a drag is under way
	// Frame is the frame on screen and Look how it draws, to compare only: the same one, not
	// loaded or read again.
	Frame, Look any
	// Background and Other are its look's ground and any other document's colour.
	Background, Other color.NRGBA
}

// Snapshot is the window GlobeMap's.
func (g *globeMap) Snapshot() GlobeState {
	l := g.currentLook()
	s := GlobeState{
		ViewState: ViewState{Open: g.open, Viewed: g.view != nil, Groups: groupsOf(g.groups), Builds: g.seq, Editor: editorState(g.editor)},
		Counts:    g.counts, LegendHidden: g.noKey, Zoom: g.zoom, Offset: g.off, Moved: g.moved, Filter: g.filter,
		Dragging: g.drag != nil, Look: l, Background: l.pal.background, Other: l.other,
	}
	if g.cur != nil {
		s.Frame, s.Loaded, s.Nodes = g.cur, true, nodePaths(g.cur.m)
	}
	return s
}

// Snapshot is the window Mindmap's.
func (m *mindmap) Snapshot() ViewState {
	s := ViewState{Open: m.open, Viewed: m.view != nil, Workspace: m.groupsWS, Groups: groupsOf(m.groups),
		Depth: m.depth, Builds: m.seq, Editor: editorState(m.editor)}
	if m.cur != nil {
		s.Loaded, s.Nodes = true, nodePaths(m.cur.m)
	}
	return s
}

// EditGroups is the window GlobeMap's settings' group editor.
func (g *globeMap) EditGroups() *groupEditor { return g.editor }

// EditGroups is the window Mindmap's settings' group editor.
func (m *mindmap) EditGroups() *groupEditor { return m.editor }

// PressNode presses the document at path in the frame on screen, as a click on its dot does; false
// with no frame, or none of its documents at path.
func (g *globeMap) PressNode(path string) bool {
	if g.cur == nil {
		return false
	}
	i := slices.IndexFunc(g.cur.m.Nodes, func(n mapNode) bool { return n.Path == path })
	if i < 0 {
		return false
	}
	g.press(&globeShot{f: g.cur}, i)
	return true
}
