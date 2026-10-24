package tui

import (
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"golang.org/x/text/width"
)

// dockKeys are the dock's rows as listed: each widget's :go name.
func (r *running) dockKeys() []string {
	return onLoop(r, func() []string {
		var out []string
		for _, t := range r.h.dockRows.All() {
			out = append(out, t.name)
		}
		return out
	})
}

func (r *running) waitDock(t *testing.T, what string, want ...string) {
	t.Helper()
	r.s.WaitFor(t, what, func(string) bool { return slices.Equal(r.dockKeys(), want) })
}

// menuAutoHides is App.menuAutoHide as published.
func (r *running) menuAutoHides() bool {
	return onLoop(r, func() bool { return r.h.menuAutoHide(r.h.prefs) })
}

// The terminal is in the dock while it is open, and while its shell runs with the drawer closed,
// once however many tabs it holds; it leaves when its shell exits. While it is listed the
// auto-hiding bar stays shown, the preference changed or not; the editor alone, it hides again. A
// click on the hidden terminal's icon opens it, the keyboard in its shell; the editor's gives the
// keyboard back to the page.
func TestTheDockListsTheTerminalWhileItsShellRuns(t *testing.T) {
	_, r := runShellTUI(t, nil)
	r.waitDock(t, "the editor alone", "editor")
	if !r.menuAutoHides() {
		t.Fatal("with the editor alone the bar does not auto-hide")
	}

	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "a shell running", func(string) bool { return r.shellsRunning() == 1 })
	r.waitDock(t, "the terminal open", "editor", "terminal")
	if r.menuAutoHides() {
		t.Fatal("the bar auto-hides while the dock lists the terminal")
	}
	onLoop(r, func() bool { r.h.toggleMenuBar(); r.h.toggleMenuBar(); return true }) // the preference changed, and back
	if r.menuAutoHides() {
		t.Fatal("the preference changed while the dock is busy hid the bar")
	}

	onLoop(r, func() bool { r.h.terminals.toggleTerminal(); return true }) // hidden, its shell running on
	r.s.WaitFor(t, "the drawer closed", func(string) bool { return !onLoop(r, func() bool { return r.h.panelOpen["terminal"] }) })
	r.waitDock(t, "the hidden terminal kept", "editor", "terminal")

	onLoop(r, func() bool { r.h.goDock("terminal"); return true })
	r.s.WaitFor(t, "the terminal open again, the keyboard in it", func(string) bool {
		return onLoop(r, func() bool { return r.h.panelOpen["terminal"] && r.h.within("terminalView") })
	})
	r.s.WaitForText(t, "$ ")
	onLoop(r, func() bool { r.h.goDock("editor"); return true }) // the editor's icon: the keyboard back in the page
	r.s.WaitFor(t, "the keyboard in the editor", func(string) bool { return r.focused("editor") })
	onLoop(r, func() bool { r.h.goDock("terminal"); return true })
	r.s.WaitFor(t, "the keyboard in the terminal again", func(string) bool {
		return onLoop(r, func() bool { return r.h.within("terminalView") })
	})
	r.keys(t, decltest.Type("exit\r")...)
	r.waitDock(t, "the terminal gone with its shell", "editor")
	r.s.WaitFor(t, "the bar auto-hiding again", func(string) bool { return r.menuAutoHides() })
}

// A plugin card hidden with its plugin running stays in the dock; turned off, it leaves.
func TestAHiddenCardStaysInTheDockWhileItsPluginRuns(t *testing.T) {
	dir := t.TempDir()
	installPlugin7(t, dir, "card", "dialog", "session", rightCard+
		"[[commands]]\nid = \"toggle\"\ntitle = \"Show the card\"\nkey = \"s\"\n"+sessionCommands)
	r, logs := pluginTUI(t, dir)
	r.pluginCommand("card", "login", "tok")
	waitSaid(t, logs, "card", "login kot <nil>")
	// "card" is the Lua card's :go name: a plugin card of that name takes its panel id
	card := onLoop(r, func() string { return r.h.widgetName(cardPanelID("card")) })
	r.waitDock(t, "the card shown", "editor", card)
	run := runOf(r, "card")
	r.pluginCommand("card", "toggle") // hidden, its plugin running on
	waitSaid(t, logs, "card", "hide")
	r.waitDock(t, "the hidden card kept", "editor", card)
	r.turnOff("card")
	waitExited(t, run)
	r.waitDock(t, "the card gone with its plugin", "editor")
}

// Help is the menu bar's last menu, on the left; the dock is at the bar's right end.
func TestHelpIsTheLastMenuOnTheLeft(t *testing.T) {
	_, r := runShellTUI(t, map[string]string{prefMenuHide: "false"})
	r.s.WaitForText(t, "Help")
	bar := strings.TrimRight(strings.Split(r.s.String(), "\n")[0], " ")
	help, system := strings.Index(bar, "Help"), strings.Index(bar, "System")
	if help < 0 || system < 0 || help < system || !strings.HasSuffix(bar, editorIcon) {
		t.Fatalf("the bar: %q; want System, then Help, and the dock's %s at the right end", bar, editorIcon)
	}
	// Help is a word away from System, not pushed to the right end
	if gap := help - system - len("System"); gap > 4 {
		t.Fatalf("Help sits %d columns after System: %q", gap, bar)
	}
}

