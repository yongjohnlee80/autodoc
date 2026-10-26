package tui

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// menuAt is the right-click menu's added rows for ctx, as one open builds them.
func (r *running) menuAt(ctx menuCtx) []widget.MenuItemModel {
	return onLoop(r, func() []widget.MenuItemModel {
		s := r.h.luaFeature()
		return s.rows.editorItems(s, ctx)
	})
}

// labelsOf is items' labels, a submenu's rows as "Sub>Row", separators left out.
func labelsOf(items []widget.MenuItemModel) []string {
	var out []string
	for _, it := range items {
		switch {
		case it.Kind == widget.ItemKindSeparator:
		case len(it.Children) > 0:
			for _, ch := range it.Children {
				out = append(out, it.Label+">"+ch.Label)
			}
		default:
			out = append(out, it.Label)
		}
	}
	return out
}

func same(a, b []string) bool { return strings.Join(a, "|") == strings.Join(b, "|") }

// startMenu is a running TUI with src loaded.
func startMenu(t *testing.T, src string) *running {
	t.Helper()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	r.luaLoad(t, `autodoc.command{ name = "mark", run = function(ed, args, line) ed:insert("<" .. line .. ">") end }
`+src)
	return r
}

// A row shows only for a file its globs match (the base name), and only when its when answers true;
// a folder's scope is when on ctx.path. Both are asked at every open.
func TestAMenuRowShowsForItsFilesAndWhen(t *testing.T) {
	r := startMenu(t, `whens = 0
autodoc.menu{ where = "editor", files = {"*.sql"}, title = "&Run query", command = "mark q" }
autodoc.menu{ where = "editor", when = function(ctx) whens = whens + 1; return ctx.selection end, title = "Selected", command = "mark s" }
autodoc.menu{ where = "editor", when = function(ctx) return ctx.path:find("^notes/") ~= nil end, title = "Notes", command = "mark n" }`)
	for _, c := range []struct {
		ctx  menuCtx
		want []string
	}{
		{menuCtx{path: "db/q.sql"}, []string{"Run query"}},
		{menuCtx{path: "a.md", selection: true}, []string{"Selected"}},
		{menuCtx{path: "notes/x.md"}, []string{"Notes"}},
		{menuCtx{path: "q.sql.md"}, nil},
	} {
		if got := labelsOf(r.menuAt(c.ctx)); !same(got, c.want) {
			t.Errorf("%+v: %q; want %q", c.ctx, got, c.want)
		}
	}
	if n := onLoop(r, func() float64 { return lua2num(r, "whens") }); n != 4 {
		t.Errorf("when was asked %v times over four opens; want 4", n)
	}
	// through the menu itself, on the file open: a.md matches *.md, not *.sql
	r.luaLoad(t, `autodoc.menu{ where = "editor", files = {"*.md"}, title = "Markdown", command = "mark m" }`)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	var labels []string
	onLoop(r, func() bool { labels = labelsOf(r.h.editorMenu(r.h.core)); return true })
	if !strings.Contains(strings.Join(labels, "|"), "Markdown") || strings.Contains(strings.Join(labels, "|"), "Run query") {
		t.Errorf("the open a.md's menu: %q; want Markdown and no Run query", labels)
	}
}

// lua2num is the Lua global name as a number.
func lua2num(r *running, name string) float64 {
	return float64(lua.LVAsNumber(r.h.scripts.L.GetGlobal(name)))
}

