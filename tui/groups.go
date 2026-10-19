package tui

import (
	"cmp"
	"fmt"
	"image/color"
	"maps"
	"math/rand/v2"
	"slices"
	"strings"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
	"github.com/yongjohnlee80/golib/tui/style"
)

// GROUPS — how the GlobeMap and the Mindmap colour a document (and the Mindmap outlines it), as
// Obsidian's graph view does: an ordered list of groups, each a query and a colour (and a shape),
// a document wearing the first group it matches. Each view keeps its own list, in the workspace's
// graph file (graphconf.go). A view with none is initialised: a group a type the workspace's schema
// declares (type:<value>, in its order), then a group a top-level folder (path:<folder>/, the
// fullest first), so a document's type wins over its folder; each a colour at random from the
// palette, among those legible on the view's ground.

// relGroup is one group.
type relGroup struct {
	Query  string       // "type:adr", "tag:#project", "path:notes/"
	Colour color.NRGBA  // what its documents wear
	Shape  pm.MindShape // the Mindmap's outline for them
}

// groupPalette is the palette with no graph file to name one: sixteen hues apart (Tableau 10, and
// six more of their weight).
var groupPalette = []color.NRGBA{
	hexColour(0x4e79a7), hexColour(0xf28e2b), hexColour(0xe15759), hexColour(0x76b7b2), hexColour(0x59a14f),
	hexColour(0xedc948), hexColour(0xb07aa1), hexColour(0xff9da7), hexColour(0x9c755f), hexColour(0xbab0ac),
	hexColour(0x17becf), hexColour(0xbcbd22), hexColour(0x7f3c8d), hexColour(0x11a579), hexColour(0xe73f74),
	hexColour(0x3969ac),
}

func hexColour(v uint32) color.NRGBA {
	return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
}

// groupQueries are a view's initial groups' queries: one a type the schema declares, in its order,
// then one a top-level folder of files, the fullest first (by name between equals).
func groupQueries(types, files []string) []string {
	var out []string
	for _, t := range types {
		out = append(out, "type:"+t)
	}
	count := map[string]int{}
	for _, f := range files {
		if dir, _, ok := strings.Cut(f, "/"); ok && dir != "" && !strings.HasPrefix(dir, ".") {
			count[dir]++
		}
	}
	for _, d := range slices.SortedFunc(maps.Keys(count), func(a, b string) int { return cmp.Or(count[b]-count[a], cmp.Compare(a, b)) }) {
		out = append(out, "path:"+d+"/")
	}
	return out
}

// initGroups are the groups of queries, each a colour of palette (groupPalette for none) at random,
// none twice until each is taken, then each a turn further from ground; every colour made legible
// on ground (onGround). Each group takes the automatic outline of its place (mindAutoShapes, then a
// box).
func initGroups(queries []string, palette []color.NRGBA, ground color.NRGBA, rnd *rand.Rand) []relGroup {
	if len(palette) == 0 {
		palette = groupPalette
	}
	out := make([]relGroup, len(queries))
	var deck []color.NRGBA
	turn := -1
	for k, q := range queries {
		if len(deck) == 0 {
			deck = slices.Clone(palette)
			rnd.Shuffle(len(deck), func(i, j int) { deck[i], deck[j] = deck[j], deck[i] })
			turn++
		}
		out[k] = relGroup{Query: q, Colour: onGround(away(deck[0], ground, turn), ground), Shape: pm.MindDefault}
		deck = deck[1:]
		if k < len(mindAutoShapes) {
			out[k].Shape = mindAutoShapes[k]
		}
	}
	return out
}

// onGround is c made legible on ground (golib style.Legible, at 3: WCAG's ratio for graphics).
func onGround(c, ground color.NRGBA) color.NRGBA {
	r, g, b, ok := style.Legible(style.RGB(c.R, c.G, c.B), style.RGB(ground.R, ground.G, ground.B), 3).RGBValues()
	if !ok {
		return c
	}
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}
}

