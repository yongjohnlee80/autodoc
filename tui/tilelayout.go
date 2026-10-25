package tui

import (
	"math"
	"slices"
	"strconv"

	"github.com/yongjohnlee80/golib/parse/qml"
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