// Rows with one menu share one submenu, wherever its first row is, across scripts: a package's row
// joins the user's. Submenus are told apart by their exact names, never a slug of them: "A-B" and
// "A B", two non-Latin names and two long names with one prefix are distinct, and every item id
// in the menu is unique.
func TestMenuRowsGroupUnderTheirExactSubmenu(t *testing.T) {
	long := strings.Repeat("Long submenu name ", 4)
	r := startMenu(t, `autodoc.menu{ where = "editor", menu = "Jupyter", title = "Run", command = "mark a" }
autodoc.menu{ where = "editor", title = "Loose", command = "mark b" }
autodoc.menu{ where = "editor", menu = "A-B", title = "Dash", command = "mark c" }
autodoc.menu{ where = "editor", menu = "A B", title = "Space", command = "mark d" }
autodoc.menu{ where = "editor", menu = "日本", title = "Ja", command = "mark e" }
autodoc.menu{ where = "editor", menu = "中文", title = "Zh", command = "mark f" }
autodoc.menu{ where = "editor", menu = "`+long+`one", title = "L1", command = "mark g" }
autodoc.menu{ where = "editor", menu = "`+long+`two", title = "L2", command = "mark h" }`)
	if err := onLoop(r, func() error {
		return r.h.loadGated("pkg", "pkg.lua", []byte(`autodoc.menu{ where = "editor", menu = "Jupyter", title = "Clear", command = "mark z" }`))
	}); err != nil {
		t.Fatal(err)
	}
	items := r.menuAt(menuCtx{path: "a.md"})
	want := []string{"Jupyter>Run", "Jupyter>Clear", "Loose", "A-B>Dash", "A B>Space", "日本>Ja", "中文>Zh", long + "one>L1", long + "two>L2"}
	if got := labelsOf(items); !same(got, want) {
		t.Fatalf("the rows: %q; want %q", got, want)
	}
	ids := map[widget.ItemID]bool{}
	var walk func([]widget.MenuItemModel)
	walk = func(items []widget.MenuItemModel) {
		for _, it := range items {
			if ids[it.ID] {
				t.Errorf("item id %s twice", it.ID)
			}
			ids[it.ID] = true
			walk(it.Children)
		}
	}
	walk(items)
	var ok bool
	onLoop(r, func() bool { ok = len(r.h.editorMenu(r.h.core)) > 0; return true })
	if !ok {
		t.Fatal("the menu has no rows")
	}
}

// A title function is answered at each open, its & the row's key. A script's title is its own: a
// row titled as one of AutoDoc's own ("&Back") is never swapped for a catalog translation.
func TestAMenuRowsTitleIsAnsweredAtEachOpenAndKeptAsWritten(t *testing.T) {
	r := startMenu(t, `n = 0
autodoc.menu{ where = "editor", title = function(ctx) n = n + 1; return "&Run " .. n end, command = "mark r" }
autodoc.menu{ where = "editor", title = "&Back", command = "mark b" }`)
	for i, want := range []string{"Run 1", "Run 2"} {
		items := r.menuAt(menuCtx{path: "a.md"})
		if got := labelsOf(items); !same(got, []string{want, "Back"}) {
			t.Fatalf("open %d: %q", i+1, got)
		}
		if items[1].Hotkey != 'r' || items[2].Hotkey != 'b' {
			t.Errorf("open %d: keys %q %q; want r and b", i+1, items[1].Hotkey, items[2].Hotkey)
		}
		if items[2].LabelMsg.ID != "" {
			t.Errorf("a script's &Back took the catalog's message %q", items[2].LabelMsg.ID)
		}
	}
}

// The same id from the same script replaces its row, in its place. The old row's handle then
// removes nothing; the replacement's removes it, once. Rows with no id are each new.
func TestAMenuRowIsReplacedByItsIdAndRemovedByItsOwnHandle(t *testing.T) {
	r := startMenu(t, `autodoc.menu{ where = "editor", title = "Before", command = "mark a" }
one = autodoc.menu{ where = "editor", id = "x", title = "One", command = "mark b" }
autodoc.menu{ where = "editor", title = "After", command = "mark c" }
two = autodoc.menu{ where = "editor", id = "x", title = "Two", command = "mark d" }`)
	at := menuCtx{path: "a.md"}
	if got := labelsOf(r.menuAt(at)); !same(got, []string{"Before", "Two", "After"}) {
		t.Fatalf("after the replace: %q", got)
	}
	r.luaLoad(t, `one:remove()`)
	if got := labelsOf(r.menuAt(at)); !same(got, []string{"Before", "Two", "After"}) {
		t.Fatalf("the old handle removed the replacement: %q", got)
	}
	r.luaLoad(t, `two:remove(); two:remove()`)
	if got := labelsOf(r.menuAt(at)); !same(got, []string{"Before", "After"}) {
		t.Fatalf("after the replacement's remove: %q", got)
	}
	r.luaLoad(t, `autodoc.menu{ where = "editor", title = "Twin", command = "mark e" }
autodoc.menu{ where = "editor", title = "Twin", command = "mark e" }`)
	if got := labelsOf(r.menuAt(at)); !same(got, []string{"Before", "After", "Twin", "Twin"}) {
		t.Fatalf("two rows with no id: %q", got)
	}
	r.mustRun(t, "lua-reload")
	if got := labelsOf(r.menuAt(at)); len(got) != 0 {
		t.Fatalf("after :lua-reload: %q", got)
	}
}

