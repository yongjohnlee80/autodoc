package tui

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// TILED LAYOUT — the base is a TileManager (main.qml), its windows the page (a FloatWindow, the fill
// member) and the tileable panels. Each window's Layout is a preference of its own,
// tui.<window>.layout: tiled, the manager places it over a tile while it fits; float, it is at its
// own place, a window or a drawer over the page as before. The page is tiled unless the user
// floats it; every panel floats unless the user tiles it.
//
// A panel's tile is where its edge preference, tui.<window>.tileEdge, puts it, its size across that
// edge tui.<window>.tileSize, a percent of the base. Dragging a shared border keeps the new size;
// the Layout is only ever the user's: a panel the manager releases for want of room is still
// tiled, and tiles again by itself when the room returns.
//
// These are the window's tiling only. Its docked edge and size and its floating rectangle (the
// panel geometry preferences) are its own place, untouched by tiling.

// tileWindow is a window the base may tile: its preference id, and its tile's defaults.
type tileWindow struct {
	id    string // tui.<id>.layout; App.<id>Tiled
	node  string // its member's id in main.qml, the key its tileChanged reports
	tiled bool   // its Layout's default
	edge  string // its tile's edge by default; "" for the page, the fill member
	size  int    // its tile's size by default, a percent of the base across its edge
}

// staticTileWindows are the base's windows main.qml declares: the page, then the tileable panels.
// A card's panel is a window too, one per card plugin (cardTileWindows); tileHost's tileWindows are
// both.
var staticTileWindows = []tileWindow{
	{id: "editor", node: "editorWindow", tiled: true},
	{id: "explorer", node: "explorer", edge: "left", size: 35},
	{id: "terminal", node: "terminal", edge: "bottom", size: 30},
	{id: "agent", node: "agent", edge: "right", size: 35},
	{id: "results", node: "results", edge: "bottom", size: 35},
	{id: "todos", node: "todos", edge: "left", size: 30},
}

// The tile size's bounds, in percent of the base.
const minTileSize, maxTileSize = 5, 90

const (
	layoutTiled = "tiled"
	layoutFloat = "float"
)

func layoutKey(id string) string   { return "tui." + id + ".layout" }
func tileEdgeKey(id string) string { return "tui." + id + ".tileEdge" }
func tileSizeKey(id string) string { return "tui." + id + ".tileSize" }

// tileWindowOf is the window of ws whose member is node, or false.
func tileWindowOf(ws []tileWindow, node string) (tileWindow, bool) {
	i := slices.IndexFunc(ws, func(w tileWindow) bool { return w.node == node })
	if i < 0 {
		return tileWindow{}, false
	}
	return ws[i], true
}

// tileHost is what the tiling needs of its host: the preferences, the windows the base may tile
// (the static ones, then one per card), and setting a window's layoutKey, tileEdgeKey or
// tileSizeKey: a static window's through its Preferences row, a card's as its geometry is kept, by
// its panel's id. Either way the document hears it.
type tileHost interface {
	Prefs() feature.PrefsView
	tileWindows() []tileWindow
	setTile(id, name, value string)
}

// tiles is the base's Layout: the windows' tiling preferences, and what keeps them.
type tiles struct {
	c    feature.Core
	host tileHost // nil for a Core that is no tileHost: the static windows alone
}

func newTiles(c feature.Core) *tiles {
	t := &tiles{c: c}
	t.host, _ = c.(tileHost)
	return t
}

// windows are the windows the tiles keep: the host's, the cards' among them.
func (t *tiles) windows() []tileWindow {
	if t.host == nil {
		return staticTileWindows
	}
	return t.host.tileWindows()
}

// set sets a window's tiling preference name through the host.
func (t *tiles) set(id, name, value string) {
	if t.host == nil {
		t.c.SetPref(name, value)
		return
	}
	t.host.setTile(id, name, value)
}

func (*tiles) Name() string { return "tiles" }

// State is none of its own: the rows show the preferences (App.<id>Tiled, TileEdge and TileSize).
func (*tiles) State() map[string]any { return map[string]any{} }

func (t *tiles) Commands() map[string]feature.Command {
	return map[string]feature.Command{
		// the TileManager's tileChanged: a completed operation on a member's tile
		"App.tileChanged": {
			Args: []qml.SpecValueKind{feature.ArgStr, feature.ArgStr, feature.ArgStr, feature.ArgNum, qml.SpecValueBool},
			What: "a tile's key, its operation, edge and size, and whether it is tiled",
			Run: func(a []qml.SpecValue) error {
				size, err := strconv.ParseFloat(a[3].Raw, 64)
				if err != nil {
					return feature.ArgError("takes a size, not " + a[3].Raw)
				}
				t.tileChanged(a[0].Raw, a[1].Raw, size)
				return nil
			},
		},
	}
}

