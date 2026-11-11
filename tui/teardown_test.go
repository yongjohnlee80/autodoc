package tui

import (
	"os"
	"path/filepath"
	"testing"
)

// TestAStoppedProgramHoldsNoLuaFolder: a program that has stopped holds none of its Lua packages'
// folders, so a second program in the same process can remove them, on every OS (Windows won't
// remove a folder an open root holds): its teardown, Run's, which a cell's rig runs too, closes
// the engine's roots. The first program read both packages through their roots: the one whose
// manifest names another said so.
func TestAStoppedProgramHoldsNoLuaFolder(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"kept/package.toml": "name = \"kept\"\n",
		"kept/init.lua":     `autodoc.command{ name = "kepthello", run = function() end }`,
		"held/package.toml": "name = \"other\"\n",
		"held/init.lua":     `autodoc.command{ name = "never", run = function() end }`,
	})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	t.Run("the first program", func(t *testing.T) {
		r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
		r.connected(t)
		r.waitNotice(t, "the Lua package held: package.toml names other, but its folder is held")
	})
	for _, p := range []string{"kept", "held"} {
		if err := os.RemoveAll(filepath.Join(dir, p)); err != nil {
			t.Errorf("the stopped program's package folder %s: %v", p, err)
		}
	}
}
