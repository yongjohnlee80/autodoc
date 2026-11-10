//go:build gui

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui"
)

// The window opens as tui.gui.titlebar's mirror says, and both halves go together: with AutoDoc's
// own bar the window gets one option more (no frame of the system's) and the launch asks for the
// bar in the layout; with the system's, or no mirror, neither. A window without the system's frame
// and without AutoDoc's bar would have no title bar at all, and the system's frame over AutoDoc's
// bar would draw two.
func TestTheWindowOpensAsTheTitleBarMirrorSays(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, "config.toml")
	// TOML literal strings: a path's separators are written as they are, on every OS
	body := "[server]\nsocket = '" + filepath.Join(dir, "s.sock") + "'\nstate_dir = '" + state + "'\ndata_dir = '" + filepath.Join(dir, "data") + "'\n"
	if err := os.WriteFile(conf, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	mirror := tui.GUITitleBarFile(conf)
	if mirror == "" {
		t.Fatal("the config's state directory was not read")
	}
	open := func(name string) (bool, int) {
		t.Helper()
		o, opts := windowFor(tui.LaunchOptions{ConfigPath: conf})
		return o.TitleBar, len(opts)
	}
	bar, base := open("no mirror")
	if bar {
		t.Fatal("no mirror: the launch asks for AutoDoc's bar; want the system's title bar")
	}
	for _, v := range []string{tui.TitleBarSystem, "something else"} {
		if err := os.WriteFile(mirror, []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if bar, n := open(v); bar || n != base {
			t.Fatalf("mirror %q: bar %v, %d options; want the system's title bar, %d options", v, bar, n, base)
		}
	}
	if err := os.WriteFile(mirror, []byte(tui.TitleBarOwn+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if bar, n := open(tui.TitleBarOwn); !bar || n != base+1 {
		t.Fatalf("mirror %q: bar %v, %d options; want AutoDoc's bar asked for and %d options (no frame of the system's)", tui.TitleBarOwn, bar, n, base+1)
	}
}
