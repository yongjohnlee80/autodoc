package tui

import (
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// About lists the version and its date, then CONFIG with the log under it, PLUGINS, DAEMON SOCKET,
// AUTHOR and the licence, each label bold and the log italic.
func TestAboutListsItsLabelledLines(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	info := AboutInfo{Version: "v9.9.9", BuildDate: "2026-10-11", Config: "/cfg/config.toml", Log: "/state/serve.log", Plugins: "/cfg/plugins"}
	r := runTUI(t, NewSession(d.sock, nil), Options{AboutInfo: info})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.leader(t, 'A')
	r.s.WaitForText(t, "AutoDoc v9.9.9 (2026-10-11)")
	for _, want := range []string{"CONFIG: /cfg/config.toml", "(log: /state/serve.log)", "PLUGINS: /cfg/plugins",
		"DAEMON SOCKET: " + d.sock, "AUTHOR: Yong Sung John Lee", "Licensed under Apache 2.0"} {
		r.s.WaitForText(t, want)
	}
	rows := strings.Split(r.s.String(), "\n")
	snap := r.s.Backend.Snapshot()
	attr := func(label string) tuicore.AttrMask {
		for y, row := range rows {
			if x := strings.Index(row, label); x >= 0 {
				col := len([]rune(row[:x]))
				return snap[y][col].Attrs.Mask
			}
		}
		t.Fatalf("%q not on screen", label)
		return 0
	}
	for _, label := range []string{"CONFIG:", "PLUGINS:", "DAEMON SOCKET:", "AUTHOR:"} {
		if attr(label)&tuicore.AttrBold == 0 {
			t.Errorf("%s is not bold", label)
		}
	}
	if attr("(log:")&tuicore.AttrItalic == 0 {
		t.Error("the log line is not italic")
	}
	if attr("/cfg/config.toml")&tuicore.AttrBold != 0 {
		t.Error("the config's path is bold")
	}
}

// With no build date the headline is the version alone; a program's replacement text is shown as
// it is.
func TestAboutWithoutADateAndWithAReplacement(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{AboutInfo: AboutInfo{Version: "dev"}})
	if got := h.aboutHeadline(); got != "AutoDoc dev" {
		t.Errorf("headline %q", got)
	}
	h2 := newHost(NewSession("unused", nil), Options{About: "My product 1.0"})
	if got := h2.aboutText(); got != "My product 1.0" {
		t.Errorf("replacement %q", got)
	}
}

// About dims what is behind it: the status bar's cell (NORMAL), outside the dialog, is drawn faint by
// the scrim while About is open; before, it is not. (The "connected" toast stays above the scrim.)
func TestAboutDimsTheBackground(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{AboutInfo: AboutInfo{Version: "v9.9.9"}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitForText(t, "NORMAL")
	y, x := -1, -1
	for row, line := range strings.Split(r.s.String(), "\n") {
		if i := strings.Index(line, "NORMAL"); i >= 0 {
			y, x = row, len([]rune(line[:i]))
			break
		}
	}
	if y < 0 {
		t.Fatal("the status bar is not on screen")
	}
	faint := func() bool { return r.s.Backend.Snapshot()[y][x].Attrs.Mask&tuicore.AttrFaint != 0 }
	if faint() {
		t.Fatal("the status bar is faint before About opens")
	}
	r.leader(t, 'A')
	r.s.WaitForText(t, "AutoDoc v9.9.9")
	r.s.WaitFor(t, "the status bar dimmed behind About", func(string) bool { return faint() })
}