// Prefs are three per static window: its Layout, and for a panel its tile's edge and size. A card's
// three are kept by its panel's id, as its geometry is (readCardTiles), and are no Preferences rows.
func (*tiles) Prefs() []feature.PrefSpec {
	var rows []feature.PrefSpec
	for _, w := range staticTileWindows {
		def := layoutFloat
		if w.tiled {
			def = layoutTiled
		}
		rows = append(rows, feature.ValuePref(layoutKey(w.id), def, []string{layoutTiled, layoutFloat}).
			Shows(func(p feature.PrefsView) map[string]any {
				return map[string]any{"App." + w.id + "Tiled": p.Value(layoutKey(w.id)) == layoutTiled}
			}))
		if w.edge == "" {
			continue // the page is the fill member: the rest of the base, wherever the tiles are
		}
		rows = append(rows, feature.ValuePref(tileEdgeKey(w.id), w.edge, feature.Edges).
			Shows(func(p feature.PrefsView) map[string]any {
				return map[string]any{"App." + w.id + "TileEdge": p.Value(tileEdgeKey(w.id))}
			}))
		size := feature.ValuePref(tileSizeKey(w.id), strconv.Itoa(w.size), nil).
			Shows(func(p feature.PrefsView) map[string]any {
				n, _ := strconv.Atoi(p.Value(tileSizeKey(w.id)))
				return map[string]any{"App." + w.id + "TileSize": n}
			})
		size.Valid = func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= minTileSize && n <= maxTileSize
		}
		rows = append(rows, size)
	}
	return rows
}

// setLayout is Layout › Tiled or Float for the window id: its preference, live.
func (t *tiles) setLayout(id string, tiled bool) {
	v := layoutFloat
	if tiled {
		v = layoutTiled
	}
	t.set(id, layoutKey(id), v)
}

// tileChanged keeps a shared border's drag as the tile's size. Nothing else is the preferences':
// a member never re-docks on its own (its edge is the preference's), and a release for want of
// room leaves its Layout as the user chose it.
func (t *tiles) tileChanged(node, operation string, size float64) {
	w, ok := tileWindowOf(t.windows(), node)
	if !ok || w.edge == "" || operation != "resize" {
		return
	}
	n := min(max(int(math.Round(size)), minTileSize), maxTileSize)
	t.set(w.id, tileSizeKey(w.id), strconv.Itoa(n))
}

// rowMaker makes a row of the menu Layout › goes in (feature.RowMaker).
type rowMaker = feature.RowMaker

// layoutRows is Layout ›, layoutChoices in a submenu, for the window id. A host that sets no
// preference has none.
func layoutRows(c any, id string, row rowMaker) []widgetcore.MenuItemModel {
	choices := layoutChoices(c, id, row)
	if choices == nil {
		return nil
	}
	sub := widgetcore.NewSubmenu("layout", "Layout", choices)
	sub.Label, sub.Hotkey, sub.HotkeyIdx = tuicore.ParseMnemonic("L&ayout") // never h, j, k or l: they move in the menu
	return []widgetcore.MenuItemModel{feature.WithLabelMessage(sub, "L&ayout")}
}

// tileEdgeRows are Layout › Tiled ›'s rows: the edges a panel's tile docks at, each with its label.
var tileEdgeRows = []struct{ edge, label string }{{"left", "Le&ft"}, {"right", "&Right"}, {"top", "&Top"}, {"bottom", "&Bottom"}}

// layoutChoices are the window id's Layout, Tiled and Float: the one checked is its preference, the
// user's choice (a window released for want of room is still Tiled), and choosing the other
// switches it live. A panel's Tiled is a submenu of the edges its tile docks at
// (tui.<id>.tileEdge): its edge is checked while it is tiled, and choosing one docks it there, tiling
// it first when it floats. The page, the fill, has no edge. nil for a host that sets no preference.
func layoutChoices(c any, id string, row rowMaker) []widgetcore.MenuItemModel {
	h, ok := c.(tileHost)
	if !ok {
		return nil
	}
	w, known := tileWindowByID(h.tileWindows(), id)
	value := func(key, def string) string {
		if v := h.Prefs().Value(key); v != "" {
			return v
		}
		return def
	}
	layoutDefault := layoutFloat
	if w.tiled {
		layoutDefault = layoutTiled
	}
	tiled := value(layoutKey(id), layoutDefault) == layoutTiled
	radio := func(value, label string) widgetcore.MenuItemModel {
		r := row("layout."+value, label, func() { h.setTile(id, layoutKey(id), value) })
		r.Kind, r.Group, r.Checked = widgetcore.ItemKindRadio, "layout", tiled == (value == layoutTiled)
		return r
	}
	float := radio(layoutFloat, "&Float")
	if !known || w.edge == "" {
		return []widgetcore.MenuItemModel{radio(layoutTiled, "&Tiled"), float}
	}
	edge := value(tileEdgeKey(id), w.edge)
	var edges []widgetcore.MenuItemModel
	for _, e := range tileEdgeRows {
		r := row("layout.tiled."+e.edge, e.label, func() {
			h.setTile(id, tileEdgeKey(id), e.edge) // the edge first: a floating panel then tiles once, there
			// the Layout as it is now, not as the menu was built: a reload while it was open may
			// have floated the panel
			if h.Prefs().Value(layoutKey(id)) != layoutTiled {
				h.setTile(id, layoutKey(id), layoutTiled)
			}
		})
		r.Kind, r.Group, r.Checked = widgetcore.ItemKindRadio, "layout.edge", tiled && edge == e.edge
		edges = append(edges, r)
	}
	sub := widgetcore.NewSubmenu("layout.tiled", "Tiled", edges)
	sub.Label, sub.Hotkey, sub.HotkeyIdx = tuicore.ParseMnemonic("&Tiled")
	return []widgetcore.MenuItemModel{feature.WithLabelMessage(sub, "&Tiled"), float}
}

