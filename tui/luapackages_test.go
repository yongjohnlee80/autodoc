package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles writes files (path → content) under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for p, src := range files {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// TestLuaPackagesLoadGatedAndInstall: a folder of lua/ with a package.toml loads as a package:
// gated, its requires asked, its require its own; a folder whose manifest names another is an
// error, and the rest still load. :lua-install clones a package from git, asks, puts it in lua/
// and reloads; a second install of the same name is refused.
func TestLuaPackagesLoadGatedAndInstall(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	writeFiles(t, dir, map[string]string{
		"wc/package.toml": "name = \"wc\"\nversion = \"1.0.0\"\ndescription = \"counts\"\nrequires = [\"workspace.read\"]\n",
		"wc/lib.lua":      `return { label = "words" }`,
		"wc/init.lua": `local lib = require("lib")
autodoc.command{ name = "pkgwc", run = function(ed)
  local ok, err = autodoc.doc.read(autodoc.current_workspace(), "a.md", function() end)
  ed:insert(lib.label .. " " .. tostring(ok) .. " " .. tostring(err))
end }`,
		"bad/package.toml": "name = \"other\"\n",
		"bad/init.lua":     `autodoc.command{ name = "never", run = function() end }`,
	})
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.waitNamespaces(t)
	r.s.WaitForText(t, "let the Lua script wc use workspace.read?")
	r.waitNotice(t, "the Lua package bad: package.toml names other, but its folder is bad")

	r.page("", 1, 1)
	r.mustRun(t, "pkgwc")
	r.wantText(t, ":pkgwc ungranted", "words nil needs workspace.read: not granted")
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	r.s.WaitFor(t, "the grant", func(string) bool {
		return onLoop(r, func() bool { return r.h.prefs.luaGrants["wc"]["workspace.read"] })
	})
	r.page("", 1, 1)
	r.mustRun(t, "pkgwc")
	r.wantText(t, ":pkgwc granted", "words nil nil")

	// a package in a git repository
	repo := t.TempDir()
	writeFiles(t, repo, map[string]string{
		"package.toml": "name = \"hello\"\nversion = \"0.1.0\"\ndescription = \"says hello\"\n",
		"init.lua":     `autodoc.command{ name = "hello-pkg", run = function(ed) ed:insert("hello from a package") end }`,
	})
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "hello"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	r.mustRun(t, "lua-install "+repo)
	r.s.WaitForText(t, "install the Lua package hello?")
	onLoop(r, func() bool { r.h.asker.accepted(); return true })
	r.waitNotice(t, "installed the Lua package hello")
	// the reload runs at once; it says bad/ did not load again, the test's own broken package
	r.waitNotice(t, "lua-reload: 1 of the scripts did not load")
	if _, err := os.Stat(filepath.Join(dir, "hello", "init.lua")); err != nil {
		t.Fatalf("hello was not put in lua/: %v", err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Fatalf("lua/ holds %d entries, a staged clone left behind?", len(entries))
	}
	r.page("", 1, 1)
	r.mustRun(t, "hello-pkg")
	r.wantText(t, ":hello-pkg", "hello from a package")

	r.clearNotices()
	r.mustRun(t, "lua-install "+repo)
	r.waitNotice(t, "lua-install: not installed: hello is installed already")
	if entries, _ := os.ReadDir(dir); len(entries) != 3 {
		t.Fatalf("a refused install left %d entries", len(entries))
	}
	if err := r.luaRun("lua-install"); err == nil || !strings.Contains(err.Error(), "lua-install <git-url>") {
		t.Fatalf(":lua-install with no URL: %v", err)
	}
}

// TestAPackageReadsOnlyItsOwnFolder: a link inside a package that leads outside its folder is not
// the package's: a module linked out, a folder of modules linked out and an init.lua linked out are
// refused, while a link that stays inside the folder is followed.
func TestAPackageReadsOnlyItsOwnFolder(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	writeFiles(t, outside, map[string]string{
		"secret.lua":     `return "outside"`,
		"mods/inner.lua": `return "outside too"`,
		"init.lua":       `autodoc.command{ name = "linked-init", run = function() end }`,
	})
	writeFiles(t, dir, map[string]string{
		"sly/package.toml":    "name = \"sly\"\n",
		"sly/real.lua":        `return "inside"`,
		"linked/package.toml": "name = \"linked\"\n",
		"sly/init.lua": `autodoc.command{ name = "sly", run = function(ed)
  local out = {}
  for _, m in ipairs({"alias", "secret", "mods.inner"}) do
    local ok, v = pcall(require, m)
    out[#out + 1] = m .. "=" .. (ok and v or "refused")
  end
  ed:insert(table.concat(out, " "))
end }`,
	})
	for link, target := range map[string]string{
		filepath.Join(dir, "sly", "alias.lua"):   "real.lua", // inside: followed
		filepath.Join(dir, "sly", "secret.lua"):  filepath.Join(outside, "secret.lua"),
		filepath.Join(dir, "sly", "mods"):        filepath.Join(outside, "mods"),
		filepath.Join(dir, "linked", "init.lua"): filepath.Join(outside, "init.lua"),
	} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "kestrel\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{LuaDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.waitNotice(t, "the Lua package linked: no init.lua in the package's folder")
	r.page("", 1, 1)
	r.mustRun(t, "sly")
	r.wantText(t, ":sly", "alias=inside secret=refused mods.inner=refused")
	if err := r.luaRun("linked-init"); err == nil {
		t.Fatal("the init.lua linked outside its package ran")
	}
}
