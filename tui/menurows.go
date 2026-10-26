package tui

import (
	"cmp"
	"fmt"
	"path"
	"slices"
	"strconv"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// MENU ROWS (ADR 1792991184): one registry of the rows scripts (autodoc.menu) and plugins'
// manifests ([[menu]]) add to the editor's right-click menu and the Plugins menu. The editor's are
// built as that menu opens: each row scoped by its files and its when, titled by its title (a
// string, a Lua function, or a plugin's pushed title), grouped under its menu's submenu. Every
// when and title of one open shares one budget (menuBudget), so slow rows can't hold the menu.

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

// rowsShown is a menu's rows as it opens: each row that shows, with its title, in serial order.
// An error, or the budget running out, leaves that row out; said is the open's one notice, "" for
// none.
func (m *menuRows) rowsShown(s *scripting, where string, ctx menuCtx) (shown []shownRow, said string) {
	end := time.Now().Add(menuBudget)
	var first error
	skipped := 0
	// the rows as the open began: a when or a title may add, remove or replace rows as it runs, and
	// a row one took out (or put another in place of) doesn't show this time, nor run its own
	for _, r := range slices.Clone(m.rows) {
		if !slices.Contains(m.rows, r) || r.where != where || !matches(r.files, path.Base(ctx.path)) {
			continue
		}
		if (r.when != nil || (r.titleFn != nil && r.pushed == nil)) && !time.Now().Before(end) {
			skipped++
			continue
		}
		if r.when != nil {
			v, err := s.rowAnswer(end, r, "when", r.when, ctx)
			if err != nil {
				first = cmp.Or(first, err)
				continue
			}
			if !lua.LVAsBool(v) {
				continue
			}
			if !slices.Contains(m.rows, r) {
				continue // its own when took it out, or put another in its place: its title never runs
			}
		}
		title := r.title
		switch {
		case r.pushed != nil:
			title = *r.pushed
		case r.titleFn != nil:
			v, err := s.rowAnswer(end, r, "title", r.titleFn, ctx)
			if err == nil {
				if t, ok := v.(lua.LString); ok && t != "" {
					title = string(t)
				} else {
					err = fmt.Errorf("%s's title: answered a %s, not a string", r.name(), v.Type())
				}
			}
			if err != nil {
				first = cmp.Or(first, err)
				continue
			}
		}
		shown = append(shown, shownRow{r, title})
	}
	// a row withdrawn by its own when or title, or by a later row's, after it was reached
	shown = slices.DeleteFunc(shown, func(x shownRow) bool { return !slices.Contains(m.rows, x.r) })
	switch {
	case first != nil:
		said = first.Error()
	case skipped > 0:
		said = s.c.Trf("autodoc.notice.menurows.menu_rows_left_out_their_scripts_took_longer_tha", skipped, menuBudget)
	}
	return shown, said
}

// shownRow is a row as one open shows it.
type shownRow struct {
	r     *addedRow
	title string
}

// name is a row as its errors name it.
func (r *addedRow) name() string { return cmp.Or(r.id, "menu row "+strconv.Itoa(r.serial)) }

// editorItems are the right-click menu's rows for ctx, after a separator; none without one. A
// script's words are its own: a row's label is only its & parsed, never a catalog translation.
func (m *menuRows) editorItems(s *scripting, ctx menuCtx) []widget.MenuItemModel {
	shown, said := m.rowsShown(s, "editor", ctx)
	if said != "" {
		s.c.Notify(said)
	}
	if len(shown) == 0 {
		return nil
	}
	items := []widget.MenuItemModel{widget.NewSeparator("menu.sep")}
	at := map[string]int{} // a submenu's place in items
	for _, x := range shown {
		r := x.r
		label, key, idx := tuicore.ParseMnemonic(x.title)
		item := widget.NewCommand(widget.ItemID("menu.row."+strconv.Itoa(r.serial)), label,
			widget.CoreMenuAction{ID: tuicore.ActionID("menu.row." + strconv.Itoa(r.serial)), Run: func(*widget.EditorCore) { r.run() }})
		item.Hotkey, item.HotkeyIdx, item.Enabled = key, idx, !r.disabled
		if r.menu == "" {
			items = append(items, item)
			continue
		}
		i, ok := at[r.menu]
		if !ok {
			sub := widget.NewSubmenu(widget.ItemID("menu.group."+strconv.Itoa(m.group(r.menu))), r.menu, nil)
			sub.Label, sub.Hotkey, sub.HotkeyIdx = tuicore.ParseMnemonic(r.menu)
			items = append(items, sub)
			i = len(items) - 1
			at[r.menu] = i
		}
		items[i].Children = append(items[i].Children, item)
	}
	return items
}

// pluginsMenuRows are the Plugins menu's rows of scripts, as App.luaMenus lists them: each row,
// or a submenu of them, in serial order.
func (m *menuRows) pluginsMenuRows(s *scripting) []tuidecl.Row {
	shown, _ := m.rowsShown(s, "plugins", menuCtx{})
	var rows []tuidecl.Row
	subs := map[string]*tuidecl.ListModel{}
	var subRows = map[string][]tuidecl.Row{}
	for _, x := range shown {
		row := tuidecl.Row{"kind": "item", "label": x.title, "target": strconv.Itoa(x.r.serial), "enabled": !x.r.disabled}
		if x.r.menu == "" {
			rows = append(rows, row)
			continue
		}
		if _, ok := subs[x.r.menu]; !ok {
			subs[x.r.menu] = tuidecl.NewListModel("label", "target", "enabled")
			rows = append(rows, tuidecl.Row{"kind": "submenu", "label": x.r.menu, "rows": subs[x.r.menu], "target": "", "enabled": true})
		}
		subRows[x.r.menu] = append(subRows[x.r.menu], row)
	}
	for name, l := range subs {
		l.Reset(subRows[name])
	}
	return rows
}

// rowAnswer is a row's when or title, answered by the engine before end.
func (s *scripting) rowAnswer(end time.Time, r *addedRow, part string, fn *lua.LFunction, ctx menuCtx) (lua.LValue, error) {
	if s.eng == nil {
		return lua.LNil, fmt.Errorf("%s's %s: no scripts are running", r.name(), part)
	}
	L := s.eng.L
	t := L.CreateTable(0, 4)
	L.SetField(t, "path", lua.LString(ctx.path))
	L.SetField(t, "workspace", lua.LString(ctx.workspace))
	L.SetField(t, "kind", lua.LString(ctx.kind))
	L.SetField(t, "selection", lua.LBool(ctx.selection))
	return s.eng.Answer(end, r.name()+"'s "+part, fn, t)
}

// menuCtxOf is the open file's context for the right-click menu opening on c.
func (h *Host) menuCtxOf(c *widget.EditorCore) menuCtx {
	var ctx menuCtx
	if h.file != nil {
		ctx.path, ctx.workspace = h.file.path, h.file.ws
	}
	if ctx.path != "" {
		ctx.kind = h.resolver().Of(ctx.path).String()
	}
	_, ctx.selection = c.Selection()
	return ctx
}