// tileWindowByID is the window of ws whose id is id, or false.
func tileWindowByID(ws []tileWindow, id string) (tileWindow, bool) {
	i := slices.IndexFunc(ws, func(w tileWindow) bool { return w.id == id })
	if i < 0 {
		return tileWindow{}, false
	}
	return ws[i], true
}

// tabsRowOf is a row of a TabView's menu as a rowMaker: a row whatever tab it was opened for.
func tabsRowOf(id, label string, run func()) widgetcore.MenuItemModel {
	text, key, at := tuicore.ParseMnemonic(label)
	row := widgetcore.NewCommand(widgetcore.ItemID(id), text, widgetcore.TabsMenuAction{ID: tuicore.ActionID(id), Run: func(int) { run() }})
	row.Hotkey, row.HotkeyIdx = key, at
	return feature.WithLabelMessage(row, label)
}

// tabViewMenus are the panels whose menu is their TabView's: its view, and the window whose
// Layout › it is.
var tabViewMenus = []struct{ view, window string }{{"sideTabs", "explorer"}, {"todoTabs", "todos"}}

// attachTabViewMenus gives the explorer's and the todos' TabViews their menu, Layout › alone (theirs
// has no Go to ›): on attach, and after a reload, whose views are new.
func (h *Host) attachTabViewMenus() {
	for _, m := range tabViewMenus {
		c, _ := h.p.Find(m.view)
		v, ok := c.(interface {
			SetMenu(build func(i int) []widgetcore.MenuItemModel)
		})
		if !ok {
			h.keep(fmt.Errorf("main.qml declares no TabView with id: %s", m.view))
			continue
		}
		window := m.window
		v.SetMenu(func(int) []widgetcore.MenuItemModel { return layoutRows(core{h}, window, tabsRowOf) })
	}
}

// tileWindowIDs are the windows' ids, in their order, the cards' last: what :layout takes.
func (h *Host) tileWindowIDs() []string {
	ws := core{h}.tileWindows()
	ids := make([]string, len(ws))
	for i, w := range ws {
		ids[i] = w.id
	}
	return ids
}

// layoutEntries are :layout [window]: the window's Layout, Tiled and Float, in a menu over the page
// (the editor's alone, which has no strip in the terminal). Tab completes the windows; one that is
// none says which are.
func (h *Host) layoutEntries() []feature.Entry {
	return []feature.Entry{{ID: "window.layout", Title: "a window's layout", Verb: "layout", Args: "[window]",
		Help:     "Tiled at an edge, or Float, for a window: alone, the editor's; Tab lists the windows",
		Complete: func(editor.CompleteContext) []string { return h.tileWindowIDs() },
		Run: func(x feature.Invocation) error {
			id := strings.TrimSpace(x.Line)
			if id == "" {
				id = "editor"
			}
			if ids := h.tileWindowIDs(); !slices.Contains(ids, id) {
				h.notify(h.trf("autodoc.notice.layout.no_window_s_the_windows_are_s", id, strings.Join(ids, ", ")))
				return nil
			}
			return h.openLayoutMenu(id)
		}}}
}

// openLayoutMenu is :layout's menu, the window id's layoutChoices, at the page's top-left corner.
func (h *Host) openLayoutMenu(id string) error {
	owner, ok := h.editor.(widgetcore.ContextOwner)
	if !ok {
		return errors.New("layout: the page opens no menu")
	}
	m := editor.NewEditorMenu(owner, nil)
	m.SetEnabled(true)
	m.SetRows(func() []widgetcore.MenuItemModel { return layoutChoices(core{h}, id, coreRowOf) })
	if !m.OpenAt(tuicore.Point{X: 1, Y: 1}) {
		return errors.New("layout: the menu has nowhere to open")
	}
	return nil
}

// tiledNow reports whether the window whose member is node is over its tile now (governed): one the
// manager released for want of room is at its own place, though its Layout is still Tiled.
func (h *Host) tiledNow(node string) bool {
	c, _ := h.p.Find("tiles")
	m, ok := c.(interface{ Governed(key string) bool })
	return ok && m.Governed(node)
}
