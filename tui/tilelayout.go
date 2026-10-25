package tui

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
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

// tileWindows are the base's windows: the page, then the tileable panels.
var tileWindows = []tileWindow{
	{id: "editor", node: "editorWindow", tiled: true},
	{id: "explorer", node: "explorer", edge: "left", size: 30},
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

// tileWindowOf is the window whose member is node, or false.
func tileWindowOf(node string) (tileWindow, bool) {
	i := slices.IndexFunc(tileWindows, func(w tileWindow) bool { return w.node == node })
	if i < 0 {
		return tileWindow{}, false
	}
	return tileWindows[i], true
}

// tiles is the base's Layout: the windows' tiling preferences, and what keeps them.
type tiles struct{ c Core }

func newTiles(c Core) *tiles { return &tiles{c: c} }

func (*tiles) Name() string { return "tiles" }

// State is none of its own: the rows show the preferences (App.<id>Tiled, TileEdge and TileSize).
func (*tiles) State() map[string]any { return map[string]any{} }

func (t *tiles) Commands() map[string]Command {
	return map[string]Command{
		// the TileManager's tileChanged: a completed operation on a member's tile
		"App.tileChanged": {
			Args: []qml.SpecValueKind{argStr, argStr, argStr, argNum, qml.SpecValueBool},
			What: "a tile's key, its operation, edge and size, and whether it is tiled",
			Run: func(a []qml.SpecValue) error {
				size, err := strconv.ParseFloat(a[3].Raw, 64)
				if err != nil {
					return argError("takes a size, not " + a[3].Raw)
				}
				t.tileChanged(a[0].Raw, a[1].Raw, size)
				return nil
			},
		},
	}
}

// Prefs are three per window: its Layout, and for a panel its tile's edge and size.
func (*tiles) Prefs() []PrefSpec {
	var rows []PrefSpec
	for _, w := range tileWindows {
		def := layoutFloat
		if w.tiled {
			def = layoutTiled
		}
		rows = append(rows, ValuePref(layoutKey(w.id), def, []string{layoutTiled, layoutFloat}).
			shows(func(p prefs) map[string]any {
				return map[string]any{"App." + w.id + "Tiled": p.Value(layoutKey(w.id)) == layoutTiled}
			}))
		if w.edge == "" {
			continue // the page is the fill member: the rest of the base, wherever the tiles are
		}
		rows = append(rows, ValuePref(tileEdgeKey(w.id), w.edge, edges).
			shows(func(p prefs) map[string]any {
				return map[string]any{"App." + w.id + "TileEdge": p.Value(tileEdgeKey(w.id))}
			}))
		size := ValuePref(tileSizeKey(w.id), strconv.Itoa(w.size), nil).
			shows(func(p prefs) map[string]any {
				n, _ := strconv.Atoi(p.Value(tileSizeKey(w.id)))
				return map[string]any{"App." + w.id + "TileSize": n}
			})
		size.valid = func(v string) bool {
			n, err := strconv.Atoi(v)
			return err == nil && n >= minTileSize && n <= maxTileSize
		}
		rows = append(rows, size)
	}
	return rows
}

// tiled reports whether the window id's Layout is Tiled: the user's choice, whether or not the
// manager places it now (one released for want of room is still Tiled).
func (t *tiles) tiled(id string) bool { return t.c.Prefs().Value(layoutKey(id)) == layoutTiled }

// setLayout is Layout › Tiled or Float for the window id: its preference, live.
func (t *tiles) setLayout(id string, tiled bool) {
	v := layoutFloat
	if tiled {
		v = layoutTiled
	}
	t.c.SetPref(layoutKey(id), v)
}

// tileChanged keeps a shared border's drag as the tile's size. Nothing else is the preferences':
// a member never re-docks on its own (its edge is the preference's), and a release for want of
// room leaves its Layout as the user chose it.
func (t *tiles) tileChanged(node, operation string, size float64) {
	w, ok := tileWindowOf(node)
	if !ok || w.edge == "" || operation != "resize" {
		return
	}
	n := min(max(int(math.Round(size)), minTileSize), maxTileSize)
	t.c.SetPref(tileSizeKey(w.id), strconv.Itoa(n))
}

// rowMaker makes a row of the menu Layout › goes in, run when chosen. Each menu runs its own action
// type (a buffer strip's, a panel's tabs', a TabView's, a table's), so Layout › is built of rows its
// menu makes.
type rowMaker func(id, label string, run func()) widget.MenuItemModel

// layoutHost is what Layout › needs of its host: the preferences, and setting one through its row.
type layoutHost interface {
	Prefs() prefs
	SetPref(name, value string)
}

// layoutRows is Layout ›, layoutChoices in a submenu, for the window id. A host that sets no
// preference has none.
func layoutRows(c any, id string, row rowMaker) []widget.MenuItemModel {
	choices := layoutChoices(c, id, row)
	if choices == nil {
		return nil
	}
	sub := widget.NewSubmenu("layout", "Layout", choices)
	sub.Label, sub.Hotkey, sub.HotkeyIdx = tuicore.ParseMnemonic("L&ayout") // never h, j, k or l: they move in the menu
	return []widget.MenuItemModel{withLabelMessage(sub, "L&ayout")}
}

// layoutChoices are the window id's Layout, Tiled and Float: the one checked is its preference, the
// user's choice (a window released for want of room is still Tiled), and choosing the other
// switches it live. nil for a host that sets no preference.
func layoutChoices(c any, id string, row rowMaker) []widget.MenuItemModel {
	h, ok := c.(layoutHost)
	if !ok {
		return nil
	}
	tiled := h.Prefs().Value(layoutKey(id)) == layoutTiled
	radio := func(value, label string) widget.MenuItemModel {
		r := row("layout."+value, label, func() { h.SetPref(layoutKey(id), value) })
		r.Kind, r.Group, r.Checked = widget.ItemKindRadio, "layout", tiled == (value == layoutTiled)
		return r
	}
	return []widget.MenuItemModel{radio(layoutTiled, "&Tiled"), radio(layoutFloat, "&Float")}
}

// tabsRowOf is a row of a TabView's menu as a rowMaker: a row whatever tab it was opened for.
func tabsRowOf(id, label string, run func()) widget.MenuItemModel {
	text, key, at := tuicore.ParseMnemonic(label)
	row := widget.NewCommand(widget.ItemID(id), text, widget.TabsMenuAction{ID: tuicore.ActionID(id), Run: func(int) { run() }})
	row.Hotkey, row.HotkeyIdx = key, at
	return withLabelMessage(row, label)
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
			SetMenu(build func(i int) []widget.MenuItemModel)
		})
		if !ok {
			h.keep(fmt.Errorf("main.qml declares no TabView with id: %s", m.view))
			continue
		}
		window := m.window
		v.SetMenu(func(int) []widget.MenuItemModel { return layoutRows(core{h}, window, tabsRowOf) })
	}
}

