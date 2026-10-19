package tui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The plugin and package folders' paths no program test takes: what discovery and loading skip,
// a confirmation answered with nothing pending, and an install with no build step.
func TestPluginAndPackageFolderEdges(t *testing.T) {
	pdir := t.TempDir()
	write := func(path, text string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(pdir, "README.md"), "not a plugin")
	write(filepath.Join(pdir, ".stage-1/plugin.toml"), "name = \"staged\"\n")
	write(filepath.Join(pdir, "real/plugin.toml"), "name = \"real\"\nkind = \"dialog\"\nprotocol = 4\ncommand = [\"./real\"]\n")
	if es := discoverPlugins(pdir); len(es) != 1 || es[0].m.Name != "real" || es[0].reason != "" {
		t.Fatalf("discovered %+v; want the one plugin, a file and a stage skipped", es)
	}

	if errs := (&Host{}).loadPackages(); errs != nil {
		t.Fatalf("no lua folder: %v", errs)
	}
	lua := t.TempDir()
	write(filepath.Join(lua, "notes.lua"), "return {}")
	write(filepath.Join(lua, ".stage-2/package.toml"), "name = \"staged\"\n")
	h := &Host{luaDir: lua}
	if errs := h.loadPackages(); errs != nil || len(h.userPackages) != 0 {
		t.Fatalf("a file and a stage: %v, packages %v; want both skipped", errs, h.userPackages)
	}
	if errs := (&Host{luaDir: filepath.Join(lua, "notes.lua")}).loadPackages(); len(errs) != 1 {
		t.Fatalf("a lua folder that is a file: %v; want its one error", errs)
	}

	pl := &plugins{}
	pl.pluginConfirmed() // nothing pending: nothing happens
	pl.pluginDeclined()
	if err := buildPlugin(context.Background(), t.TempDir(), pluginChange{dir: t.TempDir()}); err != nil {
		t.Fatalf("no build step: %v", err)
	}
}
