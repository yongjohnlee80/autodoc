package tui

import (
	"errors"
	"fmt"
	"image/color"
	"maps"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
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
	// protocol 28: a node's tags, and the schema's types in its order; anything else is left out
	res := mapAnswer([][4]any{{"a.md", "A", "adr", false}}, nil)
	res["nodes"].([]any)[0].(map[string]any)["tags"] = []any{"x", 3, "y"}
	res["types"] = []any{"adr", "", 7, "review"}
	if m := decodeMap(res); !reflect.DeepEqual(m.Types, []string{"adr", "review"}) || !reflect.DeepEqual(m.Nodes[0].Tags, []string{"x", "y"}) {
		t.Errorf("types %q, tags %q; want [adr review] and [x y]", m.Types, m.Nodes[0].Tags)
	}
}

// A group's query is type:, tag: (or #) or path:; a document wears the first group it matches, a
// nested tag its parent's, a folder its files'.
func TestGroupsMatchFirst(t *testing.T) {
	for _, bad := range []string{"", "type:", "tag:#", "path:/", "kind:x", "adr"} {
		if _, err := parseGroupQuery(bad); err == nil {
			t.Errorf("%q read", bad)
		}
	}
	gs := newRelGroups([]relGroup{{Query: "#Project"}, {Query: "type:adr"}, {Query: "path:/notes/"}, {Query: "nope"}})
	for _, c := range []struct {
		n    mapNode
		want int
	}{
		{mapNode{Path: "a.md", Type: "adr"}, 1},
		{mapNode{Path: "notes/a.md", Type: "adr"}, 1}, // the type's group comes first
		{mapNode{Path: "notes/a.md"}, 2},
		{mapNode{Path: "notesy/a.md"}, -1},
		{mapNode{Path: "x.md", Type: "adr", Tags: []string{"#project/alpha"}}, 0}, // a nested tag
		{mapNode{Path: "x.md", Tags: []string{"projects"}}, -1},
	} {
		if got := gs.of(c.n); got != c.want {
			t.Errorf("%+v: group %d; want %d", c.n, got, c.want)
		}
	}
	var none *relGroups
	if none.ofOr(mapNode{Type: "adr"}) != -1 {
		t.Error("no groups matched a document")
	}
	if groupLabel("type:adr") != "adr" || groupLabel("#X") != "#x" || groupLabel("path:notes") != "notes/" {
		t.Errorf("labels %q %q %q", groupLabel("type:adr"), groupLabel("#X"), groupLabel("path:notes"))
	}
}

// A view's first groups: one a schema type, in its order, then one a top-level folder, the fullest
// first (hidden ones left out); each a colour at random from the palette, made legible on the
// ground, none twice until each is taken, then a turn further from the ground; the first five
// outlined in turn, then boxes.
func TestInitialGroups(t *testing.T) {
	files := []string{"a.md", "notes/x.md", "notes/y.md", "adrs/z.md", ".autodoc/graph.toml", "b/c/d.md"}
	if got := groupQueries([]string{"adr", "note"}, files); !slices.Equal(got, []string{"type:adr", "type:note", "path:notes/", "path:adrs/", "path:b/"}) {
		t.Fatalf("queries %q", got)
	}
	if got := groupQueries(nil, nil); got != nil {
		t.Errorf("no types, no folders: %q", got)
	}
	// every colour reads on its ground, a light one as a dark one; no two groups alike while the
	// palette lasts, and past it a turn further from the ground, still none alike
	for _, ground := range []color.NRGBA{hexColour(0x1c1c1c), hexColour(0xe8dcc0)} {
		qs := make([]string, len(groupPalette)+6)
		for i := range qs {
			qs[i] = fmt.Sprint("q", i)
		}
		got := initGroups(qs, nil, ground, rand.New(rand.NewPCG(1, 2)))
		seen := map[color.NRGBA]int{}
		for k, g := range got {
			if c := contrast(g.Colour, ground); c < 2.95 {
				t.Errorf("ground %v, group %d: %v, contrast %.2f; want 3", ground, k, g.Colour, c)
			}
			if j, dup := seen[g.Colour]; dup {
				t.Errorf("ground %v: groups %d and %d both %v", ground, j, k, g.Colour)
			}
			seen[g.Colour] = k
		}
	}
	pal := []color.NRGBA{hexColour(0xff0000), hexColour(0x00ff00)}
	gs := initGroups([]string{"a", "b", "c", "d", "e", "f", "g"}, pal, hexColour(0x1c1c1c), rand.New(rand.NewPCG(1, 2)))
	if !slices.Contains(pal, gs[0].Colour) || !slices.Contains(pal, gs[1].Colour) || slices.Contains(pal, gs[2].Colour) {
		t.Errorf("a palette of two: %v, %v, then %v; want both, then a turn of one", gs[0].Colour, gs[1].Colour, gs[2].Colour)
	}
	for k, want := range []pm.MindShape{pm.MindHexagon, pm.MindCircle, pm.MindSquare, pm.MindRounded, pm.MindBang, pm.MindDefault, pm.MindDefault} {
		if gs[k].Shape != want {
			t.Errorf("group %d's outline %v; want %v", k, gs[k].Shape, want)
		}
	}
}

