//go:build linux || darwin

package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// widgets_test.go: :go, the Go-to picker and F6 (ADR 1792256102 §5).

func (r *running) focusedOn(id string) bool { return onLoop(r, func() bool { return r.h.within(id) }) }

func (r *running) panelIsOpen(id string) bool {
	return onLoop(r, func() bool { return r.h.panelOpen[id] })
}

func (r *running) widgetNames() []string {
	return onLoop(r, func() []string {
		var out []string
		for _, w := range r.h.widgets() {
			out = append(out, w.name)
		}
		return out
	})
}

func f6(shift bool) tuicore.Event {
	ev := tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF6}
	if shift {
		ev.Mods = tuicore.ModShift
	}
	return ev
}

// :go explorer opens a closed explorer with the keyboard, and keeps an open one open: going is
// never a toggle.
func TestGoOpensAndNeverCloses(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	if err := r.luaRun("go explorer"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the explorer open, with the keyboard", func(string) bool {
		return r.panelIsOpen("explorer") && r.focusedOn("explorerTree")
	})
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the editor's keyboard", func(string) bool { return r.focusedOn("editor") })
	if err := r.luaRun("go explorer"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the explorer's keyboard again", func(string) bool { return r.focusedOn("explorerTree") })
	if !r.panelIsOpen("explorer") {
		t.Fatal(":go closed the open explorer")
	}
	if err := r.luaRun("go editor"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "back to the editor", func(string) bool { return r.focusedOn("editor") })
	if !r.panelIsOpen("explorer") {
		t.Fatal(":go editor closed the explorer")
	}
}

// A name is matched exactly, else as the one target holding it; more than one is refused, and so is
// none. The list never holds the menu bar, a dialog or the status line, the menu bar shown or not.
func TestGoNamesAndTheList(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	if err := r.luaRun("go relat"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the relations", func(string) bool { return r.panelIsOpen("links") && r.focusedOn("relationsList") })
	if err := r.luaRun("go e"); err == nil || !strings.Contains(err.Error(), "widgets match") {
		t.Errorf(":go e: %v, want several widgets matching", err)
	}
	if err := r.luaRun("go nowhere"); err == nil || !strings.Contains(err.Error(), `no widget "nowhere"`) {
		t.Errorf(":go nowhere: %v", err)
	}
	onLoop(r, func() bool { r.h.leaderMenuBar(); return true }) // SPC m: the menu bar shown, and focused
	names := r.widgetNames()
	want := []string{"editor", "explorer", "relations", "terminal", "agent", "preview"}
	for i, n := range want {
		if i >= len(names) || names[i] != n {
			t.Fatalf("the targets begin %v, want %v", names, want)
		}
	}
	for _, n := range names {
		if strings.Contains(strings.ToLower(n), "menu") || strings.Contains(n, "status") {
			t.Errorf("a target %q is not a widget", n)
		}
	}
	if !strings.Contains(strings.Join(names, " "), "card") {
		t.Errorf("the Lua card's panel is not a target: %v", names)
	}
}

// F6 steps the keyboard through the open targets, skipping the closed ones and the menu; Shift+F6
// goes back.
func TestF6CyclesTheOpenWidgets(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool {
		r.h.keep(r.h.p.Call("explorer", "open"))
		r.h.keep(r.h.p.Call("links", "open"))
		return true
	})
	r.s.WaitFor(t, "both panels open", func(string) bool { return r.panelIsOpen("explorer") && r.panelIsOpen("links") })
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the editor focused", func(string) bool { return r.focusedOn("editor") })
	for _, want := range []string{"explorerTree", "relationsList", "editor", "explorerTree"} {
		r.keys(t, f6(false))
		r.s.WaitFor(t, "F6 to "+want, func(string) bool { return r.focusedOn(want) })
	}
	for _, want := range []string{"editor", "relationsList"} {
		r.keys(t, f6(true))
		r.s.WaitFor(t, "Shift+F6 to "+want, func(string) bool { return r.focusedOn(want) })
	}
	onLoop(r, func() bool { r.h.closeDrawer("links"); r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the relations closed", func(string) bool { return !r.panelIsOpen("links") && r.focusedOn("editor") })
	r.keys(t, f6(false))
	r.s.WaitFor(t, "F6 to the explorer", func(string) bool { return r.focusedOn("explorerTree") })
	r.keys(t, f6(false))
	r.s.WaitFor(t, "F6 past the closed relations", func(string) bool { return r.focusedOn("editor") })
	if r.panelIsOpen("links") {
		t.Fatal("F6 opened a closed panel")
	}
}

// SPC Tab opens the Go-to picker, its cursor on the focused target; Tab and Shift+Tab move it,
// wrapping; Enter goes.
func TestTheGoToPicker(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.keys(t, key(' '))
	r.s.WaitForText(t, "SPC — commands")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab})
	r.s.WaitForText(t, "go to")
	at := func() int { return onLoop(r, func() int { return r.h.widgetAt }) }
	if at() != 0 {
		t.Fatalf("the cursor on row %d, want the editor's (0)", at())
	}
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab})
	r.s.WaitFor(t, "Tab moved the cursor", func(string) bool { return at() == 1 })
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab, Mods: tuicore.ModShift})
	r.s.WaitFor(t, "Shift+Tab back", func(string) bool { return at() == 0 })
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab, Mods: tuicore.ModShift})
	n := onLoop(r, func() int { return r.h.widgetRows.Len() })
	r.s.WaitFor(t, "Shift+Tab wraps to the last row", func(string) bool { return at() == n-1 })
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab}, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab})
	r.s.WaitFor(t, "Tab wraps to the explorer's row", func(string) bool { return at() == 1 })
	r.keys(t, enter())
	r.s.WaitFor(t, "Enter went to the explorer", func(string) bool {
		return r.panelIsOpen("explorer") && r.focusedOn("explorerTree")
	})
	if err := r.luaRun("go"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "go to")
	r.s.WaitFor(t, "the cursor on the explorer, focused", func(string) bool { return at() == 1 })
}

