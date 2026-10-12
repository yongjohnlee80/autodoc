package tui

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
	lua "github.com/yuin/gopher-lua"
)

// THE LUA DOCS — docs/lua/*.md teach Lua commands, and every ```lua block in them is a script a
// reader pastes. TestTheLuaDocsRun loads each block, in the files' order, into a running TUI's
// engine against a real daemon, and runs the commands it defines as its page says they behave
// (luaDocChecks, by the block's first line). TestTheLuaReferenceIsComplete holds reference.md's
// tables to the engine, both ways.

const luaDocsDir = "../docs/lua"

// luaBlock is one ```lua block of the docs: its file and the line its source starts on, its first
// line (a comment naming the script), and its source.
type luaBlock struct {
	file       string
	at         int
	first, src string
}

// luaDocBlocks reads every ```lua block of docs/lua/*.md, in the files' name order.
func luaDocBlocks(t *testing.T) []luaBlock {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(luaDocsDir, "*.md"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no docs in %s: %v", luaDocsDir, err)
	}
	slices.Sort(files)
	var out []luaBlock
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var src []string
		in, at := false, 0
		for i, line := range strings.Split(string(b), "\n") {
			switch {
			case !in && line == "```lua":
				in, at, src = true, i+2, nil
			case in && line == "```":
				in = false
				if len(src) == 0 {
					t.Fatalf("%s:%d: an empty ```lua block", f, at)
				}
				out = append(out, luaBlock{file: filepath.Base(f), at: at, first: src[0], src: strings.Join(src, "\n")})
			case in:
				src = append(src, line)
			}
		}
		if in {
			t.Fatalf("%s: a ```lua block never closes", f)
		}
	}
	return out
}

// page makes the editor's text text, the cursor at line, col (1-based), in Normal mode.
func (r *running) page(text string, line, col int) {
	onLoop(r, func() bool {
		r.h.core.SetValue(text)
		r.h.core.SetCursor(widget.TextPos{Line: line - 1, Col: col - 1})
		return true
	})
}

// selectText selects from l1, c1 up to l2, c2 (1-based), as v does.
func (r *running) selectText(l1, c1, l2, c2 int) {
	onLoop(r, func() bool {
		r.h.core.Select(widget.TextRange{From: widget.TextPos{Line: l1 - 1, Col: c1 - 1}, To: widget.TextPos{Line: l2 - 1, Col: c2 - 1}})
		return true
	})
}

// cursorAt is the cursor, 1-based.
func (r *running) cursorAt() (int, int) {
	p := onLoop(r, r.h.core.Cursor)
	return p.Line + 1, p.Col + 1
}

func (r *running) undo() { onLoop(r, func() bool { r.h.core.Undo(); return true }) }

func (r *running) clearNotices() { onLoop(r, func() bool { r.h.noticeLog.items = nil; return true }) }

// mustRun runs a command line that must succeed.
func (r *running) mustRun(t *testing.T, line string) {
	t.Helper()
	if err := r.luaRun(line); err != nil {
		t.Fatalf(":%s: %v", line, err)
	}
}

// mustRefuse runs a command line that must fail, its error saying want.
func (r *running) mustRefuse(t *testing.T, line, want string) {
	t.Helper()
	if err := r.luaRun(line); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf(":%s: %v; want an error saying %q", line, err, want)
	}
}

// wantText fails unless the editor's text is want.
func (r *running) wantText(t *testing.T, after, want string) {
	t.Helper()
	if got := r.editorText(); got != want {
		t.Fatalf("after %s: %q; want %q", after, got, want)
	}
}

// wantCursor fails unless the cursor is at line, col (1-based).
func (r *running) wantCursor(t *testing.T, after string, line, col int) {
	t.Helper()
	if l, c := r.cursorAt(); l != line || c != col {
		t.Fatalf("after %s: the cursor at %d,%d; want %d,%d", after, l, c, line, col)
	}
}

