package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui"
)

// The window draws AutoDoc's own title bar only when tui.gui.titlebar's mirror names it: with no
// mirror (the first start), with the system's, with what the TUI never writes, and with one that
// cannot be read, the system draws it.
func TestTheWindowOwnsItsTitleBarOnlyWhenTheMirrorSaysSo(t *testing.T) {
	dir := t.TempDir()
	write := func(name, v string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(v), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, c := range []struct {
		name, path string
		own        bool
	}{
		{"no mirror", filepath.Join(dir, "none"), false},
		{"the system's", write("system", tui.TitleBarSystem+"\n"), false},
		{"own", write("own", tui.TitleBarOwn+"\n"), true},
		{"garbage", write("garbage", "chrome\n"), false},
		{"unreadable", dir, false}, // a directory
	} {
		if got := ownsTitleBar(c.path); got != c.own {
			t.Errorf("%s: ownsTitleBar %v, want %v", c.name, got, c.own)
		}
	}
}
