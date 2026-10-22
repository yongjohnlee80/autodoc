package tui

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/themes"
	"github.com/yongjohnlee80/golib/tui/widget"
)

func writeUserTheme(t *testing.T, file string, src []byte) {
	t.Helper()
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, src, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, file); err != nil {
		t.Fatal(err)
	}
}

func TestUserThemeLoadsReloadsFallsBackAndRecovers(t *testing.T) {
	dir := t.TempDir()
	src, err := fs.ReadFile(themes.FS(), "sepia.qml")
	if err != nil {
		t.Fatal(err)
	}
	src = []byte(strings.Replace(string(src), `backdrop: "#d4c4a0"`, `backdrop: "#010203"`, 1))
	file := filepath.Join(dir, "warm_paper.qml")
	writeUserTheme(t, file, src)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "page text\n"}}, daemonOpts{prefs: map[string]string{"tui.theme": "warm_paper", "tui.ruler": "60"}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{ThemeDir: dir}, 160, 20)
	r.ready(t)
	r.h.p.Post(func() { r.h.openPath("a.md") })
	r.s.WaitForText(t, "page text")
	// A theme switch is a reload of the whole layout, not a frame's change; a file that recovers
	// costs two (the fallback's layout reloaded by the save, then the switch back to the chosen
	// theme). Each wait is on daemonWait, a reload's clock, not a frame's.
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		r.waitWithin(t, what, daemonWait, cond)
	}
	waitColour := func(colour string) {
		t.Helper()
		want := cellColor(t, colour)
		waitFor("custom backdrop "+colour, func() bool { return r.s.Backend.Snapshot()[8][2].Attrs.BG == want })
	}
	waitColour("#010203")
	if got := onLoop(r, func() string { return r.h.theme }); got != "warm_paper" {
		t.Fatalf("theme %s", got)
	}
	if got := onLoop(r, func() string { return r.h.plugins.pluginTheme().Colors["app.backdrop"] }); got != "#010203" {
		t.Fatalf("plugin palette %s", got)
	}
	if got := onLoop(r, func() string { return r.h.terminals.lookState()["App.terminalBase"].(string) }); got != "#e6d9b9" {
		t.Fatalf("terminal palette %s", got)
	}
	before := onLoop(r, func() widget.TextPos { return r.h.core.Cursor() })
	next := []byte(strings.Replace(string(src), "#010203", "#040506", 1))
	writeUserTheme(t, file, next)
	waitColour("#040506")
	writeUserTheme(t, file, []byte(`Theme { app.window: "bad" }`))
	waitFor("fallback to dark", func() bool { return onLoop(r, func() string { return r.h.theme }) == defaultTheme })
	if got := onLoop(r, func() string { return r.h.core.Value() }); got != "page text\n" {
		t.Fatalf("document lost: %q", got)
	}
	if got := onLoop(r, func() widget.TextPos { return r.h.core.Cursor() }); got != before {
		t.Fatalf("cursor changed: %v -> %v", before, got)
	}
	writeUserTheme(t, file, next)
	waitColour("#040506")
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	waitFor("deletion fallback", func() bool { return onLoop(r, func() string { return r.h.theme }) == defaultTheme })
	writeUserTheme(t, file, src)
	waitColour("#010203")
}

func TestInvalidUserThemeAtStartupDoesNotBreakTheScreen(t *testing.T) {
	dir := t.TempDir()
	writeUserTheme(t, filepath.Join(dir, "broken.qml"), []byte(`Theme { app { window: "bad" } }`))
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "kept\n"}}, daemonOpts{prefs: map[string]string{"tui.theme": "broken"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{ThemeDir: dir})
	r.ready(t)
	r.s.WaitFor(t, "default theme", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == defaultTheme })
	if len(r.h.themeCatalog.Problems) == 0 {
		t.Fatal("no file/key diagnostics")
	}
}