// luaDocChecks run each documented command as its page says it behaves, by its block's first line.
// A block that defines a command needs one; a block that defines none (a helpers file) only loads.
var luaDocChecks = map[string]func(t *testing.T, r *running){
	// README.md
	"-- lines.lua: :lines says how long the page is": func(t *testing.T, r *running) {
		r.page("a\nb\nc", 1, 1)
		r.mustRun(t, "lines")
		r.waitNotice(t, "the page has 3 lines")
	},

	// reference.md
	"-- shape.lua: :shape shows every field autodoc.command takes": func(t *testing.T, r *running) {
		r.mustRun(t, "sh  one  two ")
		r.waitNotice(t, "shape: 2 args, line 'one  two'")
		cmd := onLoop(r, func() widget.Command { c, _ := r.h.core.Commands().Lookup("shape"); return c })
		if cmd.Args != "[words]" || cmd.Help != "show what run gets" || !slices.Equal(cmd.Aliases, []string{"sh"}) {
			t.Fatalf(":shape registered as %+v", cmd)
		}
	},
	"-- peek.lua: :peek <path> shows a document's first line, read through autodoc.call": func(t *testing.T, r *running) {
		r.mustRun(t, "peek a.md")
		r.waitNotice(t, "peek: # A")
		r.mustRun(t, "peek missing.md")
		r.waitNotice(t, "peek: -32061 ")
		r.mustRefuse(t, "peek", "peek <path>, in a workspace")
	},

	// tutorial-vim-commands.md
	"-- hello.lua: :hello says hello": func(t *testing.T, r *running) {
		r.mustRun(t, "hello")
		r.waitNotice(t, "Hello from Lua")
	},
	"-- hello.lua, step 2: :hello [name], also :hi": func(t *testing.T, r *running) {
		r.mustRun(t, "hi Ada Lovelace")
		r.waitNotice(t, "Hello, Ada Lovelace (2 words)")
		r.mustRun(t, "hello")
		r.waitNotice(t, "Hello, world (0 words)")
		// what Tab offers for "he": hello, with its arguments and help
		got := onLoop(r, func() []widget.Command { return r.h.core.Commands().Complete("he") })
		if i := slices.IndexFunc(got, func(c widget.Command) bool { return c.Name == "hello" }); i < 0 ||
			got[i].Args != "[name]" || got[i].Help != "say hello to someone" {
			t.Fatalf("Tab on he: %+v", got)
		}
	},
	"-- where.lua: :where says where the cursor is, and what is selected": func(t *testing.T, r *running) {
		r.page("alpha\nbeta gamma", 2, 6)
		r.mustRun(t, "where")
		r.waitNotice(t, "line 2 of 2, column 6: beta gamma")
		r.selectText(2, 6, 2, 11)
		r.mustRun(t, "where")
		r.waitNotice(t, "; selected 2,6 to 2,11: gamma")
	},
	"-- edit.lua: :quote, :callout and :squeeze": func(t *testing.T, r *running) {
		r.page("one\ntwo\nthree", 1, 1)
		r.selectText(1, 1, 2, 4)
		r.mustRun(t, "quote")
		r.wantText(t, ":quote on two lines", "> one\n> two\nthree")
		r.undo()
		r.wantText(t, "one undo of :quote", "one\ntwo\nthree")
		r.page("one\ntwo\nthree", 3, 2)
		r.mustRun(t, "quote")
		r.wantText(t, ":quote with no selection", "one\ntwo\n> three")

		r.page("Intro", 1, 1)
		r.mustRun(t, "callout tip")
		r.wantText(t, ":callout tip", "Intro\n\n> [!TIP]\n> ")
		if l, _ := r.cursorAt(); l != 4 {
			t.Fatalf(":callout left the cursor on line %d", l)
		}

		r.page("a\n\n\n\nb\n\nc", 1, 1)
		r.mustRun(t, "squeeze")
		r.wantText(t, ":squeeze", "a\n\nb\n\nc")
		r.waitNotice(t, "squeeze: 2 blank lines removed")
		r.undo()
		r.wantText(t, "one undo of :squeeze", "a\n\n\n\nb\n\nc")
	},
	"-- next.lua: :next <term> goes to the next match; :tasks finds the open tasks": func(t *testing.T, r *running) {
		r.page("beta\nalpha beta\nbeta", 1, 1)
		r.mustRun(t, "next beta")
		r.wantCursor(t, ":next beta", 2, 7)
		r.mustRun(t, "next beta")
		r.wantCursor(t, ":next beta again", 3, 1)
		r.mustRefuse(t, "next BETA", "next: no BETA on the page") // a capital letter matches exactly
		r.mustRefuse(t, "next zeta", "next: no zeta on the page")
		r.mustRefuse(t, "next", "next <term>")
		r.mustRun(t, "next beta")
		r.wantCursor(t, ":next beta, wrapping", 1, 1)

		r.page("# Plan\n- [x] done\n- [ ] open one", 1, 1)
		r.mustRun(t, "tasks")
		r.s.WaitFor(t, ":tasks went to the open task", func(string) bool { l, _ := r.cursorAt(); return l == 3 })
	},
	"-- wc.lua: :wc counts the selection's words, or the page's": func(t *testing.T, r *running) {
		r.page("héllo wörld\nsecond line", 1, 1)
		r.mustRun(t, "wc")
		r.waitNotice(t, "the page: 4 words, 2 lines, 23 characters")
		r.selectText(1, 7, 1, 12)
		r.mustRun(t, "wc")
		r.waitNotice(t, "the selection: 1 word, 1 line, 5 characters")
	},
	"-- toc.lua: :toc [depth] inserts a table of contents of the page's headings": func(t *testing.T, r *running) {
		const page = "# Title\n\n## Install\n\n### From source ###\n\n```sh\n## not a heading\n```\n\n## Use\n#### Deep"
		r.page(page, 1, 1)
		r.mustRun(t, "toc")
		r.wantText(t, ":toc", "# Title\n\n- [[#Install]]\n  - [[#From source]]\n- [[#Use]]"+page[len("# Title"):])
		r.undo()
		r.mustRun(t, "toc 2")
		r.wantText(t, ":toc 2", "# Title\n\n- [[#Install]]\n- [[#Use]]"+page[len("# Title"):])
		r.page("# Title\nno sections", 1, 1)
		r.mustRefuse(t, "toc", "toc: no ## headings to list")
	},
	"-- link.lua: :link <term> inserts a [[wikilink]] to the workspace's best match": func(t *testing.T, r *running) {
		r.page("See  for more.", 1, 5)
		r.mustRun(t, "link kestrel")
		r.waitNotice(t, "link: [[a]]")
		r.wantText(t, ":link kestrel", "See [[a]] for more.")
		r.mustRun(t, "link zebrafinch")
		r.waitNotice(t, "link: nothing matches zebrafinch")
		r.mustRefuse(t, "link", "link <term>")
	},
	"-- table.lua: AutoDoc's :table, also taking the header's names": func(t *testing.T, r *running) {
		r.page("", 1, 1)
		r.mustRun(t, "table Name Role")
		r.wantText(t, ":table Name Role", "| Name | Role |\n| ----- | ----- |\n|  |  |")
		r.page("Intro", 1, 1)
		r.mustRun(t, "table 2 1")
		r.wantText(t, ":table 2 1", "Intro\n\n| Column1 | Column2 |\n| ----- | ----- |\n|  |  |")
	},
	"-- 50-rule.lua: :rule puts a horizontal rule under the line, with 00-helpers.lua's my.under": func(t *testing.T, r *running) {
		r.page("para", 1, 1)
		r.mustRun(t, "rule")
		r.wantText(t, ":rule", "para\n\n---")
	},
}

