//go:build gui

package tui

import (
	"fmt"
	"image/color"

	graphlayout "github.com/yongjohnlee80/golib/graph/layout"
	"github.com/yongjohnlee80/golib/gui"
	guimermaid "github.com/yongjohnlee80/golib/gui/mermaid"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/style"
)

// THE RELATION VIEWS' PALETTE AND LEGEND — the colours the GlobeMap and the Mindmap draw with, from
// the theme the host gives their view, and the legend they paint over it.

// paletteOf is a view's colours from a theme's values (document.*, syntax.*), each one the theme
// lacks or gives in a form other than #rrggbb taking def's.
func paletteOf(v map[string]string, def relPalette) relPalette {
	c := func(key string, fallback color.NRGBA) color.NRGBA {
		if c, ok := parseHex(v[key]); ok {
			return c
		}
		return fallback
	}
	p := relPalette{
		background: c("document.base", def.background), text: c("document.text", def.text),
		muted: c("document.lineNumber", def.muted), accent: c("document.highlight", def.accent),
		superseded: c("syntax.alert", def.superseded), relation: c("syntax.attribute", def.relation),
		types: map[string]color.NRGBA{},
	}
	for t, key := range map[string]string{"adr": "syntax.keyword", "review": "syntax.string",
		"convention": "syntax.dataType", "synthesis": "syntax.decVal", "pr": "syntax.specialChar"} {
		p.types[t] = c(key, def.types[t])
	}
	return p
}

// relPalette is a view's colours.
type relPalette struct {
	background, text, muted, accent, superseded, relation color.NRGBA
	types                                                 map[string]color.NRGBA // a document's colour by its frontmatter type
}

// relationTypes are the frontmatter types drawn in colours of their own; any other is text's.
var relationTypes = []string{"adr", "review", "convention", "synthesis", "pr"}

// defaultPalette is dark.qml's colours: what a view wears before a theme gives it others.
func defaultPalette() relPalette {
	hex := func(v uint32) color.NRGBA {
		return color.NRGBA{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v), A: 0xff}
	}
	return relPalette{
		background: hex(0x1c1c1c), text: hex(0xd0d0d0), muted: hex(0x5f5f5f), accent: hex(0x87afd7),
		superseded: hex(0xff5f5f), relation: hex(0xafafd7),
		types: map[string]color.NRGBA{"adr": hex(0x87afd7), "review": hex(0xd7af87), "convention": hex(0x87d7af),
			"synthesis": hex(0xafd7ff), "pr": hex(0xff8787)},
	}
}

// mixNRGBA is a blended with b by t: 0 is a, 1 is b.
func mixNRGBA(a, b color.NRGBA, t float32) color.NRGBA {
	l := func(x, y uint8) uint8 { return uint8(float32(x)*(1-t) + float32(y)*t + 0.5) }
	return color.NRGBA{R: l(a.R, b.R), G: l(a.G, b.G), B: l(a.B, b.B), A: l(a.A, b.A)}
}

// A VIEW'S LEGEND — what its marks stand for, on a plate in its bottom right corner (l hides it, and
// shows it again). Each row is a mark, drawn in a box of legendMarkW by legendMarkH, and its words.

const (
	legendRowH  = 18
	legendMarkW = 24
	legendMarkH = 13
	legendPad   = 8
)

// legendRow is one row of a legend. A text in AutoDoc's words is a catalog message, msg with its
// %1, %2 … filled by args: legendIn puts it in the UI's language, before the rows are drawn. A
// group's query is shown as written.
type legendRow struct {
	mark func(c gui.Canvas, box gui.Rect)
	text string
	msg  string // text's catalog id; "" text is not one
	args []any
}

// legendMsg is a row whose text is the catalog message id, filled with args; legendIn writes it.
func legendMsg(mark func(gui.Canvas, gui.Rect), id string, args ...any) legendRow {
	return legendRow{mark: mark, msg: id, args: args}
}

// legendIn is rows in the UI's language, by trf (a Core's Trf): each catalog message's text again.
func legendIn(rows []legendRow, trf func(id string, args ...any) string) []legendRow {
	for i, r := range rows {
		if r.msg != "" {
			rows[i].text = trf(r.msg, r.args...)
		}
	}
	return rows
}

// paintLegend draws rows on a plate in the bottom right of a view of size sz, in pal's colours:
// nothing when they do not fit.
func paintLegend(c gui.Canvas, sz gui.Size, pal relPalette, rows []legendRow) {
	if len(rows) == 0 {
		return
	}
	f := gui.Font{Size: 12}
	texts := make([]*gui.TextLayout, len(rows))
	w := float32(0)
	for i, r := range rows {
		texts[i] = c.Text().Layout(r.text, f, 240)
		w = max(w, texts[i].Width)
	}
	pw, ph := 2*legendPad+legendMarkW+8+w, 2*legendPad+float32(len(rows))*legendRowH
	if pw+24 > sz.W || ph+24 > sz.H {
		return
	}
	plate := gui.Rect{X: sz.W - pw - 12, Y: sz.H - ph - 12, W: pw, H: ph}
	c.FillRRect(plate, 6, gui.Solid(mixNRGBA(pal.background, pal.text, 0.07)))
	c.StrokeRRect(plate, 6, 1, gui.Solid(mixNRGBA(pal.background, pal.text, 0.25)))
	for i, r := range rows {
		y := plate.Y + legendPad + float32(i)*legendRowH
		r.mark(c, gui.Rect{X: plate.X + legendPad, Y: y + (legendRowH-legendMarkH)/2, W: legendMarkW, H: legendMarkH})
		c.DrawText(texts[i], gui.Pt(plate.X+legendPad+legendMarkW+8, y+(legendRowH-texts[i].Height)/2), gui.Solid(pal.text))
	}
}

