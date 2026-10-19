package tui

import (
	"bytes"
	"image/color"
	"math/rand/v2"
	"path/filepath"

	"github.com/BurntSushi/toml"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// THE GRAPH FILE — the groups the GlobeMap and the Mindmap colour documents by (groups.go), and the
// palette a view's first groups take their colours from. Two files, the workspace's winning:
//
//	<root>/.autodoc/graph.toml   the workspace's: it travels with the KB, as Obsidian's
//	                             .obsidian/graph.json does; written when a view is first initialised
//	graph.toml beside config     the user's: a palette and groups every workspace starts from
//
// A view's groups are the workspace file's when it has them; else they are initialised once, the
// user's groups first, then a group a schema type and a group a folder (groupQueries) not among
// them, and written into the workspace file, which the views' settings then edit.

// graphFile is the workspace file's path, under its root.
func graphFile(root string) string { return filepath.Join(root, ".autodoc", "graph.toml") }

// graphFileHeader opens a graph file AutoDoc writes.
const graphFileHeader = `# AutoDoc's graph groups: how the GlobeMap and the Mindmap colour a document (and the Mindmap
# outlines it). A document wears the first group its query matches: type:<type> (the schema's type
# field), tag:#<tag> (a nested tag too), path:<folder>/. Each view keeps its own list; its settings
# (Relations › GlobeMap settings…, Mindmap settings…) edit this file. palette is the colours a new
# group takes at random, each made legible on the view's theme. Shapes: box, square, pill, circle,
# double circle, hexagon.
`

// graphConf is a graph file.
type graphConf struct {
	Palette  []string `toml:"palette,omitempty"`
	GlobeMap viewConf `toml:"globemap"`
	Mindmap  viewConf `toml:"mindmap"`
}

// viewConf is a view's part of a graph file.
type viewConf struct {
	Groups []confGroup `toml:"groups,omitempty"`
}

// confGroup is a group as a graph file holds it.
type confGroup struct {
	Query  string `toml:"query"`
	Colour string `toml:"colour"`
	Shape  string `toml:"shape,omitempty"`
}

// The views' names in a graph file.
const (
	viewGlobeMap = "globemap"
	viewMindmap  = "mindmap"
)

// parseGraphConf reads a graph file; an empty one is no groups and no palette.
func parseGraphConf(b []byte) (graphConf, error) {
	var c graphConf
	_, err := toml.Decode(string(b), &c)
	return c, err
}

// format is c as AutoDoc writes a graph file.
func (c graphConf) format() string {
	var b bytes.Buffer
	b.WriteString(graphFileHeader)
	_ = toml.NewEncoder(&b).Encode(c) // strings and lists of tables: nothing it refuses
	return b.String()
}

func (c *graphConf) view(name string) *viewConf {
	if name == viewMindmap {
		return &c.Mindmap
	}
	return &c.GlobeMap
}

// palette is c's palette, the colours that read; nil for none.
func (c graphConf) palette() []color.NRGBA {
	var out []color.NRGBA
	for _, s := range c.Palette {
		if col, ok := parseHex(s); ok {
			out = append(out, col)
		}
	}
	return out
}

// groups are view's groups in c; nil for none. A colour that does not read is the default
// palette's of its place, a shape that does not read a box.
func (c graphConf) groups(view string) []relGroup {
	stored := c.view(view).Groups
	if len(stored) == 0 {
		return nil
	}
	out := make([]relGroup, len(stored))
	for k, s := range stored {
		out[k] = relGroup{Query: s.Query, Colour: groupPalette[k%len(groupPalette)]}
		if col, ok := parseHex(s.Colour); ok {
			out[k].Colour = col
		}
		for _, m := range mindShapes {
			if m.name == s.Shape {
				out[k].Shape = m.shape
			}
		}
	}
	return out
}

// setGroups puts list as view's groups in c. The GlobeMap's keep no shape.
func (c *graphConf) setGroups(view string, list []relGroup) {
	stored := make([]confGroup, len(list))
	for i, g := range list {
		stored[i] = confGroup{Query: g.Query, Colour: hexOf(g.Colour)}
		if view == viewMindmap {
			stored[i].Shape = mindShapeName(g.Shape)
		}
	}
	c.view(view).Groups = stored
}

// initialGroups are view's first groups, for the workspace file ws with the user's file user: the
// user's groups for it, then a group each query of queries they lack, each a colour at random from
// the palette (ws's, else the user's, else groupPalette), among those legible on ground.
func initialGroups(view string, ws, user graphConf, queries []string, ground color.NRGBA, rnd *rand.Rand) []relGroup {
	pal := ws.palette()
	if len(pal) == 0 {
		pal = user.palette()
	}
	out := user.groups(view)
	have := map[string]bool{}
	for _, g := range out {
		have[g.Query] = true
	}
	var missing []string
	for _, q := range queries {
		if !have[q] {
			missing = append(missing, q)
		}
	}
	fresh := initGroups(missing, pal, ground, rnd)
	for k := range fresh {
		if k+len(out) >= len(mindAutoShapes) {
			fresh[k].Shape = pm.MindDefault // the automatic outline is by the group's place in the whole list
		} else {
			fresh[k].Shape = mindAutoShapes[k+len(out)]
		}
	}
	return append(out, fresh...)
}