// away is c turn steps further from ground: toward black on a light ground, white on a dark one, a
// fifth of the way a step (at most four fifths); c itself at turn 0.
func away(c, ground color.NRGBA, turn int) color.NRGBA {
	if turn <= 0 {
		return c
	}
	to := color.NRGBA{A: 0xff}
	if contrast(ground, to) < contrast(ground, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}) {
		to = color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff} // a dark ground: away is lighter
	}
	t := min(0.2*float64(turn), 0.8)
	l := func(x, y uint8) uint8 { return uint8(float64(x) + (float64(y)-float64(x))*t + 0.5) }
	return color.NRGBA{R: l(c.R, to.R), G: l(c.G, to.G), B: l(c.B, to.B), A: 0xff}
}

// contrast is WCAG's contrast ratio of a and b (golib style.ContrastRatio): 1 to 21.
func contrast(a, b color.NRGBA) float64 {
	r, _ := style.ContrastRatio(style.RGB(a.R, a.G, a.B), style.RGB(b.R, b.G, b.B))
	return r
}

// groupQuery is a group's query, read: what of a document it matches, and the value.
type groupQuery struct {
	kind  string // "type", "tag" or "path"
	value string // tag: lower case, without its #; path: a prefix ending in /
}

// parseGroupQuery reads a query: type:<value>, tag:<tag> (or #<tag>), path:<folder>.
func parseGroupQuery(q string) (groupQuery, error) {
	q = strings.TrimSpace(q)
	if t, ok := strings.CutPrefix(q, "#"); ok {
		q = "tag:" + t
	}
	kind, v, ok := strings.Cut(q, ":")
	v = strings.TrimSpace(v)
	switch {
	case !ok || v == "":
	case kind == "type":
		return groupQuery{kind, v}, nil
	case kind == "tag":
		if t := strings.ToLower(strings.TrimPrefix(v, "#")); t != "" {
			return groupQuery{kind, t}, nil
		}
	case kind == "path":
		if p := strings.Trim(v, "/"); p != "" {
			return groupQuery{kind, p + "/"}, nil
		}
	}
	return groupQuery{}, fmt.Errorf("%q: a group's query is type:<type>, tag:#<tag> or path:<folder>", q)
}

// matches reports whether document n is of the query.
func (g groupQuery) matches(n mapNode) bool {
	switch g.kind {
	case "type":
		return n.Type == g.value
	case "path":
		return strings.HasPrefix(n.Path, g.value)
	case "tag":
		return slices.ContainsFunc(n.Tags, func(t string) bool {
			t = strings.ToLower(strings.TrimPrefix(t, "#"))
			return t == g.value || strings.HasPrefix(t, g.value+"/") // a nested tag is its parent's too
		})
	}
	return false
}

// relGroups are a view's groups, their queries read: the first that matches a document is its.
type relGroups struct {
	list    []relGroup
	queries []groupQuery
}

// newRelGroups reads list's queries; a group whose query does not read matches nothing.
func newRelGroups(list []relGroup) relGroups {
	g := relGroups{list: list, queries: make([]groupQuery, len(list))}
	for i, x := range list {
		g.queries[i], _ = parseGroupQuery(x.Query)
	}
	return g
}

// ofOr is of, and -1 for no groups at all (g nil).
func (g *relGroups) ofOr(n mapNode) int {
	if g == nil {
		return -1
	}
	return g.of(n)
}

// of is the group document n wears, by its index in the list; -1 for none.
func (g relGroups) of(n mapNode) int {
	for i, q := range g.queries {
		if q.matches(n) {
			return i
		}
	}
	return -1
}

// hexOf is c as #rrggbb.
func hexOf(c color.NRGBA) string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

// parseHex reads a colour given as #rrggbb (golib style.ParseColor, the one reader of colours),
// only one with RGB values: not a terminal's palette slot.
func parseHex(s string) (color.NRGBA, bool) {
	c, err := style.ParseColor(s)
	if err != nil || !strings.HasPrefix(strings.TrimSpace(s), "#") {
		return color.NRGBA{}, false
	}
	r, g, b, ok := c.RGBValues()
	return color.NRGBA{R: r, G: g, B: b, A: 0xff}, ok
}

// groupLegendRows is the most groups a view's legend lists; a row then says how many more.
const groupLegendRows = 12

// groupLabel is a group's words in a legend: its type, #tag or folder/.
func groupLabel(q string) string {
	gq, err := parseGroupQuery(q)
	switch {
	case err != nil:
		return q
	case gq.kind == "tag":
		return "#" + gq.value
	}
	return gq.value
}
