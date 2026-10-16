package tui

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// CARD PANELS — a card plugin (a dialog with [dialog] modal = false) is a docked panel of its own,
// as the results panel is: it opens at the edge its placement names, moves and resizes as every
// panel does (Alt/Option-left drag, Alt/Option-right drag, Go › Arrange panel), and keeps its size
// and its floating rectangle in the preferences under its panel's id (tui.card_<name>.*).
//
// The panels are written into main.qml as the program is built, one for each card plugin found
// then (cardPanelsQML): a plugin added while AutoDoc runs has its panel from the next start.
//
// Closing the panel hides the card and never stops its plugin: Esc while the panel has the keys,
// its right-click menu's Close, or the plugin's host.close. The plugin is told plugin.hide and runs
// on, its tables and its session with it; a command, its Plugins menu entry or its toggle opens the
// panel again (plugin.show). It stops when the user turns it off (its toggle while the panel is
// open, its Plugins menu's Turn off, Manage plugins), when it exits, or when AutoDoc quits.

// cardPanelsMarker is the line of main.qml the card panels are written after.
const cardPanelsMarker = "    // ---- the card plugins' panels ----\n"

// cardPanelPrefix begins every card panel's id; the plugin's name follows, its - as _.
const cardPanelPrefix = "card_"

// cardPanelID is the panel of the card plugin name: its Drawer's id, and the name its preferences
// and the document's App.<id>… values are kept under.
func cardPanelID(name string) string { return cardPanelPrefix + strings.ReplaceAll(name, "-", "_") }

// cardViewID is the card's view in its panel, which takes the keyboard.
func cardViewID(name string) string { return cardPanelID(name) + "View" }

// cardEdge is the edge a placement puts a card's panel at: an edge, or the centre, is itself; a
// corner is its side's (top-right is the right edge).
func cardEdge(place string) string {
	switch place {
	case "top-left", "bottom-left":
		return "left"
	case "top-right", "bottom-right":
		return "right"
	}
	return place
}

// cardPanel is a card plugin's panel, made for the plugin found when the program was built.
type cardPanel struct {
	name  string
	entry pluginEntry // the manifest as it was then: its geometry's defaults
	view  *pluginView // the panel's PluginCard, once the document has built it
}

// fixCards are the card panels, fixed the first time they are asked for: the card plugins found
// when the program is built. Panels, the document's text and its PluginCard type read the same.
func (pl *plugins) fixCards() []*cardPanel {
	if pl.cardOrder != nil {
		return pl.cardOrder
	}
	pl.cardOrder = []*cardPanel{}
	pl.cards = map[string]*cardPanel{}
	for _, e := range pl.pluginList {
		if e.reason != "" || !e.m.card() {
			continue
		}
		c := &cardPanel{name: e.m.Name, entry: e}
		pl.cards[c.name] = c
		pl.cardOrder = append(pl.cardOrder, c)
	}
	return pl.cardOrder
}

// cardSpecs are the card panels' specs.
func (pl *plugins) cardSpecs() []PanelSpec {
	var out []PanelSpec
	for i, c := range pl.fixCards() {
		out = append(out, pl.cardSpec(c, 80+i))
	}
	return out
}

func (pl *plugins) cardSpec(c *cardPanel, priority int) PanelSpec {
	var places []string
	for _, p := range c.entry.m.Dialog.Placements {
		if edge := cardEdge(p); !slices.Contains(places, edge) {
			places = append(places, edge)
		}
	}
	name := c.name
	return PanelSpec{ID: cardPanelID(name), View: cardViewID(name), Priority: priority,
		Edge:   func(p prefs) string { return pl.cardEdgeIn(p, name) },
		Places: places, Size: [2]int{10, 90}, Length: [2]int{20, 100},
		Default: func(edge string) (int, int) { return pl.cardDefault(name, edge) },
		Opened:  func(PanelHost) { pl.cardOpened(name) },
		Closed: func(h PanelHost) {
			pl.cardClosed(name)
			h.Keep(h.RestoreKeyboardPane("editor"))
		}}
}

