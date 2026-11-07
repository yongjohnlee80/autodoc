package tui

import graphlayout "github.com/yongjohnlee80/golib/graph/layout"

// THE GLOBEMAP'S MODEL, in either build: the links it follows (f), its links' classes, and the
// simulation that places a workspace's documents (golib graph/layout), which a window's GlobeMap
// draws as it settles (globemap_gui.go) and a terminal's once settled, in Braille (globemap_cells.go).

// globeLimits bound a build: a knowledge base of twenty thousand documents.
var globeLimits = graphlayout.Limits{MaxNodes: 20000, MaxEdges: 200000, MaxWork: 2_000_000_000}

const (
	// A node pushes only within globeReach link lengths, and each link is a spring of
	// globeLinkPull: the unlinked documents settle round the linked ones, not on a far ring, and a
	// dense part spreads out rather than drawing tight (golib graph/layout's Reach and LinkPull).
	globeReach    = 8
	globeLinkPull = 0.5
	globeSteps    = 300 // the simulation's steps to settle, which its progress counts
)

// globeFilter is a choice of the links the GlobeMap follows.
type globeFilter struct {
	key, name string // the stored name (prefGlobeFilter), and the title's
	nameMsg   string // name's catalog id: the title's, in the UI's language
	kinds     []any  // graph.map's kinds; nil every kind
}

// globeFilters are the links the GlobeMap follows, f cycling through them: the relations first (the
// default), every link one press away.
var globeFilters = []globeFilter{
	{"relations", "relations", "autodoc.text.globemap_gui.relations",
		[]any{"supersedes", "superseded_by", "amends", "related", "sources", "adr", "review", "blocked"}},
	{"every", "every link", "autodoc.text.globemap_gui.every_link", nil},
	{"body", "body links", "autodoc.text.globemap_gui.body_links", []any{"wikilink", "embed", "markdown"}},
}

// Edge classes, each one path and one colour: body links, supersession, every other relation.
const (
	edgeBody = iota
	edgeSupersession
	edgeRelation
	edgeClasses
)

func edgeClass(kind string) int {
	switch kind {
	case "wikilink", "embed", "markdown":
		return edgeBody
	case "supersedes", "superseded_by":
		return edgeSupersession
	}
	return edgeRelation
}

// globeMapPage is the documents a page of the GlobeMap's map asks for; 0 is the daemon's default.
// A test makes it small, to read a map of a few documents over several pages.
var globeMapPage = 0

// globeForceInput is the simulation of m: seeded, so one map settles the same way each time.
func globeForceInput(m relMap) graphlayout.ForceInput {
	in := graphlayout.ForceInput{Nodes: len(m.Nodes), Seed: 1, Steps: globeSteps, Reach: globeReach, LinkPull: globeLinkPull}
	for _, e := range m.Edges {
		in.Edges = append(in.Edges, graphlayout.Edge{From: e.Src, To: e.Dst})
	}
	return in
}

// degrees counts each document's links.
func degrees(m relMap) []int {
	deg := make([]int, len(m.Nodes))
	for _, e := range m.Edges {
		deg[e.Src]++
		deg[e.Dst]++
	}
	return deg
}