// A graph file keeps each view's groups (the Mindmap's with outlines) and a palette, and reads
// back what it wrote; a view's first groups are the user's file's first, then those it lacks, in a
// colour of the workspace's palette, else the user's, made legible on the ground.
func TestGraphFile(t *testing.T) {
	var c graphConf
	c.Palette = []string{"#112233"}
	c.setGroups(viewGlobeMap, []relGroup{{Query: "type:adr", Colour: hexColour(0x010203), Shape: pm.MindHexagon}})
	c.setGroups(viewMindmap, []relGroup{{Query: "path:notes/", Colour: hexColour(0x040506), Shape: pm.MindCircle}})
	text := c.format()
	if !strings.HasPrefix(text, "# AutoDoc's graph groups") || strings.Contains(text, `shape = "hexagon"`) {
		t.Errorf("the file:\n%s", text)
	}
	back, err := parseGraphConf([]byte(text))
	if err != nil {
		t.Fatal(err)
	}
	if g := back.groups(viewGlobeMap); len(g) != 1 || g[0] != (relGroup{Query: "type:adr", Colour: hexColour(0x010203)}) {
		t.Errorf("the GlobeMap's back: %+v", g)
	}
	if g := back.groups(viewMindmap); len(g) != 1 || g[0] != (relGroup{Query: "path:notes/", Colour: hexColour(0x040506), Shape: pm.MindCircle}) {
		t.Errorf("the Mindmap's back: %+v", g)
	}
	if !slices.Equal(back.palette(), []color.NRGBA{hexColour(0x112233)}) {
		t.Errorf("palette %v", back.palette())
	}
	if _, err := parseGraphConf([]byte("palette = [")); err == nil {
		t.Error("a file that does not read read")
	}
	user := graphConf{Palette: []string{"#f0f0f0"}}
	user.setGroups(viewGlobeMap, []relGroup{{Query: "#todo", Colour: hexColour(0xabcdef)}})
	rnd := rand.New(rand.NewPCG(3, 4))
	got := initialGroups(viewGlobeMap, graphConf{}, user, []string{"#todo", "type:adr"}, hexColour(0x101010), rnd)
	if len(got) != 2 || got[0].Query != "#todo" || got[0].Colour != hexColour(0xabcdef) || got[1].Query != "type:adr" || got[1].Colour != hexColour(0xf0f0f0) {
		t.Errorf("first groups %+v; want the user's #todo, then type:adr in the user's palette", got)
	}
	ws := graphConf{Palette: []string{"#e0e0e0"}}
	if got := initialGroups(viewGlobeMap, ws, user, []string{"type:adr"}, hexColour(0x101010), rnd); got[1].Colour != hexColour(0xe0e0e0) {
		t.Errorf("the workspace's palette: %v; want its colour, over the user's", got[1].Colour)
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
// indexes, its labels are their titles verbatim, its shapes their types' (shape). A document the root does
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
	gs := newRelGroups(initGroups([]string{"type:adr", "type:review", "type:synthesis"}, nil, hexColour(0x101010), rand.New(rand.NewPCG(1, 1))))
	shape := func(n mapNode) pm.MindShape {
		if i := gs.of(n); i >= 0 {
			return gs.list[i].Shape
		}
		return pm.MindDefault
	}
	d := mindmapModel(m, "a.md", shape)
	want := []pm.MindNode{
		{ID: "n0", Label: "Root <b>doc</b>", Shape: pm.MindHexagon, Parent: -1},
		{ID: "n1", Label: "Same", Shape: pm.MindCircle, Parent: 0},
		{ID: "n2", Label: "Same", Shape: pm.MindDefault, Parent: 0},
		{ID: "n3", Label: "d.md", Shape: pm.MindSquare, Parent: 1}, // b.md reached it first
	}
	if d == nil || !reflect.DeepEqual(d.Nodes, want) {
		t.Fatalf("the mind map of a.md\n got %+v\nwant %+v", d, want)
	}
	if got := mindmapModel(m, "c.md", shape).Nodes; got[0].ID != "n2" || got[1].ID != "n0" || got[1].Parent != 0 {
		t.Errorf("rooted at c.md: %+v", got)
	}
	if mindmapModel(m, "nope.md", shape) != nil {
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

// pagedReader answers graph.map from pages, as a protocol-36 daemon does: page i is pages[i], its
// next the cursor of page i+1 ("" after the last). It records the opts it was asked with.
func pagedReader(pages ...map[string]any) (func(map[string]any) (any, error), *[]map[string]any) {
	var asked []map[string]any
	return func(opts map[string]any) (any, error) {
		asked = append(asked, opts)
		i := 0
		if a, ok := opts["after"].(string); ok {
			fmt.Sscanf(a, "p%d", &i)
		}
		page := maps.Clone(pages[i])
		page["next"] = ""
		if i+1 < len(pages) {
			page["next"] = fmt.Sprintf("p%d", i+1)
		}
		return page, nil
	}, &asked
}

func node(p string) map[string]any { return map[string]any{"path": p, "title": strings.ToUpper(p)} }
func edge(src, dst string) map[string]any {
	return map[string]any{"src": src, "dst": dst, "kind": "wikilink"}
}

// readMap (docs/decisions/search.md#clients-read-every-page) reads every page, each asked after the
// last one's next with the caller's options kept: a link to a document of a later page, or of an
// earlier one, is placed; a link whose document never came is dropped; the counts are told after
// each page; the types are the answer's.
func TestReadMapReadsEveryPage(t *testing.T) {
	read, asked := pagedReader(
		map[string]any{"nodes": []any{node("a.md"), node("b.md")}, "edges": []any{edge("a.md", "e.md"), edge("b.md", "a.md")}, "types": []any{"adr"}},
		map[string]any{"nodes": []any{node("c.md"), node("d.md")}, "edges": []any{edge("c.md", "a.md"), edge("d.md", "gone.md")}, "types": []any{"adr"}},
		map[string]any{"nodes": []any{node("e.md")}, "edges": []any{edge("e.md", "c.md")}, "types": []any{"adr"}},
	)
	var told []int
	m, err := readMap(read, map[string]any{"kinds": []any{"wikilink"}}, func(n int) { told = append(told, n) })
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Nodes) != 5 || m.Nodes[4].Path != "e.md" || !slices.Equal(m.Types, []string{"adr"}) {
		t.Errorf("nodes %v, types %v", m.Nodes, m.Types)
	}
	var got []string
	for _, e := range m.Edges {
		got = append(got, m.Nodes[e.Src].Path+">"+m.Nodes[e.Dst].Path)
	}
	if want := []string{"a.md>e.md", "b.md>a.md", "c.md>a.md", "e.md>c.md"}; !slices.Equal(got, want) {
		t.Errorf("edges %v, want %v (d.md's link to a document that never came dropped)", got, want)
	}
	if !slices.Equal(told, []int{2, 4, 5}) {
		t.Errorf("told %v, want the documents read after each page", told)
	}
	if len(*asked) != 3 || (*asked)[0]["after"] != nil || (*asked)[2]["after"] != "p2" || (*asked)[2]["kinds"] == nil {
		t.Errorf("asked %v", *asked)
	}
}

// An older daemon's answer has no next: it is the whole map, read once; a failed page fails the
// map; a next that never ends is refused, not followed for good.
func TestReadMapAtItsEdges(t *testing.T) {
	calls := 0
	m, err := readMap(func(map[string]any) (any, error) {
		calls++
		return map[string]any{"nodes": []any{node("a.md"), node("b.md")}, "edges": []any{edge("a.md", "b.md")}}, nil
	}, map[string]any{}, nil)
	if err != nil || calls != 1 || len(m.Nodes) != 2 || len(m.Edges) != 1 {
		t.Errorf("an answer without next: %d calls, %+v, %v", calls, m, err)
	}
	boom := errors.New("the daemon went away")
	read, _ := pagedReader(map[string]any{"nodes": []any{node("a.md")}}, map[string]any{"nodes": []any{node("b.md")}})
	if _, err := readMap(func(o map[string]any) (any, error) {
		if o["after"] != nil {
			return nil, boom
		}
		return read(o)
	}, map[string]any{}, nil); !errors.Is(err, boom) {
		t.Errorf("a failed second page: %v", err)
	}
	if _, err := readMap(func(map[string]any) (any, error) {
		return map[string]any{"nodes": []any{}, "next": "again"}, nil
	}, map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "no end") {
		t.Errorf("a next that never ends: %v", err)
	}
}
