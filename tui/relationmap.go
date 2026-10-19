package tui

import (
	"bytes"
	"fmt"
	"path"
	"slices"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// THE RELATION VIEWS' DATA — graph.map's answer as the GlobeMap and the Mindmap read it (ADR
// 1792347044), and the panels the GUI writes into main.qml for them. The views themselves are the
// GUI's (globemap_gui.go, mindmap_gui.go); a terminal has Relations › Links alone.

// mapNode is a document of graph.map: its path, title, frontmatter type, whether another document
// supersedes it, and its tags (protocol 28; none from an older daemon).
type mapNode struct {
	Path, Title, Type string
	Superseded        bool
	Tags              []string
}

// mapEdge is a link of graph.map between two of its documents, by their index in relMap.Nodes.
type mapEdge struct {
	Src, Dst int
	Kind     string
}

// relMap is one graph.map answer: its documents, sorted by path, and the links among them.
type relMap struct {
	Nodes []mapNode
	Edges []mapEdge
}

// decodeMap reads graph.map's answer. A link naming a document the answer does not hold is
// dropped, as is anything not shaped as the contract says.
func decodeMap(res any) relMap {
	var m relMap
	at := map[string]int{}
	for _, n := range asList(asMap(res)["nodes"]) {
		nm := asMap(n)
		superseded, _ := nm["superseded"].(bool)
		at[str(nm, "path")] = len(m.Nodes)
		var tags []string
		for _, t := range asList(nm["tags"]) {
			if t, ok := t.(string); ok {
				tags = append(tags, t)
			}
		}
		m.Nodes = append(m.Nodes, mapNode{Path: str(nm, "path"), Title: str(nm, "title"), Type: str(nm, "type"), Superseded: superseded, Tags: tags})
	}
	for _, e := range asList(asMap(res)["edges"]) {
		em := asMap(e)
		src, ok1 := at[str(em, "src")]
		dst, ok2 := at[str(em, "dst")]
		if ok1 && ok2 {
			m.Edges = append(m.Edges, mapEdge{Src: src, Dst: dst, Kind: str(em, "kind")})
		}
	}
	return m
}

// label is how a view names document i: its title, else its file's name.
func (m relMap) label(i int) string {
	if t := m.Nodes[i].Title; t != "" {
		return t
	}
	return path.Base(m.Nodes[i].Path)
}

// mindShape is a mind map node's outline by its document's frontmatter type; any other type, and
// none, is MindDefault, a rounded box: golib draws MindRounded as it draws MindCloud (a synthesis),
// and the legend tells each type's apart. Every node keeps its own ID whatever its shape.
var mindShape = map[string]pm.MindShape{
	"adr": pm.MindHexagon, "convention": pm.MindSquare, "review": pm.MindCircle,
	"synthesis": pm.MindCloud, "pr": pm.MindBang,
}

// mindID is the model ID of document i: the click on its node names it back (mindIndex).
func mindID(i int) string { return fmt.Sprintf("n%d", i) }

// mindIndex is the document a model ID names, and whether it names one of m's.
func (m relMap) mindIndex(id string) (int, bool) {
	var i int
	if _, err := fmt.Sscanf(id, "n%d", &i); err != nil || mindID(i) != id || i < 0 || i >= len(m.Nodes) {
		return 0, false
	}
	return i, true
}

// mindmapModel is m as a mind map rooted at the document root: each document placed once, under
// the one that reached it first breadth-first from root, neighbours taken in path order, links
// followed both ways. A document root does not reach is not in it; nil when root is not in m.
func mindmapModel(m relMap, root string) *pm.MindmapDiagram {
	r := slices.IndexFunc(m.Nodes, func(n mapNode) bool { return n.Path == root })
	if r < 0 {
		return nil
	}
	adj := make([][]int, len(m.Nodes))
	for _, e := range m.Edges {
		if e.Src != e.Dst {
			adj[e.Src] = append(adj[e.Src], e.Dst)
			adj[e.Dst] = append(adj[e.Dst], e.Src)
		}
	}
	at := make([]int, len(m.Nodes)) // a document's index in the model, plus one; 0 not placed
	d := &pm.MindmapDiagram{}
	place := func(i, parent int) {
		d.Nodes = append(d.Nodes, pm.MindNode{ID: mindID(i), Label: m.label(i), Shape: shapeOf(m.Nodes[i].Type), Parent: parent})
		at[i] = len(d.Nodes)
	}
	place(r, -1)
	for q := []int{r}; len(q) > 0; q = q[1:] {
		next := slices.Clone(adj[q[0]])
		slices.Sort(next) // m.Nodes is sorted by path: path order
		for _, j := range next {
			if at[j] == 0 {
				place(j, at[q[0]]-1)
				q = append(q, j)
			}
		}
	}
	return d
}

func shapeOf(typ string) pm.MindShape {
	if s, ok := mindShape[typ]; ok {
		return s
	}
	return pm.MindDefault
}

// globeSettingsElsewhere is App.openGlobeSettings where there is no GlobeMap: a terminal.
const globeSettingsElsewhere = "the GlobeMap and its settings are the window's: run autodoc --gui"

// The lines of main.qml the GUI's relation views are written after: their Relations menu items,
// and their panels.
const (
	relationMenuMarker  = "            // ---- the relation views' menu items (the GUI's) ----\n"
	relationViewsMarker = "    // ---- the relation views' panels (the GUI's) ----\n"
)

// withRelationViews is src with the GUI's relation views written in after their markers when gui
// (the program runs as a window, not only built able to): nothing for a terminal, whatever the
// build (relationMenuQML and relationViewsQML are empty in a build without the GUI), and nothing
// where a marker is missing (a test's own layout).
func withRelationViews(src []byte, gui bool) []byte {
	if !gui {
		return src
	}
	return insertAfter(insertAfter(src, relationMenuMarker, relationMenuQML), relationViewsMarker, relationViewsQML)
}

// insertAfter is src with text written after the line marker, or src as it is when it has no
// such line or text is empty.
func insertAfter(src []byte, marker, text string) []byte {
	i := bytes.Index(src, []byte(marker))
	if i < 0 || text == "" {
		return src
	}
	at := i + len(marker)
	out := append(slices.Clip(src[:at]), text...)
	return append(out, src[at:]...)
}
