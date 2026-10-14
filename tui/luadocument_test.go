package tui

import (
	"strings"
	"testing"
)

// docLua says what autodoc.document() gives, a field a part: path, workspace, kind, derived, then
// the frontmatter's title, tags, n, nested.k and 2026, then frontmatter_error.
const docLua = `
autodoc.command{ name = "doc", run = function(ed)
  local d = autodoc.document()
  local fm = d.frontmatter or {}
  local parts = {tostring(d.path), tostring(d.workspace), d.kind, tostring(d.derived), tostring(fm.title),
    type(fm.tags) == "table" and table.concat(fm.tags, ",") or tostring(fm.tags), tostring(fm.n),
    tostring(fm.nested and fm.nested.k), tostring(fm["2026"]), tostring(d.frontmatter_error)}
  autodoc.notify("doc: " .. table.concat(parts, "/"))
end }`

// TestLuaSeesTheDocument: autodoc.document() is the page in the editor: the draft, a note with its
// frontmatter read from the editor's text now, a text file, and frontmatter that is not YAML.
func TestLuaSeesTheDocument(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {
		"notes/a.md", "---\ntitle: Plan\ntags: [x, y]\nn: 3\nnested: {k: v}\n2026: year\n---\n# A\n",
		"b.txt", "plain\n",
	}})
	r := attached(t, d)
	r.luaLoad(t, docLua)

	r.mustRun(t, "doc")
	r.waitNotice(t, "doc: nil/nil/markdown/nil/nil/nil/nil/nil/nil/nil")

	r.openByPicker(t, "notes/a.md")
	r.waitFile(t, "notes/a.md")
	r.mustRun(t, "doc")
	r.waitNotice(t, "doc: notes/a.md/kb/markdown/nil/Plan/x,y/3/v/year/nil")

	r.openByPicker(t, "b.txt")
	r.waitFile(t, "b.txt")
	r.mustRun(t, "doc")
	r.waitNotice(t, "doc: b.txt/kb/text/nil/nil/nil/nil/nil/nil/nil")
	r.openByPicker(t, "notes/a.md")
	r.waitFile(t, "notes/a.md")

	// the editor's text now, not the file's: an edit not yet saved shows
	r.page("---\ntitle: Edited\n---\n", 1, 1)
	r.mustRun(t, "doc")
	r.waitNotice(t, "doc: notes/a.md/kb/markdown/nil/Edited/nil/nil/nil/nil/nil")
	r.page("---\ntags: [a\n---\n", 1, 1)
	r.clearNotices()
	r.mustRun(t, "doc")
	r.waitNotice(t, "/the frontmatter is not YAML: ")
	if !onLoop(r, func() bool {
		for _, n := range r.h.noticeLog.items {
			if strings.HasPrefix(n.text, "doc: notes/a.md/kb/markdown/nil/nil/nil/nil/nil/nil/the frontmatter is not YAML") {
				return true
			}
		}
		return false
	}) {
		t.Fatal("frontmatter that is not YAML: no frontmatter and its error")
	}
}

// TestLuaListsTheCatalog: autodoc.commands() is the catalog, an entry under the ':' names it owns
// now, then the editor's own verbs: a Lua command with its id and title, AutoDoc's find whose
// name a script took under its id with no name, and :w as the editor's.
func TestLuaListsTheCatalog(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.luaLoad(t, `
autodoc.command{ name = "find", title = "my find", run = function(ed) end }
autodoc.command{ name = "cmds", aliases = {"cs"}, run = function(ed)
  local rows = {}
  for _, c in ipairs(autodoc.commands()) do
    rows[#rows + 1] = c.id .. "|" .. c.name .. "|" .. table.concat(c.aliases, ",") .. "|" .. c.title .. "|" .. c.source
  end
  autodoc.notify("cmds: ;" .. table.concat(rows, ";") .. ";")
end }`)
	r.mustRun(t, "cmds")
	r.waitNotice(t, "cmds: ;")
	list := onLoop(r, func() string {
		for _, n := range r.h.noticeLog.items {
			if strings.HasPrefix(n.text, "cmds: ;") {
				return n.text
			}
		}
		return ""
	})
	for _, want := range []string{";lua.cmds|cmds|cs|cmds|lua;", ";lua.find|find||my find|lua;", ";find.term||", "|w|write||editor;"} {
		if !strings.Contains(list, want) {
			t.Errorf("autodoc.commands() lacks %q:\n%s", want, strings.ReplaceAll(list, ";", "\n"))
		}
	}
}