// tileWindowIDs are the windows' ids, in their order: what :layout takes.
func tileWindowIDs() []string {
	ids := make([]string, len(tileWindows))
	for i, w := range tileWindows {
		ids[i] = w.id
	}
	return ids
}

// layoutEntries are :layout [window]: the window's Layout, Tiled and Float, in a menu over the page
// (the editor's alone, which has no strip in the terminal). Tab completes the windows; one that is
// none says which are.
func (h *Host) layoutEntries() []Entry {
	return []Entry{{ID: "window.layout", Title: "a window's layout", Verb: "layout", Args: "[window]",
		Help:     "Tiled or Float for a window: alone, the editor's; Tab lists the windows",
		Complete: func(widget.CompleteContext) []string { return tileWindowIDs() },
		Run: func(x Invocation) error {
			id := strings.TrimSpace(x.Line)
			if id == "" {
				id = "editor"
			}
			if !slices.Contains(tileWindowIDs(), id) {
				h.notify(h.trf("autodoc.notice.layout.no_window_s_the_windows_are_s", id, strings.Join(tileWindowIDs(), ", ")))
				return nil
			}
			return h.openLayoutMenu(id)
		}}}
}

// openLayoutMenu is :layout's menu, the window id's layoutChoices, at the page's top-left corner.
func (h *Host) openLayoutMenu(id string) error {
	owner, ok := h.editor.(widget.ContextOwner)
	if !ok {
		return errors.New("layout: the page opens no menu")
	}
	m := widget.NewEditorMenu(owner, nil)
	m.SetEnabled(true)
	m.SetRows(func() []widget.MenuItemModel { return layoutChoices(core{h}, id, coreRowOf) })
	if !m.OpenAt(tuicore.Point{X: 1, Y: 1}) {
		return errors.New("layout: the menu has nowhere to open")
	}
	return nil
}
