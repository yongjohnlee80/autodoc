package relations

import (
	"image/color"
	"maps"
	"slices"

	tuimermaid "github.com/yongjohnlee80/golib/tui/mermaid"
)

// THE RELATION VIEWS' SNAPSHOTS: what a view shows now, as plain values, for the
// whole-program tests that stay in the host's package (they read a view as the program left it).
// Nothing draws from it.

// ViewState is a relation view's state: its panel, its map and its groups.
type ViewState struct {
	Open      bool     // the panel is open
	Viewed    bool     // its view is built
	Loaded    bool     // a map is shown
	Nodes     []string // the map's documents, by path
	Selected  string   // the selected document's title, else its path; "" none
	Workspace string   // the workspace its groups were read for; "" none yet
	Groups    []Group  // those groups; nil until read
	Depth     int      // the Mindmap's: the links out from the root it shows
	Builds    uint64   // the loads begun
	// Tints are the terminal Mindmap's boxes' group colours, by box id, and Boxes its laid-out
	// boxes.
	Tints map[string]color.NRGBA
	Boxes []tuimermaid.Box
	// Editor is the settings' group editor.
	Editor EditorState
}

// EditorState is the settings' group editor: the workspace it edits, whether its groups are being
// read, and the groups as it holds them.
type EditorState struct {
	Workspace string
	Loading   bool
	Groups    []Group
}

func editorState(e *groupEditor) EditorState {
	if e == nil {
		return EditorState{}
	}
	return EditorState{Workspace: e.ws, Loading: e.loading, Groups: slices.Clone(e.list)}
}

func groupsOf(gs *relGroups) []Group {
	if gs == nil {
		return nil
	}
	return slices.Clone(gs.list)
}

func nodePaths(m relMap) []string {
	out := make([]string, len(m.Nodes))
	for i, n := range m.Nodes {
		out[i] = n.Path
	}
	return out
}

// Snapshot is the GlobeMap's in cells.
func (g *CellGlobeMap) Snapshot() ViewState {
	s := ViewState{Open: g.open, Viewed: g.view != nil, Workspace: g.groupsWS, Groups: groupsOf(g.groups),
		Builds: g.seq, Editor: editorState(g.editor)}
	if g.cur != nil {
		s.Loaded, s.Nodes = true, nodePaths(g.cur.m)
		if g.selected >= 0 && g.selected < len(g.cur.m.Nodes) {
			s.Selected = g.cur.m.label(g.selected)
		}
	}
	return s
}

// Snapshot is the Mindmap's in cells.
func (m *CellMindmap) Snapshot() ViewState {
	s := ViewState{Open: m.open, Viewed: m.view != nil, Workspace: m.groupsWS, Groups: groupsOf(m.groups),
		Depth: m.depth, Builds: m.seq, Editor: editorState(m.editor)}
	if m.cur != nil {
		s.Loaded, s.Nodes, s.Tints = true, nodePaths(m.cur.m), maps.Clone(m.cur.tints)
		if m.cur.grid != nil {
			s.Boxes = slices.Clone(m.cur.grid.Boxes)
		}
	}
	return s
}

// EditGroups is the terminal GlobeMap's settings' group editor.
func (g *CellGlobeMap) EditGroups() *groupEditor { return g.editor }

// EditGroups is the terminal Mindmap's settings' group editor.
func (m *CellMindmap) EditGroups() *groupEditor { return m.editor }

// UseStore makes s the store the Mindmap and its editor read their groups from.
func (m *CellMindmap) UseStore(s *GraphStore) { m.store, m.editor.store = s, s }

// RelatedState is the Links drawer's: its rows' labels, whether it shows the second ring, and the
// row under its cursor.
type RelatedState struct {
	Rows []string
	Deep bool
	At   int
}

// Snapshot is the Links drawer's.
func (r *Related) Snapshot() RelatedState {
	return RelatedState{Rows: r.labels(), Deep: r.deep, At: r.at}
}
