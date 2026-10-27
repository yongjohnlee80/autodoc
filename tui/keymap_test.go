package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	tuikey "github.com/yongjohnlee80/golib/tui/key"
)

// keysOfCommand are the keys the App's map gives the command id, on the loop.
func (r *running) keysOfCommand(id string) []tuikey.Seq {
	return onLoop(r, func() []tuikey.Seq {
		m := r.h.p.App().Keymap()
		c, ok := m.Command(id)
		if !ok {
			return nil
		}
		return m.KeysOf(c)
	})
}

// sourceKeys are the keys a Keys source of the App's state holds, by their names ("F9 ,").
func (r *running) sourceKeys(name string) string {
	return onLoop(r, func() string {
		v, _ := r.h.p.Tree().Source(name)
		ks, _ := v.Obj.(tuidecl.Keys)
		names := make([]string, len(ks))
		for i, k := range ks {
			names[i] = k.String()
		}
		return strings.Join(names, " ")
	})
}

func writeKeymap(t *testing.T, file, xml string) {
	t.Helper()
	if err := os.WriteFile(file, []byte(xml), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A user's keymap.xml moves a key by its command's id: the moved key runs the command, which holds it
// alone; SPC's card names the moved key, and help lists every key the file moves.
func TestAUsersKeymapMovesAKey(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	file := filepath.Join(t.TempDir(), "keymap.xml")
	writeKeymap(t, file, `<mapping>
<shortcut id="autodoc.window.openSearch"><key value="F5"/></shortcut>
<shortcut id="autodoc.leader.openRecent"><key value="Shift+R"/></shortcut>
<shortcut id="autodoc.window.maximizePanel"><key value="F9"/></shortcut>
<shortcut id="autodoc.terminal.mode"><key value="Ctrl+Y"/></shortcut>
<shortcut id="autodoc.terminal.suspend"><key value="Ctrl+B"/></shortcut>
<shortcut id="autodoc.window.nextPanelTab"><key value="F7"/></shortcut>
</mapping>`)
	r := runTUI(t, NewSession(d.sock, nil), Options{KeymapFile: file})
	r.connected(t)
	if got := r.keysOfCommand("autodoc.window.openSearch"); !slices.Equal(got, tuikey.MustParse("F5")) {
		t.Fatalf("openSearch's keys: %v, want F5 alone", got)
	}
	r.s.WaitFor(t, "SPC's card names the moved key", func(string) bool {
		return strings.Contains(r.source("App.leaderText"), "Shift+R  recent files")
	})
	r.s.WaitFor(t, "help lists the moved keys", func(string) bool {
		help := r.source("App.helpText")
		return strings.Contains(help, "YOUR KEYS") && strings.Contains(help, "autodoc.window.openSearch") &&
			strings.Contains(help, "keymap.xml, line 2")
	})
	// a terminal keeps maximize's, suspend's, the two tab keys and the four move keys from its program,
	// and toggles its mode on the mode key: all follow the map
	r.s.WaitFor(t, "the terminals follow the moved keys", func(string) bool {
		return r.sourceKeys("App.terminalHostKeys") == "F9 Ctrl+B F7 Ctrl+Shift+Tab Ctrl+H Ctrl+J Ctrl+K Ctrl+L" && r.sourceKeys("App.terminalModeKey") == "Ctrl+Y"
	})
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF5})
	r.s.WaitForText(t, "words; a * ends a prefix")
}

// The key file is followed: created after the start, it moves its keys; a broken save is refused,
// naming the file and line, and the keys stay as they were.
func TestTheKeymapFileIsFollowed(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	file := filepath.Join(t.TempDir(), "keymap.xml")
	r := runTUI(t, NewSession(d.sock, nil), Options{KeymapFile: file})
	r.connected(t)
	if got := r.keysOfCommand("autodoc.leader.openRecent"); !slices.Equal(got, tuikey.MustParse("R")) {
		t.Fatalf("with no file, openRecent's keys: %v, want r", got)
	}
	if hk, mk := r.sourceKeys("App.terminalHostKeys"), r.sourceKeys("App.terminalModeKey"); hk != "F11 Ctrl+Z Ctrl+Tab Ctrl+Shift+Tab Ctrl+H Ctrl+J Ctrl+K Ctrl+L" || mk != "Ctrl+T" {
		t.Errorf("with no file, the terminals' keys: hostKeys %q, modeKey %q", hk, mk)
	}
	writeKeymap(t, file, `<mapping><shortcut id="autodoc.leader.openRecent"><key value="Shift+R"/></shortcut></mapping>`)
	r.s.WaitFor(t, "the new file applied", func(string) bool {
		return slices.Equal(r.keysOfCommand("autodoc.leader.openRecent"), tuikey.MustParse("Shift+R")) &&
			strings.Contains(r.source("App.leaderText"), "Shift+R  recent files")
	})
	writeKeymap(t, file, "<mapping>\n<shortcut id=\"autodoc.leader.openRecent\"><key value=\"Ctrl+Nope\"/></shortcut></mapping>")
	r.waitNotice(t, "keymap.xml, line 2")
	if got := r.keysOfCommand("autodoc.leader.openRecent"); !slices.Equal(got, tuikey.MustParse("Shift+R")) {
		t.Errorf("after a refused save, openRecent's keys: %v, want Shift+R still", got)
	}
}

