package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// mirrored is what tui.gui.renderer's mirror at path holds; "" while there is none.
func mirrored(path string) string {
	b, _ := os.ReadFile(path)
	return strings.TrimSpace(string(b))
}

// tui.gui.renderer is mirrored into the state directory as the preference is read, so the next
// start's window, which opens before the store is read, takes it: auto at first. Options › Renderer
// sets it, the radios follow, the mirror is rewritten and the notice says it applies at the next
// start; a renderer that is neither is refused, and nothing changes.
func TestTheRendererPreferenceIsMirroredForTheNextStart(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	file := filepath.Join(t.TempDir(), guiRendererName)
	r := runTUI(t, NewSession(d.sock, nil), Options{RendererFile: file})
	r.ready(t)
	r.s.WaitFor(t, "the mirror auto, as read", func(string) bool { return mirrored(file) == RendererAuto })
	if r.source("App.rendererAuto") != "true" || r.source("App.rendererOpenGL") != "false" {
		t.Fatalf("the radios at the start: auto %s, opengl %s", r.source("App.rendererAuto"), r.source("App.rendererOpenGL"))
	}
	onLoop(r, func() bool { r.h.setRenderer(RendererOpenGL); return true })
	r.s.WaitFor(t, "the mirror opengl, the radio with it, the notice", func(string) bool {
		return mirrored(file) == RendererOpenGL && r.source("App.rendererOpenGL") == "true" &&
			r.noticed("the renderer changes at the next start")
	})
	r.s.WaitFor(t, "the store keeping it", func(string) bool {
		m, _ := d.db.Preferences(t.Context())
		return m[prefRenderer] == RendererOpenGL
	})
	onLoop(r, func() bool { r.h.setRenderer("metal"); return true })
	r.s.WaitFor(t, "the refusal", func(string) bool { return r.noticed(`no renderer "metal"`) })
	if got := mirrored(file); got != RendererOpenGL {
		t.Fatalf("a refused renderer rewrote the mirror: %q", got)
	}
}

// GUIRendererFile is the mirror in the state directory the config names, where Launch has the TUI
// write it; a config that cannot be read names none.
func TestTheRendererMirrorIsInTheConfigsStateDirectory(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	conf := filepath.Join(dir, "config.toml")
	body := "[server]\nsocket = \"" + filepath.Join(dir, "s.sock") + "\"\nstate_dir = \"" + state + "\"\ndata_dir = \"" + filepath.Join(dir, "data") + "\"\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, want := GUIRendererFile(conf), filepath.Join(state, guiRendererName); got != want {
		t.Fatalf("GUIRendererFile = %q, want %q", got, want)
	}
	if err := os.WriteFile(conf, []byte("[server]\nnot_a_setting = 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := GUIRendererFile(conf); got != "" {
		t.Fatalf("a config that cannot be read: %q, want none", got)
	}
}

// A mirror that could not be written is written the next time the preference is read or set, the
// same value: what was mirrored is only what was written.
func TestTheRendererMirrorIsWrittenOnceItCanBe(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	dir := filepath.Join(t.TempDir(), "not-yet")
	file := filepath.Join(dir, guiRendererName)
	r := runTUI(t, NewSession(d.sock, nil), Options{RendererFile: file})
	r.ready(t)
	if got := mirrored(file); got != "" {
		t.Fatalf("a mirror in a folder that is not there: %q", got)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool { r.h.setRenderer(RendererAuto); return true }) // the same value
	r.s.WaitFor(t, "the mirror written", func(string) bool { return mirrored(file) == RendererAuto })
}
