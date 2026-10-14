package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The Lua host's paths no program test takes: the catalog's entries before the scripts start, a
// verb's capability for every effect, reload and autodoc.run with no catalog, and the editor
// socket's folder made private.
func TestLuaHostEdges(t *testing.T) {
	s := &scripting{}
	for _, e := range s.Catalog() {
		if err := e.Run(Invocation{}); err == nil || !strings.Contains(err.Error(), "have not started") {
			t.Errorf("%s before the scripts start: %v", e.ID, err)
		}
	}
	s.verbs = map[string]luaVerb{"a.r": {effect: "read"}, "a.w": {effect: "write"}, "a.s": {effect: "settings"}, "a.x": {}}
	for verb, want := range map[string]string{"a.r": "workspace.read", "a.w": "workspace.write", "a.s": "settings", "a.x": "\x00unclassified", "a.none": "\x00unclassified"} {
		if got := s.capabilityOf(verb); got != want {
			t.Errorf("capabilityOf(%s) = %q; want %q", verb, got, want)
		}
	}
	h := &Host{}
	if err := h.reloadScripts(); err == nil || !strings.Contains(err.Error(), "no command catalog") {
		t.Errorf("a reload with no catalog: %v", err)
	}
	if err := h.runFromLua("file.save", nil); !errors.Is(err, errUnknownCommand) {
		t.Errorf("autodoc.run with no catalog: %v", err)
	}
	if err := h.runFromLua("plugin.x.y", nil); err == nil || !strings.Contains(err.Error(), "plugin's command") {
		t.Errorf("autodoc.run of a plugin's: %v", err)
	}

	dir := filepath.Join(t.TempDir(), "editor")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(dir); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(dir); fi.Mode().Perm() != 0o700 {
		t.Fatalf("a wide folder was left %v", fi.Mode().Perm())
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	if err := privateDir(link); err == nil {
		t.Fatal("a link was taken for the socket's folder")
	}
}

// A package's autodoc.run and autodoc.menu go through their gates: run needs commands at the call,
// menu registers and asks at the row.
func TestAPackagesRunAndMenuGates(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	if err := onLoop(r, func() error {
		return r.h.loadGated("pkg", "pkg.lua", []byte(`autodoc.menu{ where = "plugins", title = "P", command = "w" }
autodoc.command{ name = "pkgrun", run = function(ed)
  local ok, err = autodoc.run("file.save")
  ed:insert(tostring(ok) .. " " .. tostring(err))
end }`))
	}); err != nil {
		t.Fatal(err)
	}
	r.page("", 1, 1)
	r.mustRun(t, "pkgrun")
	r.wantText(t, "a package's autodoc.run", "nil needs commands: not granted")
	if n := onLoop(r, func() int { return r.h.luaFeature().luaMenuRows().Len() }); n != 1 {
		t.Fatalf("the package's menu row: %d", n)
	}
}
