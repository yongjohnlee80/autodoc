package feature

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
const MenuBudget = 200 * time.Millisecond

// rowSource is which reload drops a row.
type rowSource uint8

const (
	FromLua      rowSource = iota // autodoc.menu's: a :lua-reload drops it
	FromManifest                  // a plugin manifest's [[menu]]: the plugin's manifest reload drops it
)

// addedRow is one row of the editor's right-click menu or the Plugins menu.
type AddedRow struct {
	Owner, ID string    // the key: the script that added it ("" for the user's own) or "plugin:<name>"; its id
	Source    rowSource // which reload drops it
	Where     string    // "editor" or "plugins"
	Serial    int       // its place: its item id, and its order across a replace
	Menu      string    // the submenu it shows under; "" for the menu itself
	Files     []string  // globs over the open file's base name; none matches every file
	When      *lua.LFunction
	Title     string // as written: & marks its key
	TitleFn   *lua.LFunction
	Pushed    *string // a running plugin's latest title (host.menu); nil for none
	Disabled  bool    // a running plugin's latest enabled = false
	Run       func()  // its action: its owner's grant checked, then its line or its plugin command
}

// menuRows is the registry, its rows in serial order.
type MenuRows struct {
	Rows   []*AddedRow
	next   int
	groups map[string]int // a submenu's exact name → its own number, given on first sight
}

// add registers r. One with the owner, source and id of a row already there replaces it in its
// place, its serial kept; a row with no id is always new. It returns r.
func (m *MenuRows) Add(r *AddedRow) *AddedRow {
	if r.ID != "" {
		for i, old := range m.Rows {
			if old.Owner == r.Owner && old.Source == r.Source && old.ID == r.ID {
				r.Serial = old.Serial
				m.Rows[i] = r
				return r
			}
		}
	}
	m.next++
	r.Serial = m.next
	m.Rows = append(m.Rows, r)
	return r
}

// remove drops r, when it is still registered: a row a replace put in its place is another. The
// slot it leaves is cleared, so nothing keeps its functions.
func (m *MenuRows) Remove(r *AddedRow) bool {
	if i := slices.Index(m.Rows, r); i >= 0 {
		m.Rows = slices.Delete(m.Rows, i, i+1)
		return true
	}
	return false
}

// drop removes every row match reports.
func (m *MenuRows) Drop(match func(*AddedRow) bool) {
	kept := m.Rows[:0]
	for _, r := range m.Rows {
		if !match(r) {
			kept = append(kept, r)
		}
	}
	clear(m.Rows[len(kept):])
	m.Rows = kept
}

// find is owner's row id from source, nil for none.
func (m *MenuRows) Find(owner string, source rowSource, id string) *AddedRow {
	for _, r := range m.Rows {
		if r.Owner == owner && r.Source == source && r.ID == id {
			return r
		}
	}
	return nil
}

// bySerial is the row with serial n.
func (m *MenuRows) BySerial(n int) *AddedRow {
	for _, r := range m.Rows {
		if r.Serial == n {
			return r
		}
	}
	return nil
}

// group is a submenu's number: its item id, the same for its name every open.
func (m *MenuRows) Group(name string) int {
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
type MenuCtx struct {
	Path, Workspace, Kind string
	Selection             bool
}

// matches reports whether one of globs matches name; no globs match every name.
func Matches(globs []string, name string) bool {
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
func (r *AddedRow) Name() string { return cmp.Or(r.ID, "menu row "+strconv.Itoa(r.Serial)) }