// legendDot is a dot of colour col, r across, centred in its box.
func legendDot(col color.NRGBA, r float32) func(gui.Canvas, gui.Rect) {
	return func(c gui.Canvas, b gui.Rect) {
		c.FillEllipse(gui.Rect{X: b.X + b.W/2 - r/2, Y: b.Y + b.H/2 - r/2, W: r, H: r}, gui.Solid(col))
	}
}

// legendRing is a ring of colour col round a dot of colour dot.
func legendRing(dot, col color.NRGBA) func(gui.Canvas, gui.Rect) {
	return func(c gui.Canvas, b gui.Rect) {
		legendDot(dot, 7)(c, b)
		p := &gui.Path{}
		circle(p, graphlayout.Point{X: b.X + b.W/2, Y: b.Y + b.H/2}, 6)
		c.StrokePath(p, 1.5, gui.Solid(col))
	}
}

// legendLine is a link of colour col across its box.
func legendLine(col color.NRGBA) func(gui.Canvas, gui.Rect) {
	return func(c gui.Canvas, b gui.Rect) {
		c.StrokePath((&gui.Path{}).MoveTo(gui.Pt(b.X, b.Y+b.H/2)).LineTo(gui.Pt(b.X+b.W, b.Y+b.H/2)), 2, gui.Solid(col))
	}
}

// legendForm is a Mindmap node's outline by its shape, in th's node colours: the map's own
// (gui/mermaid.PaintMindShape).
func legendForm(s pm.MindShape, th guimermaid.Theme) func(gui.Canvas, gui.Rect) {
	return func(c gui.Canvas, b gui.Rect) {
		guimermaid.PaintMindShape(c, s, b, th.NodeFill, th.NodeStroke, 1, max(th.Font.Size, 8)*0.6)
	}
}

// groupLegend are a legend's rows for the groups of gs that documents of m wear, in the groups'
// order, each its mark; at most groupLegendRows, then a row saying how many more. None at all says
// how to have some.
func groupLegend(gs *relGroups, m relMap, mark func(relGroup) func(gui.Canvas, gui.Rect)) []legendRow {
	none := func(gui.Canvas, gui.Rect) {}
	if gs == nil || len(gs.list) == 0 {
		return []legendRow{legendMsg(none, "autodoc.text.relationviews_gui.no_groups_no_schema_types_or_folders_settings_ad")}
	}
	shown, more := legendGroups(gs, m)
	var rows []legendRow
	for _, g := range shown {
		rows = append(rows, legendRow{mark: mark(g), text: groupLabel(g.Query)})
	}
	if more > 0 {
		rows = append(rows, legendMsg(none, "autodoc.text.relationviews_gui.more_groups", more))
	}
	return rows
}

// paletteFields reads each colour property into its place.
func paletteFields(cs map[string]*style.Color) map[string]tuidecl.Field {
	out := map[string]tuidecl.Field{}
	for k, c := range cs {
		out[k] = tuidecl.ColorField(c)
	}
	return out
}

// paletteProps are a view's colour properties.
var paletteProps = append([]string{"background", "text", "muted", "accent", "superseded", "relation"}, relationTypes...)

// relationViewType is the QML type of a view: name, built by build from its palette, and dropped
// by gone. A view takes no children. Its colours are constructor properties: a theme switch
// changes them, and the document builds the view again (gone runs for the old one). A colour the
// theme gives without RGB values (a terminal's palette index) keeps defaultPalette's.
func relationViewType(name string, build func(relPalette) tuicore.Component, gone func(tuicore.Component)) tuidecl.Type {
	return tuidecl.Type{Name: name, Ctor: paletteProps, Destroyed: gone, Build: func(b tuidecl.Build) (tuicore.Component, []string, error) {
		if len(b.Children) != 0 {
			return nil, nil, fmt.Errorf("a %s takes no children (at %s)", name, b.Pos)
		}
		names := paletteProps
		cs := map[string]*style.Color{}
		for _, n := range names {
			cs[n] = new(style.Color)
		}
		consumed, err := b.ReadProps(paletteFields(cs))
		if err != nil {
			return nil, nil, err
		}
		def := defaultPalette()
		rgb := func(c *style.Color, fallback color.NRGBA) color.NRGBA {
			if r, g, bl, ok := c.RGBValues(); ok {
				return color.NRGBA{R: r, G: g, B: bl, A: 0xff}
			}
			return fallback
		}
		p := relPalette{
			background: rgb(cs["background"], def.background), text: rgb(cs["text"], def.text),
			muted: rgb(cs["muted"], def.muted), accent: rgb(cs["accent"], def.accent),
			superseded: rgb(cs["superseded"], def.superseded), relation: rgb(cs["relation"], def.relation),
			types: map[string]color.NRGBA{},
		}
		for _, t := range relationTypes {
			p.types[t] = rgb(cs[t], def.types[t])
		}
		return build(p), consumed, nil
	}}
}
