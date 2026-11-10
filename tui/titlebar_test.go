package tui

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tui.gui.titlebar is mirrored into the state directory as the preference is read, so the next
// start's window, which opens before the store is read, takes it: the system's at first. Options ›
// Title bar sets it, the radios follow, the mirror is rewritten and the notice says it applies at the
// next start; one that is neither is refused, and nothing changes.
func TestTheTitleBarPreferenceIsMirroredForTheNextStart(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	file := filepath.Join(t.TempDir(), guiTitleBarName)
	r := runTUI(t, NewSession(d.sock, nil), Options{TitleBarFile: file})
	r.ready(t)
	r.s.WaitFor(t, "the mirror the system's, as read", func(string) bool { return mirrored(file) == TitleBarSystem })
	if r.source("App.titleBarSystem") != "true" || r.source("App.titleBarOwn") != "false" {
		t.Fatalf("the radios at the start: system %s, own %s", r.source("App.titleBarSystem"), r.source("App.titleBarOwn"))
	}
	onLoop(r, func() bool { r.h.setTitleBar(TitleBarOwn); return true })
	r.s.WaitFor(t, "the mirror own, the radio with it, the notice", func(string) bool {
		return mirrored(file) == TitleBarOwn && r.source("App.titleBarOwn") == "true" &&
			r.noticed("the title bar changes at the next start")
	})
	r.waitWithin(t, "the store keeping it", daemonWait, func() bool {
		m, _ := d.db.Preferences(t.Context())
		return m[prefTitleBar] == TitleBarOwn
	})
	onLoop(r, func() bool { r.h.setTitleBar("chrome"); return true })
	r.s.WaitFor(t, "the refusal", func(string) bool { return r.noticed(`no title bar "chrome"`) })
	if got := mirrored(file); got != TitleBarOwn {
		t.Fatalf("a refused title bar rewrote the mirror: %q", got)
	}
}

// GUITitleBarFile is the mirror in the state directory the config names, where Launch has the TUI
// write it; a config that cannot be read names none.
func TestTheTitleBarMirrorIsInTheConfigsStateDirectory(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	conf := filepath.Join(dir, "config.toml")
	body := "[server]\nsocket = \"" + filepath.Join(dir, "s.sock") + "\"\nstate_dir = \"" + state + "\"\ndata_dir = \"" + filepath.Join(dir, "data") + "\"\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := GUITitleBarFile(conf), filepath.Join(state, guiTitleBarName); got != want {
		t.Fatalf("GUITitleBarFile = %q, want %q", got, want)
	}
	if err := os.WriteFile(conf, []byte("[server]\nnot_a_setting = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := GUITitleBarFile(conf); got != "" {
		t.Fatalf("a config that cannot be read: %q, want none", got)
	}
}

// The window's own title bar is written into the layout once, above the menu bar so the dock pins
// it at the very top, only for a window that opened without the system's: a window with the
// system's title bar, and a terminal (whatever the mirror says), get the layout without it.
func TestTheOwnTitleBarIsWrittenOnlyForAWindowThatAsksForIt(t *testing.T) {
	t.Parallel()
	const bar = "    TitleBar {\n"
	for _, c := range []struct {
		name          string
		gui, titleBar bool
		want          int
	}{
		{"a terminal", false, false, 0},
		{"a terminal, the mirror saying own", false, true, 0},
		{"a window with the system's title bar", true, false, 0},
		{"a window with its own", true, true, 1},
	} {
		h := newHost(NewSession("unused", nil), Options{GUI: c.gui, TitleBar: c.titleBar})
		doc := string(h.windowLayout(layout))
		if n := strings.Count(doc, bar); n != c.want {
			t.Errorf("%s: the layout holds %d title bars, want %d", c.name, n, c.want)
		}
		if c.want == 1 && strings.Index(doc, bar) > strings.Index(doc, "    MenuBar {\n") {
			t.Errorf("%s: the title bar comes after the menu bar, so it is not at the top", c.name)
		}
	}
	if !bytes.Equal(withTitleBar([]byte("Window {}"), true), []byte("Window {}")) {
		t.Error("a layout without the marker was written to")
	}
}