// Every dock icon is one cell, and East Asian Width neutral: an ambiguous one is two cells in a CJK
// terminal, and every cell after it would be off by one.
func TestTheDockIconsAreOneNeutralCell(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	icons := map[string]string{"editor": editorIcon, "plugin card": "⧉"}
	for _, s := range onLoop(r, func() []PanelSpec { return r.h.panelSpecs() }) {
		icons[s.ID] = s.Icon
	}
	for name, icon := range icons {
		if icon == "" {
			t.Errorf("%s has no icon", name)
			continue
		}
		rs := []rune(icon)
		if len(rs) != 1 || tuicore.StringWidth(icon) != 1 {
			t.Errorf("%s's icon %q is not one cell", name, icon)
			continue
		}
		if k := width.LookupRune(rs[0]).Kind(); k != width.Neutral && k != width.EastAsianNarrow {
			t.Errorf("%s's icon %q (U+%04X) is %v: want Neutral", name, icon, rs[0], k)
		}
	}
}

// hidden is whether panel id is hidden by the dock: open, its window minimized.
func (r *running) hidden(id string) bool {
	return onLoop(r, func() bool { return r.h.panelHidden(id) })
}

// dockHiddenRow is the dock row of :go name key's "hidden" role.
func (r *running) dockHiddenRow(key string) bool {
	return onLoop(r, func() bool {
		for i, t := range r.h.dockRows.All() {
			if t.name == key {
				v, _ := r.h.dockRowOf(i, t)["hidden"].(bool)
				return v
			}
		}
		return false
	})
}

// A shown panel's icon hides it, not closed: it stays open and in the dock, dimmed, its frame gone
// from the screen and the keyboard back in the editor. Its icon again shows it as it was, the
// keyboard in it. Closed for real while hidden, it leaves the dock.
func TestADockIconHidesAndShowsItsPanel(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n"}})
	r := attached(t, d)
	onLoop(r, func() bool { r.h.togglePanel("explorer"); return true })
	r.s.WaitForText(t, "┌ explorer")
	r.waitDock(t, "the explorer listed", "editor", "explorer")

	onLoop(r, func() bool { r.h.goDock("explorer"); return true })
	r.s.WaitFor(t, "the explorer hidden", func(sc string) bool { return !strings.Contains(sc, "┌ explorer") })
	if !r.panelIsOpen("explorer") || !r.hidden("explorer") {
		t.Fatalf("hidden: open %v, minimized %v; want open, minimized (hidden, not closed)", r.panelIsOpen("explorer"), r.hidden("explorer"))
	}
	r.waitDock(t, "the hidden explorer kept", "editor", "explorer")
	r.s.WaitFor(t, "its icon dimmed", func(string) bool { return r.dockHiddenRow("explorer") })
	r.s.WaitFor(t, "the keyboard in the editor", func(string) bool { return r.focused("editor") })

	onLoop(r, func() bool { r.h.goDock("explorer"); return true })
	r.s.WaitForText(t, "┌ explorer")
	r.s.WaitFor(t, "the keyboard in the explorer", func(string) bool { return r.focusedOn("explorerTree") })
	if r.hidden("explorer") || r.dockHiddenRow("explorer") {
		t.Fatal("the explorer shown again is still marked hidden")
	}

	onLoop(r, func() bool { r.h.goDock("explorer"); return true }) // hidden again, then closed for real
	r.s.WaitFor(t, "hidden again", func(string) bool { return r.hidden("explorer") })
	onLoop(r, func() bool { r.h.closeDrawer("explorer"); return true })
	r.waitDock(t, "the closed explorer gone", "editor")
}

// The editor's icon hides every shown panel and remembers them; its next click shows the same ones
// again. A panel opened, shown or closed in between forgets them: the next click hides.
func TestTheEditorsIconHidesThePanelsAndShowsThemAgain(t *testing.T) {
	_, r := runShellTUI(t, nil)
	onLoop(r, func() bool { r.h.togglePanel("explorer"); r.h.terminals.toggleTerminal(); return true })
	r.s.WaitFor(t, "a shell running", func(string) bool { return r.shellsRunning() == 1 })
	r.waitDock(t, "both listed", "editor", "explorer", "terminal")

	onLoop(r, func() bool { r.h.goDock("editor"); return true })
	r.s.WaitFor(t, "both hidden", func(string) bool { return r.hidden("explorer") && r.hidden("terminal") })
	r.s.WaitFor(t, "the keyboard in the editor", func(string) bool { return r.focused("editor") })
	onLoop(r, func() bool { r.h.goDock("editor"); return true })
	r.s.WaitFor(t, "both shown again", func(string) bool { return !r.hidden("explorer") && !r.hidden("terminal") })
	if !r.panelIsOpen("explorer") || !r.panelIsOpen("terminal") {
		t.Fatal("the editor's icon closed a panel it should have shown")
	}

	// hidden again, the explorer shown by its own icon: the editor's next click hides it, and the
	// terminal stays hidden rather than coming back
	onLoop(r, func() bool { r.h.goDock("editor"); return true })
	r.s.WaitFor(t, "both hidden again", func(string) bool { return r.hidden("explorer") && r.hidden("terminal") })
	onLoop(r, func() bool { r.h.goDock("explorer"); return true })
	r.s.WaitFor(t, "the explorer shown", func(string) bool { return !r.hidden("explorer") })
	onLoop(r, func() bool { r.h.goDock("editor"); return true })
	r.s.WaitFor(t, "the explorer hidden by the editor's icon", func(string) bool { return r.hidden("explorer") })
	if !r.hidden("terminal") {
		t.Fatal("after a panel was shown in between, the editor's icon brought back the terminal it had hidden before")
	}

	// a panel closed in between: the next click hides what is shown, and brings back none
	onLoop(r, func() bool { r.h.closeDrawer("explorer"); return true })
	r.s.WaitFor(t, "the explorer closed", func(string) bool { return !r.panelIsOpen("explorer") })
	if !onLoop(r, func() bool { r.h.goDock("editor"); return r.h.panelHidden("terminal") }) {
		t.Fatal("after a panel closed in between, the editor's icon brought back a panel it had hidden before")
	}
}
