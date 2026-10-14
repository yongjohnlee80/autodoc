package script

import (
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// probe loads src, which registers the command name, runs line, and returns what it notified.
func probe(t *testing.T, e *Engine, core *widget.EditorCore, said *[]string, src, line string) []string {
	t.Helper()
	*said = nil
	if err := e.Load("probe.lua", []byte(src)); err != nil {
		t.Fatal(err)
	}
	if err := core.RunCommand(line); err != nil {
		t.Fatalf(":%s: %v", line, err)
	}
	return *said
}

// ed:select selects a range as Visual mode does, ed:mode says so, and ed:unselect ends it; an
// empty range selects nothing. Without Vim's keys the mode is insert, and visual while selected.
func TestEdSelectsAndSaysTheMode(t *testing.T) {
	const src = `
editor.command{ name = "sel", run = function(ed)
  editor.notify(ed:mode())
  ed:select(1, 7, 1, 11)
  local l1, c1, l2, c2 = ed:selection()
  editor.notify(table.concat({ed:mode(), l1, c1, l2, c2, ed:text(l1, c1, l2, c2)}, " "))
  ed:unselect()
  editor.notify(ed:mode() .. " " .. tostring(ed:selection()))
  ed:select(2, 2, 2, 2)
  editor.notify(tostring(ed:selection()))
end }`
	e, core, said := engine(t, "alpha beta\ngamma")
	got := probe(t, e, core, said, src, "sel")
	if strings.Join(got, "|") != "normal|visual 1 7 1 11 beta|normal nil|nil" {
		t.Fatalf("Vim's keys: %q", got)
	}

	core = widget.NewEditorCore(widget.CoreInitialText("alpha beta\ngamma"), widget.CoreKeyset(widget.KeysetStandard))
	var plain []string
	e = New(core, WithNotify(func(s string) { plain = append(plain, s) }))
	t.Cleanup(e.Close)
	got = probe(t, e, core, &plain, src, "sel")
	if strings.Join(got, "|") != "insert|visual 1 7 1 11 beta|insert nil|nil" {
		t.Fatalf("the standard keys: %q", got)
	}
}

// ed:find_all is every match as {l1, c1, l2, c2}, ed:replace_all replaces them all as one undo
// step and says how many, and both take ignore_case and whole_word.
func TestEdFindsAndReplacesAll(t *testing.T) {
	e, core, said := engine(t, "Beta beta alphabeta\nbeta")
	got := probe(t, e, core, said, `
editor.command{ name = "all", run = function(ed)
  local out = {}
  for _, m in ipairs(ed:find_all("beta")) do out[#out + 1] = table.concat(m, ",") end
  editor.notify(table.concat(out, " "))
  editor.notify(#ed:find_all("beta", {ignore_case = true}) .. " " .. #ed:find_all("beta", {whole_word = true}) ..
    " " .. #ed:find_all("zeta"))
end }`, "all")
	if strings.Join(got, "|") != "1,6,1,10 1,16,1,20 2,1,2,5|4 2 0" {
		t.Fatalf("find_all: %q", got)
	}

	got = probe(t, e, core, said, `
editor.command{ name = "rall", run = function(ed, args)
  editor.notify(tostring(ed:replace_all(args[1], args[2], {ignore_case = args[3] == "i"})))
end }`, "rall beta X i")
	if strings.Join(got, "|") != "4" || core.Value() != "X X alphaX\nX" {
		t.Fatalf("replace_all: %q, text %q", got, core.Value())
	}
	undo(core) // one undo brings back every match
	if core.Value() != "Beta beta alphabeta\nbeta" {
		t.Fatalf("after one undo: %q", core.Value())
	}
	if got := probe(t, e, core, said, "", "rall zeta X"); strings.Join(got, "|") != "0" || core.Value() != "Beta beta alphabeta\nbeta" {
		t.Fatalf("replace_all of nothing: %q, %q", got, core.Value())
	}
}

// ed:find's at_cursor counts a match starting under the cursor, forward and backward, and wrap
// goes round; without it the match under the cursor is skipped, as before.
func TestEdFindAtTheCursor(t *testing.T) {
	e, core, said := engine(t, "one two one\ntwo")
	src := `
editor.command{ name = "f", run = function(ed, args)
  ed:set_cursor(tonumber(args[2]), tonumber(args[3]))
  local o = {at_cursor = args[4] == "at", backward = args[5] == "back", wrap = args[6] == "wrap"}
  editor.notify(table.concat({ed:find(args[1], o)}, ","))
end }`
	for line, want := range map[string]string{
		"f two 1 5 at":           "1,5,1,8",  // the match under the cursor
		"f two 1 5 not":          "2,1,2,4",  // skipped without at_cursor
		"f two 1 6 at":           "2,1,2,4",  // inside a match is not its start
		"f one 1 9 at back":      "1,9,1,12", // backward, the one under the cursor
		"f one 1 9 not back":     "1,1,1,4",
		"f one 2 1 at":           "",
		"f one 2 1 at fwd wrap":  "1,1,1,4",
		"f two 1 1 at back":      "",
		"f two 1 1 at back wrap": "2,1,2,4",
	} {
		if got := probe(t, e, core, said, src, line); strings.Join(got, "|") != want {
			t.Errorf(":%s found %q; want %q", line, got, want)
		}
	}
	// opts must be a table
	if err := e.Load("bad.lua", []byte(`editor.command{ name = "bad", run = function(ed) ed:find("x", "wrap") end }`)); err != nil {
		t.Fatal(err)
	}
	if err := core.RunCommand("bad"); err == nil || !strings.Contains(err.Error(), "table expected, got string") {
		t.Fatalf("opts a string: %v", err)
	}
}

// ed:read_only and ed:modified say what the core does; an edit on a read-only core is refused.
func TestEdSaysReadOnlyAndModified(t *testing.T) {
	e, core, said := engine(t, "text")
	src := `
editor.command{ name = "state", run = function(ed, args)
  if args[1] == "edit" then ed:insert("x") end
  editor.notify(tostring(ed:read_only()) .. " " .. tostring(ed:modified()))
end }`
	if got := probe(t, e, core, said, src, "state"); got[0] != "false false" {
		t.Fatalf("a fresh page: %q", got)
	}
	if got := probe(t, e, core, said, src, "state edit"); got[0] != "false true" {
		t.Fatalf("after an edit: %q", got)
	}
	core.MarkSaved()
	if got := probe(t, e, core, said, src, "state"); got[0] != "false false" {
		t.Fatalf("after a save: %q", got)
	}
	core.SetReadOnly(true)
	if got := probe(t, e, core, said, src, "state"); got[0] != "true false" {
		t.Fatalf("read-only: %q", got)
	}
	if err := core.RunCommand("state edit"); err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("an edit on a read-only page: %v", err)
	}
}