// A when or a title that fails, or a title that isn't a string, is a notice, and the menu opens
// with the other rows. A spec the menu can't take is the script's error.
func TestAMenuRowsErrorsAreNoticedAndTheMenuStillOpens(t *testing.T) {
	r := startMenu(t, `autodoc.menu{ where = "editor", when = function() error("no when") end, title = "W", command = "mark w" }
autodoc.menu{ where = "editor", title = function() return 7 end, command = "mark t" }
autodoc.menu{ where = "editor", title = "Fine", command = "mark f" }`)
	r.clearNotices()
	if got := labelsOf(r.menuAt(menuCtx{path: "a.md"})); !same(got, []string{"Fine"}) {
		t.Fatalf("the rows: %q", got)
	}
	r.waitNotice(t, "no when") // the open's first failure: one notice an open
	r.mustRun(t, "lua-reload")
	r.luaLoad(t, `autodoc.menu{ where = "editor", title = function() return 7 end, command = "mark t" }
autodoc.menu{ where = "editor", title = "Fine", command = "mark f" }`)
	r.clearNotices()
	if got := labelsOf(r.menuAt(menuCtx{path: "a.md"})); !same(got, []string{"Fine"}) {
		t.Fatalf("the rows with a number for a title: %q", got)
	}
	r.waitNotice(t, "answered a number, not a string")
	for src, want := range map[string]string{
		`autodoc.menu{ where = "editor", title = "x", command = "mark", colour = "red" }`:        "menu has no field colour",
		`autodoc.menu{ where = "plugins", title = "x", command = "mark", files = {"*.sql"} }`:    "takes no files, when or title function",
		`autodoc.menu{ where = "plugins", title = function() return "x" end, command = "mark" }`: "takes no files, when or title function",
		`autodoc.menu{ where = "editor", title = "x", command = "mark", files = {"["} }`:         "isn't one",
		`autodoc.menu{ where = "editor", title = "x", command = "mark", when = "yes" }`:          "when is a function",
	} {
		if err := onLoop(r, func() error { return r.h.scripts.Load("bad.lua", []byte(src)) }); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v; want %q", src, err, want)
		}
	}
}

// One budget bounds every open: rows whose when spins leave the menu opening in about menuBudget
// however many there are, those rows out and one notice said. So do rows whose when runs a
// command that spins (autodoc.run): the command inherits the budget's deadline, not the engine's
// own two seconds.
func TestTheMenuBudgetBoundsSlowRowsAndCommandsTheyRun(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"spinning whens", `for i = 1, 8 do autodoc.menu{ where = "editor", when = function() while true do end end, title = "S" .. i, command = "mark s" } end`},
		{"whens that run a spinning command", `autodoc.command{ name = "spin", run = function() while true do end end }
for i = 1, 8 do autodoc.menu{ where = "editor", when = function() autodoc.run("lua.spin"); return true end, title = "N" .. i, command = "mark n" } end`},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := startMenu(t, c.src+`
autodoc.menu{ where = "editor", title = "Fine", command = "mark f" }`)
			r.clearNotices()
			start := time.Now()
			got := labelsOf(r.menuAt(menuCtx{path: "a.md"}))
			took := time.Since(start)
			if !same(got, []string{"Fine"}) {
				t.Fatalf("the rows: %q; want the fine one alone", got)
			}
			// a fixed bound, not one made of menuBudget: the documented 200 ms with room for -race
			if took > time.Second {
				t.Fatalf("eight slow rows held the open %v; the documented budget is 200ms", took)
			}
			if n := onLoop(r, func() int { return len(r.h.noticeLog.items) }); n != 1 {
				t.Fatalf("%d notices for one open; want one", n)
			}
		})
	}
}