// cardEntry is the card plugin name as the folder has it now, else as it was found at the start.
func (pl *plugins) cardEntry(name string) pluginEntry {
	if i := slices.IndexFunc(pl.pluginList, func(e pluginEntry) bool { return e.reason == "" && e.m.Name == name }); i >= 0 && pl.pluginList[i].m.card() {
		return pl.pluginList[i]
	}
	return pl.cards[name].entry
}

// cardEdgeIn is the edge the card's panel opens at under p: its placement's.
func (pl *plugins) cardEdgeIn(p prefs, name string) string {
	e := pl.cardEntry(name)
	place := e.m.Dialog.Placements[0]
	if chosen := p.pluginPlace[name]; slices.Contains(e.m.Dialog.Placements, chosen) {
		place = chosen
	}
	return cardEdge(place)
}

// cardDefault is the card's panel at an edge before the user sizes it: the manifest's width and
// height, with the border, as shares of the page; a height in percent is that share.
func (pl *plugins) cardDefault(name, edge string) (size, length int) {
	m := pl.cardEntry(name).m
	pw, ph := pl.c.PageSize()
	if pw <= 0 || ph <= 0 {
		pw, ph = 160, 48 // before the first layout: a terminal of a usual size
	}
	share := func(cells, of int) int { return (cells*100 + of - 1) / of }
	across := share(m.Dialog.Width+2, pw)
	down := m.Dialog.Height.pct
	if down == 0 {
		down = share(m.Dialog.Height.rows+2, ph)
	}
	switch edge {
	case "top", "bottom":
		size, length = down, across
	case "center":
		size, length = down, across
	default:
		size, length = across, down
	}
	return min(max(size, 10), 90), min(max(length, 20), 100)
}

// cardState is what the document reads of every card's panel: its heading, edge, size and length.
// The floating rectangle is every panel's (floatState).
func (pl *plugins) cardState() map[string]any {
	st := map[string]any{}
	for _, c := range pl.fixCards() {
		id := cardPanelID(c.name)
		edge := pl.cardEdgeIn(pl.c.Prefs(), c.name)
		size, length := pl.c.Prefs().panelGeometry(id, edge)
		st["App."+id+"Title"] = c.entry.label()
		st["App."+id+"Edge"] = edge
		st["App."+id+"Size"] = size
		st["App."+id+"Length"] = length
	}
	return st
}

// syncCards sets the card panels' edges and sizes as the preferences and the page now are.
func (pl *plugins) syncCards() {
	for _, c := range pl.fixCards() {
		pl.syncCard(c.name)
	}
}

func (pl *plugins) syncCard(name string) {
	id := cardPanelID(name)
	edge := pl.cardEdgeIn(pl.c.Prefs(), name)
	size, length := pl.c.Prefs().panelGeometry(id, edge)
	pl.c.Set("App."+id+"Edge", edge)
	pl.c.Set("App."+id+"Size", size)
	pl.c.Set("App."+id+"Length", length)
}

// showCard opens the card's panel, its plugin running: focused, when focus says so. A panel already
// open stays as it is but for the focus.
func (pl *plugins) showCard(r *pluginRun, focus bool) {
	id := cardPanelID(r.e.m.Name)
	if !pl.c.PanelOpen(id) {
		pl.syncCard(r.e.m.Name)
		pl.c.Keep(pl.c.View(id, "open"))
	}
	if focus {
		pl.c.Keep(pl.c.View(cardViewID(r.e.m.Name), "forceActiveFocus"))
	}
}

// hideCard closes the card's panel; its plugin runs on (cardClosed tells it).
func (pl *plugins) hideCard(r *pluginRun) {
	if id := cardPanelID(r.e.m.Name); pl.c.PanelOpen(id) {
		pl.c.Keep(pl.c.View(id, "close"))
	}
}

// cardOpened follows the panel opened, however it was: the plugin started when it is not running,
// or told it is shown again.
func (pl *plugins) cardOpened(name string) {
	r := pl.running[name]
	if r == nil {
		pl.openPlugin(name)
		return
	}
	if r.hidden && !r.closing {
		r.hidden = false
		r.send(plugin.MethodShow, plugin.EmptyParams())
	}
}