// A card plugin's panel is a target by its plugin's name: :go opens it with its plugin, and so does
// autodoc.run("widget.go", name), which Lua may run though it may not run the plugin's commands. A
// plugin named like a built-in target keeps its panel's id as its name.
func TestGoReachesACardPlugin(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "notes", "dialog", "echo", rightCard)
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	_, r, _ := cardTUI(t, dir, nil)
	names := strings.Join(r.widgetNames(), " ")
	if !strings.Contains(names, " notes") || !strings.Contains(names, "card_card") {
		t.Fatalf("the card plugins' names: %s", names)
	}
	if err := r.luaRun("go notes"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the notes card, with the keyboard", func(string) bool {
		return r.panelShown("notes") && r.focusedOn(cardViewID("notes"))
	})
	onLoop(r, func() bool { r.h.closeDrawer(cardPanelID("notes")); r.h.focusEditor(); return true })
	r.s.WaitFor(t, "the card closed", func(string) bool { return !r.panelShown("notes") })
	r.luaLoad(t, `autodoc.command{ name = "gonotes", run = function()
  local ok, err = autodoc.run("widget.go", "notes")
  autodoc.notify("went " .. tostring(ok) .. " " .. tostring(err))
end }`)
	if err := r.luaRun("gonotes"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "Lua went to the card", func(string) bool {
		return r.noticed("went true nil") && r.panelShown("notes") && r.focusedOn(cardViewID("notes"))
	})
	if err := r.luaRun("go card_card"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the plugin named card", func(string) bool { return r.panelShown("card") })
}

// autodoc.widgets lists the targets for Lua, the focused one marked.
func TestLuaListsTheWidgets(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.focusEditor(); return true })
	r.luaLoad(t, `autodoc.command{ name = "ws", run = function()
  local out = {}
  for _, w in ipairs(autodoc.widgets()) do out[#out + 1] = w.name .. (w.focused and "*" or "") .. (w.open and "+" or "") end
  autodoc.notify("widgets " .. table.concat(out, ","))
end }`)
	if err := r.luaRun("ws"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "the list", func(string) bool { return r.noticed("widgets editor*+,explorer,relations,terminal,agent,preview") })
}
