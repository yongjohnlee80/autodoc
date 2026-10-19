package tui

import (
	"reflect"
	"testing"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// mapAnswer is graph.map's answer as the wire decodes it: nodes [path, title, type, superseded],
// edges [src, dst, kind].
func mapAnswer(nodes [][4]any, edges [][3]string) map[string]any {
	var ns, es []any
	for _, n := range nodes {
		ns = append(ns, map[string]any{"path": n[0], "title": n[1], "type": n[2], "superseded": n[3]})
	}
	for _, e := range edges {
		es = append(es, map[string]any{"src": e[0], "dst": e[1], "kind": e[2]})
	}
	return map[string]any{"nodes": ns, "edges": es}
}

// decodeMap keeps the documents in the answer's order and each link by their indexes; a link to a
// document the answer does not hold is dropped.
func TestDecodeMap(t *testing.T) {
	m := decodeMap(mapAnswer(
		[][4]any{{"a.md", "A", "adr", false}, {"b.md", "", "", true}},
		[][3]string{{"a.md", "b.md", "supersedes"}, {"a.md", "gone.md", "wikilink"}, {"b.md", "a.md", "wikilink"}}))
	want := relMap{
		Nodes: []mapNode{{Path: "a.md", Title: "A", Type: "adr"}, {Path: "b.md", Superseded: true}},
		Edges: []mapEdge{{0, 1, "supersedes"}, {1, 0, "wikilink"}},
	}
	if !reflect.DeepEqual(m, want) {
		t.Fatalf("decoded\n got %+v\nwant %+v", m, want)
	}
	if m.label(0) != "A" || m.label(1) != "b.md" {
		t.Errorf("labels %q, %q: want the title, else the file's name", m.label(0), m.label(1))
	}
	// protocol 28: a node's tags; a tag not a string is left out
	res := mapAnswer([][4]any{{"a.md", "A", "adr", false}}, nil)
	res["nodes"].([]any)[0].(map[string]any)["tags"] = []any{"x", 3, "y"}
	if m := decodeMap(res); !reflect.DeepEqual(m.Nodes[0].Tags, []string{"x", "y"}) {
		t.Errorf("tags %q; want [x y]", m.Nodes[0].Tags)
	}
}

// A feature reads the themes the window can wear through its core: golib's, with no catalog of the
// user's.
func TestCoreNamesTheThemes(t *testing.T) {
	var h Host
	c := core{&h}
	if !reflect.DeepEqual(c.ThemeNames(), h.themeNames()) || !reflect.DeepEqual(c.ThemeLabels(), h.themeLabels()) || len(c.ThemeNames()) == 0 {
		t.Errorf("themes %q, labels %q", c.ThemeNames(), c.ThemeLabels())
	}
}

// The mind map places each document once, under the document that reached it first breadth-first
// from the root, neighbours in path order and links followed both ways; its IDs name the documents'
// indexes, its labels are their titles verbatim, its shapes their types'. A document the root does
// not reach is left out, and a root not in the map is no mind map.
func TestMindmapModel(t *testing.T) {
	m := decodeMap(mapAnswer(
		[][4]any{
			{"a.md", "Root <b>doc</b>", "adr", false}, // 0
			{"b.md", "Same", "review", false},         // 1
			{"c.md", "Same", "", false},               // 2
			{"d/d.md", "", "synthesis", false},        // 3
			{"e.md", "Alone", "pr", false},            // 4
		},
		[][3]string{{"a.md", "b.md", "wikilink"}, {"c.md", "a.md", "related"}, {"b.md", "d/d.md", "wikilink"},
			{"c.md", "d/d.md", "wikilink"}, {"d/d.md", "d/d.md", "embed"}}))
	d := mindmapModel(m, "a.md")
	want := []pm.MindNode{
		{ID: "n0", Label: "Root <b>doc</b>", Shape: pm.MindHexagon, Parent: -1},
		{ID: "n1", Label: "Same", Shape: pm.MindCircle, Parent: 0},
		{ID: "n2", Label: "Same", Shape: pm.MindRounded, Parent: 0},
		{ID: "n3", Label: "d.md", Shape: pm.MindCloud, Parent: 1}, // b.md reached it first
	}
	if d == nil || !reflect.DeepEqual(d.Nodes, want) {
		t.Fatalf("the mind map of a.md\n got %+v\nwant %+v", d, want)
	}
	if got := mindmapModel(m, "c.md").Nodes; got[0].ID != "n2" || got[1].ID != "n0" || got[1].Parent != 0 {
		t.Errorf("rooted at c.md: %+v", got)
	}
	if mindmapModel(m, "nope.md") != nil {
		t.Error("a root not in the map made a mind map")
	}
	for _, c := range []struct {
		id string
		i  int
		ok bool
	}{{"n0", 0, true}, {"n4", 4, true}, {"n5", 0, false}, {"n-1", 0, false}, {"n01", 0, false}, {"x", 0, false}, {"n1x", 0, false}} {
		if i, ok := m.mindIndex(c.id); ok != c.ok || ok && i != c.i {
			t.Errorf("mindIndex(%q) = %d, %v; want %d, %v", c.id, i, ok, c.i, c.ok)
		}
	}
}