// cardClosed follows the panel closed, however it was: the plugin is told it is hidden, and runs on.
func (pl *plugins) cardClosed(name string) {
	if r := pl.running[name]; r != nil && !r.hidden && !r.closing {
		r.hidden = true
		r.send(plugin.MethodHide, plugin.EmptyParams())
	}
}

// cardPanelsQML is the card panels, as main.qml declares a panel, for the cards in order.
func cardPanelsQML(cards []*cardPanel) string {
	var b strings.Builder
	for _, c := range cards {
		id := cardPanelID(c.name)
		fmt.Fprintf(&b, `    Panel {
        id: %[1]s
        heading: App.%[1]sTitle
        edge: App.%[1]sEdge
        size: App.%[1]sSize
        length: App.%[1]sLength
        floating: App.%[1]sFloating
        floatX: App.%[1]sX; floatY: App.%[1]sY; floatWidth: App.%[1]sW; floatHeight: App.%[1]sH
        onPlaced: App.panelPlaced(%[2]q, x, y, width, height)
        minimumSize: 10
        onResized: App.panelResized(%[2]q, size, length)
        onOpened: App.panelOpened(%[2]q)
        onClosed: App.panelClosed(%[2]q)
        PluginCard { id: %[3]s; plugin: %[4]q }
    }
`, id, id, cardViewID(c.name), c.name)
	}
	return b.String()
}

// withCardPanels is src with the card panels written after its marker; src as it is when it has
// none (a layout of a test's own).
func (pl *plugins) withCardPanels(src []byte) []byte {
	cards := pl.fixCards()
	i := bytes.Index(src, []byte(cardPanelsMarker))
	if i < 0 || len(cards) == 0 {
		return src
	}
	at := i + len(cardPanelsMarker)
	out := append(slices.Clip(src[:at]), cardPanelsQML(cards)...)
	return append(out, src[at:]...)
}

// cardType is PluginCard, a card panel's view: plugin names the card plugin whose frames it shows
// and whose keys it takes.
func (pl *plugins) cardType() tuidecl.Type {
	return tuidecl.Type{Name: "PluginCard", Ctor: []string{"plugin"}, Build: func(b tuidecl.Build) (tuicore.Component, []string, error) {
		if len(b.Children) != 0 {
			return nil, nil, fmt.Errorf("a PluginCard takes no children (at %s)", b.Pos)
		}
		var name string
		consumed, err := tuidecl.ReadProps(b.Props, map[string]tuidecl.Field{"plugin": tuidecl.StringField(&name)})
		if err != nil {
			return nil, nil, err
		}
		pl.fixCards()
		c := pl.cards[name]
		if c == nil {
			return nil, nil, fmt.Errorf("PluginCard: %q is not a card plugin found at the start (at %s)", name, b.Pos)
		}
		v := &pluginView{pl: pl, card: name}
		c.view = v
		if r := pl.running[name]; r != nil && r.view != nil { // the document built again (a theme)
			v.attach(r)
		}
		return v, consumed, nil
	}}
}

// cardFS is a directory of QML whose main.qml has the card panels written in: -dev's, read again on
// every reload.
type cardFS struct {
	fs.FS
	pl *plugins
}

func (c cardFS) Open(name string) (fs.File, error) {
	f, err := c.FS.Open(name)
	if err != nil || name != "main.qml" {
		return f, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	src, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	data := c.pl.withCardPanels(src)
	return &memFile{Reader: bytes.NewReader(data), info: memInfo{FileInfo: info, size: int64(len(data))}}, nil
}

type memFile struct {
	*bytes.Reader
	info memInfo
}

func (m *memFile) Stat() (fs.FileInfo, error) { return m.info, nil }
func (m *memFile) Close() error               { return nil }

type memInfo struct {
	fs.FileInfo
	size int64
}

func (m memInfo) Size() int64 { return m.size }
