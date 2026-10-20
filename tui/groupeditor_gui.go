//go:build gui

package tui

import (
	"image/color"
	"slices"
	"strings"
)

// THE GROUPS EDITOR — the part of the GlobeMap's and the Mindmap's settings that edits a view's
// groups (groups.go): a row a group, in order; the selected one's query and colour (and, the
// Mindmap's, outline) as fields; add, remove, move up and down, and add a group for each schema
// type and folder no group queries. Each edit is saved to the workspace's graph file
// (graphstore_gui.go) and handed to the view, which draws in it at once.

type groupEditor struct {
	c       Core
	store   *graphStore
	view    string // viewGlobeMap or viewMindmap
	name    string // the App names' stem: "globe", "mind"
	shapes  bool   // the Mindmap's: a group has an outline
	changed func([]relGroup)
	ground  func() color.NRGBA // the view's ground, for a new group's colour

	ws    string
	list  []relGroup
	types []string // the workspace's schema types, as graph.map last said
	row   int
}

func (e *groupEditor) source(s string) string { return "App." + e.name + s }

// listed is the view's groups for ws, as read or edited: the dialog shows them.
func (e *groupEditor) listed(ws string, types []string, list []relGroup) {
	e.ws, e.types, e.list = ws, types, list
	e.show("")
}

// show publishes the rows, the selected group's fields, and note (or the groups' word when "").
func (e *groupEditor) show(note string) {
	var rows []string
	for _, g := range e.list {
		row := g.Query + "  " + hexOf(g.Colour)
		if e.shapes {
			row += "  " + mindShapeName(g.Shape)
		}
		rows = append(rows, row)
	}
	e.row = min(max(e.row, 0), max(len(e.list)-1, 0))
	query, colour, shape := "", "", 0 // shape: mindShapes' row
	if e.row < len(e.list) {
		g := e.list[e.row]
		query, colour = g.Query, hexOf(g.Colour)
		for k, m := range mindShapes {
			if m.shape == g.Shape {
				shape = k
			}
		}
	}
	if note == "" {
		note = e.c.Trf("autodoc.text.groupeditor_gui.a_document_wears_the_first_group_its_query_match", e.ws)
	}
	e.c.Set(e.source("GroupRows"), choices(rows...))
	e.c.Set(e.source("GroupRow"), e.row)
	e.c.Set(e.source("GroupQuery"), query)
	e.c.Set(e.source("GroupColour"), colour)
	e.c.Set(e.source("GroupShape"), shape)
	e.c.Set(e.source("GroupsNote"), note)
}

// edit puts list as the groups: saved, shown, and handed to the view.
func (e *groupEditor) edit(list []relGroup) {
	e.list = list
	if e.store != nil && e.ws != "" {
		e.store.save(e.ws, e.view, list)
	}
	e.show("")
	if e.changed != nil {
		e.changed(list)
	}
}

func (e *groupEditor) moved(i int) {
	if i >= 0 && i < len(e.list) && i != e.row {
		e.row = i
		e.show("")
	}
}

// setQuery is the query field, as it is typed: a query that reads applies at once.
func (e *groupEditor) setQuery(text string) {
	if e.row >= len(e.list) {
		return
	}
	if _, err := parseGroupQuery(text); err != nil {
		e.c.Set(e.source("GroupsNote"), err.Error())
		return
	}
	list := slices.Clone(e.list)
	list[e.row].Query = strings.TrimSpace(text)
	e.edit(list)
}

// setColour is the colour field, as it is typed: #rrggbb applies at once.
func (e *groupEditor) setColour(text string) {
	if e.row >= len(e.list) {
		return
	}
	c, ok := parseHex(strings.TrimSpace(text))
	if !ok {
		e.c.Set(e.source("GroupsNote"), e.c.Tr("autodoc.text.groupeditor_gui.a_group_s_colour_is_rrggbb"))
		return
	}
	list := slices.Clone(e.list)
	list[e.row].Colour = c
	e.edit(list)
}

// setShape is the outline chooser's row i (mindShapes) for the group selected.
func (e *groupEditor) setShape(i int) {
	if e.row >= len(e.list) || i < 0 || i >= len(mindShapes) {
		return
	}
	list := slices.Clone(e.list)
	list[e.row].Shape = mindShapes[i].shape
	e.edit(list)
}

// add is a new group after the one selected, a tag's for the user to name, in a colour of the
// palette none of the groups wears when there is one, legible on the view's ground.
func (e *groupEditor) add() {
	ground := e.ground()
	col := onGround(groupPalette[len(e.list)%len(groupPalette)], ground)
	for _, c := range groupPalette {
		if c = onGround(c, ground); !slices.ContainsFunc(e.list, func(g relGroup) bool { return g.Colour == c }) {
			col = c
			break
		}
	}
	at := min(e.row+1, len(e.list))
	e.row = at
	e.edit(slices.Insert(slices.Clone(e.list), at, relGroup{Query: "tag:#tag", Colour: col}))
}

func (e *groupEditor) remove() {
	if e.row < len(e.list) {
		e.edit(slices.Delete(slices.Clone(e.list), e.row, e.row+1))
	}
}

// move moves the group selected by d rows: earlier groups win.
func (e *groupEditor) move(d int) {
	to := e.row + d
	if e.row >= len(e.list) || to < 0 || to >= len(e.list) {
		return
	}
	list := slices.Clone(e.list)
	list[e.row], list[to] = list[to], list[e.row]
	e.row = to
	e.edit(list)
}

// addMissing adds a group for each schema type and folder no group queries.
func (e *groupEditor) addMissing() {
	if e.store == nil || e.ws == "" {
		return
	}
	e.store.addMissing(e.ws, e.view, e.list, e.types, e.ground(), func(list []relGroup) {
		e.list = list
		e.show("")
		if e.changed != nil {
			e.changed(list)
		}
	})
}

// state is the dialog's sources before it first shows.
func (e *groupEditor) state() map[string]any {
	return map[string]any{
		e.source("GroupRows"): choices(), e.source("GroupRow"): 0, e.source("GroupQuery"): "",
		e.source("GroupColour"): "", e.source("GroupShape"): 0, e.source("GroupsNote"): "",
	}
}

// commands are the dialog's.
func (e *groupEditor) commands() map[string]Command {
	return map[string]Command{
		e.source("GroupMoved"):       oneNumber("a row", e.moved),
		e.source("GroupQuerySet"):    oneString("type:<type>, tag:#<tag> or path:<folder>", e.setQuery),
		e.source("GroupColourSet"):   oneString("#rrggbb", e.setColour),
		e.source("GroupShapeSet"):    oneNumber("a row", e.setShape),
		e.source("GroupAdd"):         none(e.add),
		e.source("GroupRemove"):      none(e.remove),
		e.source("GroupUp"):          none(func() { e.move(-1) }),
		e.source("GroupDown"):        none(func() { e.move(1) }),
		e.source("GroupsAddMissing"): none(e.addMissing),
	}
}