// TestTheLuaDocsRun: every ```lua block in docs/lua loads in the engine AutoDoc runs, and every
// command one defines does what its page says (luaDocChecks).
func TestTheLuaDocsRun(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n\nkestrel notes\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	blocks := luaDocBlocks(t)
	if len(blocks) < 12 {
		t.Fatalf("%d Lua examples in the docs", len(blocks))
	}
	seen := map[string]bool{}
	for _, b := range blocks {
		where := fmt.Sprintf("%s:%d", b.file, b.at)
		if !strings.HasPrefix(b.first, "-- ") {
			t.Fatalf("%s: a Lua block starts with %q, not a comment naming it", where, b.first)
		}
		if seen[b.first] {
			t.Fatalf("%s: two Lua blocks start %q", where, b.first)
		}
		seen[b.first] = true
		// its errors name the block's first line as line 1
		if err := onLoop(r, func() error { return r.h.scripts.Load(where, []byte(b.src)) }); err != nil {
			t.Fatalf("%s: %q does not load: %v", where, b.first, err)
		}
		check, ok := luaDocChecks[b.first]
		if !ok {
			if strings.Contains(b.src, "autodoc.command") {
				t.Fatalf("%s: %q defines a command and has no check in luaDocChecks", where, b.first)
			}
			continue
		}
		r.clearNotices()
		t.Run(strings.TrimPrefix(b.first, "-- "), func(t *testing.T) { check(t, r) })
	}
	for first := range luaDocChecks {
		if !seen[first] {
			t.Errorf("luaDocChecks has %q, which no Lua block of the docs starts with", first)
		}
	}
}