// A when runs while the engine counts as running, so asking for a reload from inside one is refused
// and the engine stays; once the open is over the engine isn't running.
func TestAMenuRowsWhenCannotReloadTheScriptsUnderIt(t *testing.T) {
	r := startMenu(t, `said = ""
autodoc.menu{ where = "editor", when = function() local ok, err = autodoc.run("scripts.reload"); said = tostring(err); return true end, title = "R", command = "mark r" }`)
	before := onLoop(r, func() any { return r.h.scripts })
	r.menuAt(menuCtx{path: "a.md"})
	if said := onLoop(r, func() string { return lua.LVAsString(r.h.scripts.L.GetGlobal("said")) }); !strings.Contains(said, "not from inside a script") {
		t.Fatalf("the reload a when asked for answered %q; want it refused", said)
	}
	if after := onLoop(r, func() any { return r.h.scripts }); after != before {
		t.Fatal("a when reloaded the engine it ran in")
	}
	if onLoop(r, func() bool { return r.h.scripts.Running() }) {
		t.Fatal("the engine still counts as running after the open")
	}
}

// A gated script's row needs commands when it runs, as its keys do.
func TestAGatedScriptsRowNeedsCommandsWhenItRuns(t *testing.T) {
	r := startMenu(t, ``)
	if err := onLoop(r, func() error {
		return r.h.loadGated("pkg", "pkg.lua", []byte(`autodoc.menu{ where = "editor", title = "Gated", command = "mark g" }`))
	}); err != nil {
		t.Fatal(err)
	}
	r.page("", 1, 1)
	r.clearNotices()
	items := r.menuAt(menuCtx{path: "a.md"})
	run := func() {
		onLoop(r, func() bool {
			items[1].Action.(widget.CoreMenuAction).Run(r.h.core)
			return true
		})
	}
	run()
	r.waitNotice(t, "pkg: its keys and menu rows need commands: not granted")
	r.wantText(t, "ungranted", "")
	onLoop(r, func() bool { r.h.prefs.luaGrants = map[string]map[string]bool{"pkg": {"commands": true}}; return true })
	run()
	r.wantText(t, "granted", "<g>")
}

// A when may change the rows as the menu opens: removing one, replacing another. The open shows the
// rows as it began, less those taken out or replaced, each once, so the menu's model is valid and it
// opens; the replacement shows from the next open.
func TestAWhenThatChangesTheRowsCannotBreakTheMenu(t *testing.T) {
	r := startMenu(t, `changed = false
autodoc.menu{ where = "editor", title = "A", command = "mark a", when = function()
  if not changed then
    changed = true
    b:remove()
    autodoc.menu{ where = "editor", id = "d", title = "D2", command = "mark d" }
  end
  return true
end }
b = autodoc.menu{ where = "editor", title = "B", command = "mark b" }
autodoc.menu{ where = "editor", title = "C", command = "mark c" }
autodoc.menu{ where = "editor", id = "d", title = "D1", command = "mark d" }`)
	for i, want := range [][]string{{"A", "C"}, {"A", "C", "D2"}} {
		var labels []string
		var err error
		onLoop(r, func() bool {
			items := r.h.editorMenu(r.h.core)
			labels = labelsOf(items[len(items)-len(want)-1:])
			err = widget.NewMenu().SetModel(items)
			return true
		})
		if !same(labels, want) || err != nil {
			t.Fatalf("open %d: %q, the model %v; want %q and a valid model", i+1, labels, err, want)
		}
	}
}

