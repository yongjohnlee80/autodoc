package script

import (
	"strings"
	"testing"
	"testing/fstest"
)

// A package's require reaches its own folder: a.lua, a/init.lua, each loaded once into the
// package's environment; a name outside it, a module it lacks and a loop are the script's errors,
// and a module that failed is tried again by the next require. The package is gated.
func TestAPackageRequiresItsOwnModules(t *testing.T) {
	e, g, said := gatedEngine(t)
	fsys := fstest.MapFS{
		"init.lua": {Data: []byte(`
local util = require("util")
local deep = require("nested.deep")
editor.command{ name = "pkgcmd", run = function(ed)
  editor.notify(util.hello() .. deep.x .. tostring(require("util") == util) .. " " .. tostring(loads))
  for _, m in ipairs({"../escape", "missing", "loopa", "flaky", "flaky"}) do
    local ok, err = pcall(require, m)
    editor.notify(m .. ": " .. tostring(ok) .. " " .. tostring(err))
  end
  editor.notify(tostring(editor.secret("x")))
end }`)},
		"util.lua":             {Data: []byte(`loads = (loads or 0) + 1; return { hello = function() return "hi" end }`)},
		"nested/deep/init.lua": {Data: []byte(`return { x = "!" }`)},
		"loopa.lua":            {Data: []byte(`return require("loopb")`)},
		"loopb.lua":            {Data: []byte(`return require("loopa")`)},
		"flaky.lua":            {Data: []byte(`tries = (tries or 0) + 1; if tries == 1 then error("first try", 0) end; return "second"`)},
	}
	if err := e.LoadPackage("pkg", fsys, g); err != nil {
		t.Fatal(err)
	}
	if err := e.core.RunCommand("pkgcmd"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(*said, "\n")
	for _, want := range []string{
		"hi!true 1", // util loaded once, in the package's own globals
		`require "../escape": a module is the package's own`,
		`require "missing": no missing.lua or missing/init.lua in the package`,
		`a loop, "loopa" requires itself on the way`, // through loopb: each require names itself first
		`require "flaky": first try`,
		"flaky: true second",
		"nil", // editor.secret: gated, not granted
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lacks %q:\n%s", want, got)
		}
	}
	if v := e.L.GetGlobal("loads"); v.String() != "nil" {
		t.Fatalf("the package's global reached the shared globals: %v", v)
	}
	// require is the package's alone: an ungated script has none
	*said = nil
	if err := e.Load("mine.lua", []byte(`editor.notify(tostring(require))`)); err != nil || strings.Join(*said, "") != "nil" {
		t.Fatalf("an ungated require: %v %q", err, *said)
	}
	if err := e.LoadPackage("none", fstest.MapFS{}, g); err == nil || !strings.Contains(err.Error(), "init.lua") {
		t.Fatalf("a package without init.lua: %v", err)
	}
}