// TestTheLuaReferenceIsComplete: reference.md lists every method of ed, every function of the
// autodoc table and every daemon verb the engine makes, and nothing the engine lacks; the calls it
// shows as examples exist.
func TestTheLuaReferenceIsComplete(t *testing.T) {
	b, err := os.ReadFile(filepath.Join(luaDocsDir, "reference.md"))
	if err != nil {
		t.Fatal(err)
	}
	ref := string(b)
	listed := func(re string) []string {
		var out []string
		for _, m := range regexp.MustCompile(re).FindAllStringSubmatch(ref, -1) {
			out = append(out, m[1])
		}
		return out
	}
	refMethods := listed("(?m)^\\| `ed:([a-z_]+)\\(")
	refFunctions := listed("(?m)^\\| `autodoc\\.([a-z_]+)[({]")
	_, verbs, _ := strings.Cut(ref, "\n## The daemon's verbs\n")
	verbs, _, _ = strings.Cut(verbs, "\n## ")
	refDomains := map[string][]string{}
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\| (.+) \\|$").FindAllStringSubmatch(verbs, -1) {
		for _, v := range regexp.MustCompile("`([a-z_]+)`").FindAllStringSubmatch(m[2], -1) {
			refDomains[m[1]] = append(refDomains[m[1]], v[1])
		}
	}
	if len(refMethods) < 10 || len(refFunctions) < 4 || len(refDomains) < 5 {
		t.Fatalf("the reference's tables did not parse: %v %v %v", refMethods, refFunctions, refDomains)
	}

	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	r.waitNamespaces(t)
	r.luaLoad(t, `
autodoc.command{ name = "ed-methods", run = function(ed)
  ed_methods = {}
  for name in pairs(getmetatable(ed).__index) do ed_methods[#ed_methods + 1] = name end
end }`)
	r.mustRun(t, "ed-methods")
	methods := onLoop(r, func() []string {
		var out []string
		r.h.scripts.L.GetGlobal("ed_methods").(*lua.LTable).ForEach(func(_, v lua.LValue) { out = append(out, v.String()) })
		return out
	})
	type engine struct {
		functions []string
		domains   map[string][]string
	}
	e := onLoop(r, func() engine {
		e := engine{domains: map[string][]string{}}
		r.h.scripts.Global().ForEach(func(k, v lua.LValue) {
			switch x := v.(type) {
			case *lua.LFunction:
				e.functions = append(e.functions, k.String())
			case *lua.LTable:
				x.ForEach(func(verb, _ lua.LValue) { e.domains[k.String()] = append(e.domains[k.String()], verb.String()) })
			}
		})
		return e
	})
	functions, domains := e.functions, e.domains
	same := func(what string, ref, engine []string) {
		t.Helper()
		for _, n := range ref {
			if !slices.Contains(engine, n) {
				t.Errorf("reference.md lists %s%s, which the engine does not have", what, n)
			}
		}
		for _, n := range engine {
			if !slices.Contains(ref, n) {
				t.Errorf("the engine has %s%s, which reference.md does not list", what, n)
			}
		}
	}
	same("ed:", refMethods, methods)
	same("autodoc.", refFunctions, functions)
	same("the domain autodoc.", slices.Collect(maps.Keys(refDomains)), slices.Collect(maps.Keys(domains)))
	for domain, vs := range domains {
		same("autodoc."+domain+".", refDomains[domain], vs)
	}
	for _, m := range regexp.MustCompile("`autodoc\\.([a-z_]+)\\.([a-z_]+)\\(").FindAllStringSubmatch(ref, -1) {
		if !slices.Contains(domains[m[1]], m[2]) {
			t.Errorf("reference.md calls autodoc.%s.%s, which the engine does not have", m[1], m[2])
		}
	}
}