// A row withdrawn while the menu opens doesn't show in that open, whichever when or title withdrew
// it and whenever: its own, or a later row's after it was reached. A row put in another's place
// shows from the next open.
func TestARowWithdrawnAsTheMenuOpensDoesNotShowInIt(t *testing.T) {
	for _, c := range []struct {
		name, src  string
		want, next []string
	}{
		{"by its own when", `local row
row = autodoc.menu{ where = "editor", title = "Self", command = "mark x", when = function() row:remove(); return true end }
autodoc.menu{ where = "editor", title = "Safe", command = "mark s" }`, []string{"Safe"}, []string{"Safe"}},
		{"by its own title", `local row
row = autodoc.menu{ where = "editor", title = function() row:remove(); return "Self" end, command = "mark x" }
autodoc.menu{ where = "editor", title = "Safe", command = "mark s" }`, []string{"Safe"}, []string{"Safe"}},
		{"removed by a later row", `local first = autodoc.menu{ where = "editor", title = "A", command = "mark a" }
autodoc.menu{ where = "editor", title = "B", command = "mark b", when = function() first:remove(); return true end }`,
			[]string{"B"}, []string{"B"}},
		{"replaced by a later row", `autodoc.menu{ where = "editor", id = "a", title = "A1", command = "mark a" }
done = false
autodoc.menu{ where = "editor", title = "B", command = "mark b", when = function()
  if not done then done = true; autodoc.menu{ where = "editor", id = "a", title = "A2", command = "mark a2" } end
  return true
end }`, []string{"B"}, []string{"A2", "B"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := startMenu(t, c.src)
			for i, want := range [][]string{c.want, c.next} {
				if got := labelsOf(r.menuAt(menuCtx{path: "a.md"})); !same(got, want) {
					t.Fatalf("open %d: %q; want %q", i+1, got, want)
				}
			}
		})
	}
}

// A row its own when withdraws, by removing it or putting another in its place, has its title
// function never called in that open: the title may act, and it spends the open's budget (Zen's
// TestZenWithdrawnRowDoesNotEvaluateItsTitle is the removed case).
func TestARowItsWhenWithdrawsNeverRunsItsTitle(t *testing.T) {
	for _, c := range []struct{ name, src string }{
		{"removed", `titled = 0
local row
row = autodoc.menu{ where = "editor", command = "mark x",
  when = function() row:remove(); return true end,
  title = function() titled = titled + 1; return "Self" end }
autodoc.menu{ where = "editor", title = "Safe", command = "mark s" }`},
		{"replaced", `titled = 0
autodoc.menu{ where = "editor", id = "x", command = "mark x",
  when = function() autodoc.menu{ where = "editor", id = "x", title = "Other", command = "mark o" }; return true end,
  title = function() titled = titled + 1; return "Self" end }
autodoc.menu{ where = "editor", title = "Safe", command = "mark s" }`},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := startMenu(t, c.src)
			got := labelsOf(r.menuAt(menuCtx{path: "a.md"}))
			n := onLoop(r, func() lua.LValue { return r.h.luaFeature().eng.L.GetGlobal("titled") })
			if n != lua.LNumber(0) || slices.Contains(got, "Self") {
				t.Fatalf("its title ran %v times; the menu %q", n, got)
			}
		})
	}
}

// A removed row leaves no slot behind it in the registry's backing array, so nothing keeps its
// functions alive.
func TestARemovedRowLeavesNoSlotBehind(t *testing.T) {
	var m menuRows
	m.add(&addedRow{id: "a"})
	b := m.add(&addedRow{id: "b"})
	m.add(&addedRow{id: "c"})
	if !m.remove(b) || len(m.rows) != 2 || m.rows[:3][2] != nil {
		t.Fatalf("after removing b: %d rows, the vacated slot %v", len(m.rows), m.rows[:3][2])
	}
}
