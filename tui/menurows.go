package tui

import (
	"cmp"
	"path"
	"slices"
	"strconv"
	"time"

	lua "github.com/yuin/gopher-lua"
)

// MENU ROWS (docs/decisions/plugins.md#editor-menu-rows): one registry of the rows scripts
// (autodoc.menu) and plugins' manifests ([[menu]]) add to the editor's right-click menu and the
// Plugins menu. The editor's are built as that menu opens: each row scoped by its files and its
// when, titled by its title (a string, a Lua function, or a plugin's pushed title), grouped under
// its menu's submenu. Every when and title of one open shares one budget (menuBudget), so slow rows
// can't hold the menu.

// menuBudget is all the time one menu open gives its rows' when and title functions together.
const menuBudget = 200 * time.Millisecond

// rowSource is which reload drops a row.
type rowSource uint8

const (
	fromLua      rowSource = iota // autodoc.menu's: a :lua-reload drops it
	fromManifest                  // a plugin manifest's [[menu]]: the plugin's manifest reload drops it
)

// addedRow is one row of the editor's right-click menu or the Plugins menu.
type addedRow struct {
	owner, id string    // the key: the script that added it ("" for the user's own) or "plugin:<name>"; its id
	source    rowSource // which reload drops it
	where     string    // "editor" or "plugins"
	serial    int       // its place: its item id, and its order across a replace
	menu      string    // the submenu it shows under; "" for the menu itself
	files     []string  // globs over the open file's base name; none matches every file
	when      *lua.LFunction
	title     string // as written: & marks its key
	titleFn   *lua.LFunction
	pushed    *string // a running plugin's latest title (host.menu); nil for none
	disabled  bool    // a running plugin's latest enabled = false
	run       func()  // its action: its owner's grant checked, then its line or its plugin command
}

// menuRows is the registry, its rows in serial order.
type menuRows struct {
	rows   []*addedRow
	next   int
	groups map[string]int // a submenu's exact name → its own number, given on first sight
}

// add registers r. One with the owner, source and id of a row already there replaces it in its
// place, its serial kept; a row with no id is always new. It returns r.
func (m *menuRows) add(r *addedRow) *addedRow {
	if r.id != "" {
		for i, old := range m.rows {
			if old.owner == r.owner && old.source == r.source && old.id == r.id {
				r.serial = old.serial
				m.rows[i] = r
				return r
			}
		}
	}
	m.next++
	r.serial = m.next
	m.rows = append(m.rows, r)
	return r
}

// remove drops r, when it is still registered: a row a replace put in its place is another. The
// slot it leaves is cleared, so nothing keeps its functions.
func (m *menuRows) remove(r *addedRow) bool {
	if i := slices.Index(m.rows, r); i >= 0 {
		m.rows = slices.Delete(m.rows, i, i+1)
		return true
	}
	return false
}

// drop removes every row match reports.
func (m *menuRows) drop(match func(*addedRow) bool) {
	kept := m.rows[:0]
	for _, r := range m.rows {
		if !match(r) {
			kept = append(kept, r)
		}
	}
	clear(m.rows[len(kept):])
	m.rows = kept
}

// find is owner's row id from source, nil for none.
func (m *menuRows) find(owner string, source rowSource, id string) *addedRow {
	for _, r := range m.rows {
		if r.owner == owner && r.source == source && r.id == id {
			return r
		}
	}
	return nil
}

// bySerial is the row with serial n.
func (m *menuRows) bySerial(n int) *addedRow {
	for _, r := range m.rows {
		if r.serial == n {
			return r
		}
	}
	return nil
}

// group is a submenu's number: its item id, the same for its name every open.
func (m *menuRows) group(name string) int {
	if m.groups == nil {
		m.groups = map[string]int{}
	}
	n, ok := m.groups[name]
	if !ok {
		n = len(m.groups) + 1
		m.groups[name] = n
	}
	return n
}

// menuCtx is what a row's when and title see as the menu opens: the open file, its workspace and
// kind, and whether text is selected.
type menuCtx struct {
	path, workspace, kind string
	selection             bool
}

// matches reports whether one of globs matches name; no globs match every name.
func matches(globs []string, name string) bool {
	if len(globs) == 0 {
		return true
	}
	for _, g := range globs {
		if ok, _ := path.Match(g, name); ok {
			return true
		}
	}
	return false
}

// name is a row as its errors name it.
func (r *addedRow) name() string { return cmp.Or(r.id, "menu row "+strconv.Itoa(r.serial)) }