// runShellWithKeymap is runShellTUI with a key file: /bin/sh as the shell, the file as the user's.
func runShellWithKeymap(t *testing.T, file string) *running {
	t.Helper()
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PS1", "$ ")
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{KeymapFile: file})
	r.connected(t)
	return r
}

// openShell opens the terminal pane, its shell with the keyboard.
func (r *running) openShell(t *testing.T) {
	t.Helper()
	r.leader(t, '`')
	r.s.WaitForText(t, "┌ terminal")
	r.s.WaitFor(t, "the terminal's keyboard", func(string) bool { return r.focused("terminalView") })
	r.keys(t, decltest.Type("echo up\r")...)
	r.s.WaitForText(t, "up")
}

// leavesInput presses k in the shell, then Esc: k is the mode key when it left input mode, so the
// Esc, Normal mode's with nothing to cancel, hides the pane.
func (r *running) leavesInput(t *testing.T, what string, k tuicore.KeyEvent) {
	t.Helper()
	r.keys(t, k, esc())
	r.s.WaitFor(t, what+": the mode key, then Esc, hides the pane", func(sc string) bool { return !strings.Contains(sc, "┌ terminal") })
}

func keyOf(code rune, mods tuicore.Mods, text string) tuicore.KeyEvent {
	return tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: code, Mods: mods, Text: text}
}

// The terminals' keys follow the map whole, the comma's too, through the real terminal: the mode key
// moved to , or Ctrl+, or given two keys leaves input mode; unbound, Ctrl+T is the shell's; a saved
// file moves it again; and maximize moved to , reaches the Window past the shell.
func TestTheTerminalsKeysFollowTheMapWhole(t *testing.T) {
	comma := keyOf(',', 0, ",")
	ctrlComma := keyOf(',', tuicore.ModCtrl, "")
	for _, c := range []struct {
		name, keys string
		press      tuicore.KeyEvent
	}{
		{"the comma", `<key value=","/>`, comma},
		{"Ctrl and the comma", `<key value="Ctrl+,"/>`, ctrlComma},
		{"the second of two", `<key value="F8"/><key value=","/>`, comma},
	} {
		t.Run(c.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "keymap.xml")
			writeKeymap(t, file, `<mapping><shortcut id="autodoc.terminal.mode">`+c.keys+`</shortcut></mapping>`)
			r := runShellWithKeymap(t, file)
			r.openShell(t)
			r.leavesInput(t, c.name, c.press)
		})
	}
	t.Run("unbound, then moved by a save", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "keymap.xml")
		writeKeymap(t, file, `<mapping><shortcut id="autodoc.terminal.mode"><key value=""/></shortcut></mapping>`)
		r := runShellWithKeymap(t, file)
		r.openShell(t)
		// The barrier: a line the shell runs after Ctrl+T and Esc, and prints only if they reached
		// it. Had Ctrl+T left input mode, Esc would hide the pane and the line would never run. The
		// first Enter runs the line Ctrl+T and Esc typed.
		r.keys(t, append([]tuicore.Event{ctrlKey('t'), esc()}, decltest.Type("\recho $((40+2))unbound\r")...)...)
		r.s.WaitForText(t, "42unbound")
		if !r.termShown() {
			t.Fatal("the unbound mode key still left input mode")
		}
		writeKeymap(t, file, `<mapping><shortcut id="autodoc.terminal.mode"><key value="Ctrl+,"/></shortcut></mapping>`)
		r.s.WaitFor(t, "the saved mode key", func(string) bool { return r.sourceKeys("App.terminalModeKey") == "Ctrl+," })
		r.leavesInput(t, "after the save", ctrlComma)
	})
	t.Run("maximize on the comma", func(t *testing.T) {
		file := filepath.Join(t.TempDir(), "keymap.xml")
		writeKeymap(t, file, `<mapping><shortcut id="autodoc.window.maximizePanel"><key value=","/></shortcut></mapping>`)
		r := runShellWithKeymap(t, file)
		r.openShell(t)
		maximized := func() bool { return onLoop(r, func() bool { return r.h.panelMax["terminal"] }) }
		r.keys(t, comma)
		r.s.WaitFor(t, ", in the shell maximized the pane", maximizedNow(maximized, true))
		if !r.focused("terminalView") {
			t.Error("the comma took the keyboard out of the shell")
		}
	})
}