// run's fourth argument is how the command was called: the name as typed, an alias say, and the
// bang. A host running it with no name (a key) gives the command's own.
func TestRunGetsTheNameAndTheBang(t *testing.T) {
	e, core, said := engine(t, "")
	src := `
editor.command{ name = "hello", aliases = {"hi"}, run = function(ed, args, line, cmd)
  editor.notify(cmd.name .. " " .. tostring(cmd.bang) .. " " .. line)
end }`
	for line, want := range map[string]string{"hello": "hello false ", "hi! there": "hi true there", "hello!": "hello true "} {
		if got := probe(t, e, core, said, src, line); strings.Join(got, "|") != want {
			t.Errorf(":%s gave %q; want %q", line, got, want)
		}
	}
	cmd, _ := core.Commands().Lookup("hi")
	*said = nil
	if err := cmd.Run(widget.CommandContext{Bang: true}); err != nil || strings.Join(*said, "|") != "hello true " {
		t.Fatalf("run with no name: %v %q", err, *said)
	}
}

// text counts in characters, as the editor's columns do: columns, sub with string.sub's
// positions, chars; a decomposed é and a flag are one character each.
func TestTextCountsCharacters(t *testing.T) {
	e, core, said := engine(t, "")
	got := probe(t, e, core, said, `
editor.command{ name = "t", run = function(ed)
  local s = "cafe\204\129 🇰🇷!"
  editor.notify(editor.text.columns(s) .. " " .. #s .. " " .. editor.text.columns(""))
  editor.notify(table.concat(editor.text.chars(s), "|"))
  for _, ij in ipairs({{4, 4}, {-2}, {2, -3}, {0, 2}, {5, 99}, {4, 2}, {}, {-99, 1}}) do
    editor.notify("[" .. editor.text.sub(s, ij[1], ij[2]) .. "]")
  end
  local _, c = ed:insert(s)
  editor.notify(tostring(c))
end }`, "t")
	want := []string{"7 16 0", "c|a|f|é| |🇰🇷|!",
		"[é]", "[🇰🇷!]", "[afé ]", "[ca]", "[ 🇰🇷!]", "[]", "[café 🇰🇷!]", "[c]",
		"8"} // just past the seventh column: the editor counts as columns does
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// An error is said by its value: a table by __tostring, by message, or by its fields; nil and
// other values by their type. A file that fails to load is said without Lua's stack trace, and one
// that runs past the deadline says so.
func TestErrorsAreSaidClearly(t *testing.T) {
	e, core, _ := engine(t, "")
	if err := e.Load("errs.lua", []byte(`
local shown = setmetatable({}, {__tostring = function() return "shown by tostring" end})
editor.command{ name = "e", run = function(ed, args)
  local v = ({
    meta = shown,
    message = {message = "said by message", code = 3},
    fields = {"first", code = 7, why = "because", nested = {1}},
    none = nil,
    bool = false,
  })[args[1]]
  error(v)
end }`)); err != nil {
		t.Fatal(err)
	}
	for arg, want := range map[string]string{
		"meta":    "e: shown by tostring",
		"message": "e: said by message",
		"fields":  `e: an error {"first", code = 7, nested = {…}, why = "because"}`,
		"none":    "e: an error with no message",
		"bool":    "e: an error value, a boolean: false",
	} {
		if err := core.RunCommand("e " + arg); err == nil || err.Error() != want {
			t.Errorf(":e %s: %v; want %q", arg, err, want)
		}
	}

	err := e.Load("broken.lua", []byte("editor.command{ name = 'x', run = function(ed) end }\nerror('at load')"))
	if err == nil || err.Error() != "broken.lua: broken.lua:2: at load" {
		t.Fatalf("a load error: %q", err)
	}
	err = e.Load("syntax.lua", []byte("this is not lua"))
	if err == nil || strings.Contains(err.Error(), "stack traceback") || !strings.HasPrefix(err.Error(), "syntax.lua: ") {
		t.Fatalf("a syntax error: %q", err)
	}
	start := time.Now()
	err = e.Load("spin.lua", []byte("while true do end"))
	if err == nil || err.Error() != "spin.lua: the script ran past 300ms and was stopped" || time.Since(start) > 2*time.Second {
		t.Fatalf("a runaway load: %v after %v", err, time.Since(start))
	}
}

// EditorDocs lists every method the editor object has, each once, and TextDocs every text
// function: the reference made from them cannot leave one out.
func TestTheDocsListTheAPI(t *testing.T) {
	e, core, said := engine(t, "")
	got := probe(t, e, core, said, `
editor.command{ name = "api", run = function(ed)
  local names = {}
  for name in pairs(getmetatable(ed).__index) do names[#names + 1] = "ed:" .. name end
  for name in pairs(editor.text) do names[#names + 1] = "editor.text." .. name end
  table.sort(names)
  editor.notify(table.concat(names, " "))
end }`, "api")
	var docs []string
	for _, d := range append(EditorDocs(), TextDocs("editor")...) {
		name, _, _ := strings.Cut(d.Call, "(")
		if d.Returns == "" || d.Notes == "" {
			t.Errorf("%s is undocumented", d.Call)
		}
		docs = append(docs, name)
	}
	if len(docs) != len(strings.Fields(got[0])) {
		t.Fatalf("the docs list %d, the engine has %d: %v / %s", len(docs), len(strings.Fields(got[0])), docs, got[0])
	}
	for _, n := range docs {
		if !strings.Contains(" "+got[0]+" ", " "+n+" ") {
			t.Errorf("the docs list %s, which the engine lacks", n)
		}
	}
}
