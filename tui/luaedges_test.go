package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLuaGrantsOf: the preference's JSON is the grants; anything else is none.
func TestLuaGrantsOf(t *testing.T) {
	g := luaGrantsOf(`{"pkg":{"clipboard":true,"agents":false}}`)
	if !g["pkg"]["clipboard"] || g["pkg"]["agents"] || len(g) != 1 {
		t.Fatalf("%v", g)
	}
	if g := luaGrantsOf("not json"); g != nil {
		t.Fatalf("a broken preference gave %v", g)
	}
	if txt := luaGrantText([]string{"clipboard", "teleport"}); !strings.Contains(txt, "clipboard: read and write the clipboard") ||
		!strings.Contains(txt, "teleport: a capability AutoDoc does not know") {
		t.Fatalf("the card says %q", txt)
	}
}

// TestReadPackageRefusals: a folder is a package only with a well-formed manifest naming it and an
// init.lua.
func TestReadPackageRefusals(t *testing.T) {
	root := t.TempDir()
	writeFiles(t, root, map[string]string{
		"broken/package.toml":    "name = [",
		"badname/package.toml":   "name = \"bad name\"\n",
		"noinit/package.toml":    "name = \"noinit\"\n",
		"good/package.toml":      "name = \"good\"\nrequires = [\"clipboard\"]\n",
		"good/init.lua":          "",
		".adding-1/package.toml": "name = \"anything\"\n",
		".adding-1/init.lua":     "",
	})
	for dir, want := range map[string]string{
		"broken":  "package.toml: ",
		"badname": `a name of letters, digits, - and _, not "bad name"`,
		"noinit":  "no init.lua",
		"missing": "package.toml: ",
	} {
		if _, err := readPackage(filepath.Join(root, dir)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v; want %q", dir, err, want)
		}
	}
	for _, dir := range []string{"good", ".adding-1"} { // a staged clone's folder is not its name yet
		if m, err := readPackage(filepath.Join(root, dir)); err != nil {
			t.Errorf("%s: %v (%+v)", dir, err, m)
		}
	}
}

// TestLuaOddsAndEnds: a key whose command fails says so; a handler that fails is a notification and
// the others still run; an install that is cancelled leaves nothing behind, and :lua-install without
// a Lua folder is refused.
func TestLuaOddsAndEnds(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := attached(t, d)
	r.luaLoad(t, `
autodoc.map{ mode = "leader", keys = "z", command = "no-such-command" }
autodoc.on("ping", function() error("first fails", 0) end)
autodoc.on("ping", function(ed) ed:insert("second ran") end)`)
	r.clearNotices()
	onLoop(r, func() bool { r.h.luaFeature().luaKey("z"); return true })
	r.waitNotice(t, "no-such-command: ")
	r.page("", 1, 1)
	onLoop(r, func() bool { r.h.emitLua("ping", nil); return true })
	r.waitNotice(t, "a Lua handler failed: ping's handler: first fails")
	r.wantText(t, "the second handler", "second ran")

	stage := t.TempDir()
	if err := os.WriteFile(filepath.Join(stage, "init.lua"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	onLoop(r, func() bool {
		r.h.askPackage("https://example.invalid/x.git", stage, "abc1234", packageManifest{Name: "x", Version: "1", Requires: []string{"clipboard"}})
		return true
	})
	r.s.WaitForText(t, "install the Lua package x?")
	onLoop(r, func() bool { r.h.asker.rejected(); return true })
	r.s.WaitFor(t, "the stage removed", func(string) bool { _, err := os.Stat(stage); return os.IsNotExist(err) })

	if err := onLoop(r, func() error { return r.h.installPackage("https://example.invalid/x.git") }); err == nil ||
		!strings.Contains(err.Error(), "no Lua folder") {
		t.Fatalf("an install with no Lua folder: %v", err)
	}
}
