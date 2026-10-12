package tui

import (
	"errors"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// catFake is a feature with catalog entries.
type catFake struct {
	fake
	entries []Entry
}

func (f *catFake) Catalog() []Entry { return f.entries }

// ran is an entry's Run that writes down each invocation into got.
func ran(got *[]string, name string) func(Invocation) error {
	return func(inv Invocation) error {
		*got = append(*got, name+" "+strings.Join(inv.Args, ",")+" |"+inv.Line+"|"+map[bool]string{true: "!"}[inv.Bang])
		return nil
	}
}

func noRun(Invocation) error { return nil }

// TestTheRegistryMergesCatalogEntries: a feature's entries are in the registry's catalog, under
// its name and as a feature's; the host's own (legacy) are built-in.
func TestTheRegistryMergesCatalogEntries(t *testing.T) {
	a := &catFake{fake: fake{name: "a"}, entries: []Entry{{ID: "a.one", Title: "one", Verb: "one", Run: noRun}}}
	r, err := newRegistry(a)
	if err != nil {
		t.Fatal(err)
	}
	e := r.catalog.lookup("a.one")
	if e == nil || e.Source != SourceFeature || e.Owner != "a" || r.catalog.names["one"] != "a.one" {
		t.Fatalf("a's entry: %+v, names %v", e, r.catalog.names)
	}
	h := newHost(NewSession("unused", nil), Options{})
	reg, err := newRegistry(h.features()...)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range reg.catalog.entries() {
		if e.Owner == "legacy" && e.Source != SourceBuiltin {
			t.Errorf("legacy's %s is %s, want builtin", e.ID, e.Source)
		}
	}
}

// TestTheCatalogRefusesWhatItCannotHold (ADR 1791762474 §5.2, §5.9, §5.10): a duplicate id, naming
// both owners; a verb golib's or another entry's; an id outside its namespace; each refused at the
// registry's build, so the program does not start.
func TestTheCatalogRefusesWhatItCannotHold(t *testing.T) {
	first := Entry{ID: "a.one", Verb: "one", Aliases: []string{"uno"}, Run: noRun}
	for want, e := range map[string]Entry{
		"the command a.one is both a's and b's":               {ID: "a.one", Run: noRun},
		":one is both a's (a.one) and b's (b.two)":            {ID: "b.two", Verb: "one", Run: noRun},
		":uno is both a's (a.one) and b's (b.two)":            {ID: "b.two", Verb: "dos", Aliases: []string{"uno"}, Run: noRun},
		"the command b.w: :w is the editor's own":             {ID: "b.w", Verb: "w", Run: noRun},
		"the command b.write: :write is the editor's own":     {ID: "b.write", Verb: "save", Aliases: []string{"write"}, Run: noRun},
		"the command b.find: :find is the editor's own":       {ID: "b.find", Verb: "find", Run: noRun},
		"the command lua.x: lua.* is Lua's":                   {ID: "lua.x", Run: noRun},
		"the command plugin.p.x: lua.* is Lua's":              {ID: "plugin.p.x", Run: noRun},
		`the command id "Bad": want dotted lower-case words`:  {ID: "Bad", Run: noRun},
		`the command id "nodot": want dotted lower-case`:      {ID: "nodot", Run: noRun},
		"the command b.two has nothing to run":                {ID: "b.two"},
		"the command b.two has aliases and no verb":           {ID: "b.two", Aliases: []string{"x"}, Run: noRun},
		`the command b.two: "12" is not a ':' name`:           {ID: "b.two", Verb: "12", Run: noRun},
		`the command b.two: "dos" is not a ':' name it can`:   {ID: "b.two", Verb: "dos", Aliases: []string{"dos"}, Run: noRun},
		`the command b.two: "two words" is not a ':' name it`: {ID: "b.two", Verb: "two words", Run: noRun},
	} {
		a := &catFake{fake: fake{name: "a"}, entries: []Entry{first}}
		b := &catFake{fake: fake{name: "b"}, entries: []Entry{e}}
		if _, err := newRegistry(a, b); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q: %v", want, err)
		}
	}
	// AutoDoc's own page find takes golib's find and nohl over; nothing else may
	c := newCatalog()
	if err := c.add(Entry{ID: "find.term", Source: SourceBuiltin, Verb: "find", Run: noRun}); err != nil {
		t.Errorf("the built-in find: %v", err)
	}
	if err := c.add(Entry{ID: "x.quit", Source: SourceBuiltin, Verb: "q", Run: noRun}); err == nil {
		t.Error("a built-in took golib's q")
	}
	// a refusal names whose the name is: AutoDoc's, or a Lua file's
	_ = c.add(Entry{ID: "lua.zz", Source: SourceLua, Owner: "z.lua", Verb: "zz", Run: noRun})
	_ = c.add(Entry{ID: "terminal.open", Source: SourceBuiltin, Verb: "terminal", Run: noRun})
	for want, e := range map[string]Entry{
		":terminal is both AutoDoc's (terminal.open) and b's (b.t)": {ID: "b.t", Source: SourceFeature, Owner: "b", Verb: "dig", Aliases: []string{"terminal"}, Run: noRun},
		":zz is both Lua's z.lua (lua.zz) and b's (b.zz)":           {ID: "b.zz", Source: SourceFeature, Owner: "b", Verb: "zz", Run: noRun},
	} {
		if err := c.add(e); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q: %v", want, err)
		}
	}
	// what a catalog with nothing, or no entry, answers
	var none *catalog
	none.attach(widget.NewEditorCore())
	if none.lookup("find.term") != nil || c.owned("no.such") != nil {
		t.Error("an absent catalog or entry answered")
	}
}

// TestTheCatalogsNamespacesAreOwned (§5.10): lua.* is Lua's alone, plugin.<name>.* that plugin's.
func TestTheCatalogsNamespacesAreOwned(t *testing.T) {
	c := newCatalog()
	for want, e := range map[string]Entry{
		"a Lua id is lua.<name>":                             {ID: "file.open", Source: SourceLua, Owner: "a.lua", Run: noRun},
		"the plugin mine's ids are plugin.mine.<id>":         {ID: "plugin.other.x", Source: SourcePlugin, Owner: "mine", Run: noRun},
		"the plugin mine's ids are plugin.mine.<id>; prefix": {ID: "plugin.mineral.x", Source: SourcePlugin, Owner: "mine", Run: noRun},
		"a plugin's command has no ':' name":                 {ID: "plugin.mine.x", Source: SourcePlugin, Owner: "mine", Verb: "x", Run: noRun},
	} {
		want, _, _ = strings.Cut(want, ";")
		if err := c.add(e); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: want %q: %v", e.ID, want, err)
		}
	}
	if len(c.order) != 0 {
		t.Fatalf("refused entries were added: %v", c.order)
	}
	if err := c.add(Entry{ID: "plugin.mine.x", Source: SourcePlugin, Owner: "mine", Run: noRun}); err != nil {
		t.Error(err)
	}
	if err := c.add(Entry{ID: "plugin.mine.x", Source: SourcePlugin, Owner: "mine", Run: noRun}); err == nil {
		t.Error("a plugin's id added twice")
	}
}

// TestARefusedEntryLeavesNothingBehind (§5.10): an alias collision refuses the whole entry, its id
// and verb included; a Lua entry replacing its own id that is refused keeps the one it would have
// replaced.
func TestARefusedEntryLeavesNothingBehind(t *testing.T) {
	c := newCatalog()
	core := widget.NewEditorCore()
	c.attach(core)
	_ = c.add(Entry{ID: "a.one", Source: SourceFeature, Owner: "a", Verb: "one", Run: noRun})
	if err := c.add(Entry{ID: "b.two", Source: SourceFeature, Owner: "b", Verb: "two", Aliases: []string{"one"}, Run: noRun}); err == nil {
		t.Fatal("an alias another entry owns was taken")
	}
	if c.lookup("b.two") != nil || c.names["two"] != "" || slices.Contains(c.order, "b.two") {
		t.Fatalf("a refused entry was half-registered: %v %v", c.names, c.order)
	}
	if _, ok := core.Commands().Lookup("two"); ok {
		t.Fatal(":two reached the command line")
	}
	var got []string
	if err := c.add(Entry{ID: "lua.up", Source: SourceLua, Owner: "a.lua", Verb: "up", Run: ran(&got, "old")}); err != nil {
		t.Fatal(err)
	}
	if err := c.add(Entry{ID: "lua.up", Source: SourceLua, Owner: "b.lua", Verb: "up", Aliases: []string{"9"}, Run: noRun}); err == nil {
		t.Fatal("a bad alias was taken")
	}
	if err := core.RunCommand("up"); err != nil || len(got) != 1 {
		t.Fatalf(":up after a refused replacement: %v, ran %q", err, got)
	}
	if err := c.add(Entry{ID: "lua.up", Source: SourceLua, Owner: "b.lua", Verb: "upper", Run: ran(&got, "new")}); err != nil {
		t.Fatal(err)
	}
	if _, ok := core.Commands().Lookup("up"); ok || c.names["up"] != "" {
		t.Fatal("the replaced Lua command's name stayed")
	}
	if err := core.RunCommand("upper"); err != nil || got[len(got)-1] != "new  ||" {
		t.Fatalf(":upper: %v, ran %q", err, got)
	}
}

// TestALuaCommandTakesOneNameOnly (ADR 1791762474 §4.2, r2): a Lua command whose name another
// entry has takes that name alone; the other keeps its id and its other names, and gets the name
// back when the Lua command goes. A Lua command takes golib's verbs as it always could.
func TestALuaCommandTakesOneNameOnly(t *testing.T) {
	c := newCatalog()
	core := widget.NewEditorCore()
	c.attach(core)
	var got []string
	if err := c.add(Entry{ID: "terminal.open", Source: SourceBuiltin, Verb: "terminal", Aliases: []string{"term"}, Help: "the terminal", Run: ran(&got, "terminal")}); err != nil {
		t.Fatal(err)
	}
	if err := c.add(Entry{ID: "lua.term", Source: SourceLua, Owner: "t.lua", Verb: "term", Help: "mine", Run: ran(&got, "lua")}); err != nil {
		t.Fatal(err)
	}
	if err := c.add(Entry{ID: "lua.w", Source: SourceLua, Owner: "t.lua", Verb: "w", Run: ran(&got, "luaw")}); err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"terminal", "term a b", "w! x"} {
		if err := core.RunCommand(line); err != nil {
			t.Fatalf(":%s: %v", line, err)
		}
	}
	if want := []string{"terminal  ||", "lua a,b |a b|", "luaw x |x|!"}; !slices.Equal(got, want) {
		t.Fatalf("ran %q, want %q", got, want)
	}
	if !slices.Equal(c.owned("terminal.open"), []string{"terminal"}) || c.names["term"] != "lua.term" {
		t.Fatalf("names %v", c.names)
	}
	if cmd, _ := core.Commands().Lookup("terminal"); len(cmd.Aliases) != 0 || cmd.Help != "the terminal" {
		t.Fatalf(":terminal is registered as %+v", cmd)
	}
	c.remove("lua.term")
	c.remove("lua.term") // gone already: nothing
	if cmd, ok := core.Commands().Lookup("term"); !ok || cmd.Name != "terminal" {
		t.Fatalf(":term after the Lua command went: %+v %v", cmd, ok)
	}
	// run, as a key or autodoc.run does: the attached core, the line the arguments joined
	got = nil
	if err := c.run("terminal.open", []string{"two", "words"}); err != nil || !slices.Equal(got, []string{"terminal two,words |two words|"}) {
		t.Fatalf("run: %v, %q", err, got)
	}
	var core2 *widget.EditorCore
	_ = c.add(Entry{ID: "a.core", Source: SourceFeature, Owner: "a", Run: func(inv Invocation) error { core2 = inv.Core; return nil }})
	if err := c.run("a.core", nil); err != nil || core2 != core {
		t.Fatalf("a run outside ':' got the core %p, want the attached %p", core2, core)
	}
	if err := c.run("no.such", nil); !errors.Is(err, errUnknownCommand) || err.Error() != "unknown: no.such" {
		t.Fatalf("an unknown id: %v", err)
	}
}

// noArgCall is QML that invokes a command with no arguments: App.save(), help.open().
var noArgCall = regexp.MustCompile(`^(App\.\w+|\w+\.open)\(\)$`)

// TestEveryMenuRowAndLeaderKeyIsACatalogEntry (ADR 1791762474 §4.1): what a menu row or a SPC key
// runs, with no arguments, is a catalog entry's twin, and each twin is a command the document can
// invoke: so the catalog holds every user command, and each entry runs what its row runs.
func TestEveryMenuRowAndLeaderKeyIsACatalogEntry(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	reg, err := newRegistry(h.features()...)
	if err != nil {
		t.Fatal(err)
	}
	twins := map[string]string{}
	for _, e := range reg.catalog.entries() {
		if e.Twin == "" {
			continue
		}
		twins[e.Twin] = e.ID
		name, _, _ := strings.Cut(e.Twin, "(")
		if obj, ok := strings.CutSuffix(name, ".open"); ok && !strings.HasPrefix(name, "App.") {
			if !mainIDs[obj] && !slices.ContainsFunc(cards, func(c Card) bool { return c.ID == obj }) {
				t.Errorf("%s's twin %s opens no dialog main.qml has", e.ID, e.Twin)
			}
		} else if reg.handlers[name] == nil {
			t.Errorf("%s's twin %s is no command", e.ID, e.Twin)
		}
	}
	src, err := os.ReadFile("qml/main.qml")
	if err != nil {
		t.Fatal(err)
	}
	var runs []string
	for _, m := range regexp.MustCompile(`onTriggered: ([^}]*?)\s*}`).FindAllStringSubmatch(string(src), -1) {
		runs = append(runs, m[1])
	}
	for _, k := range leaderKeys {
		runs = append(runs, k.Run)
	}
	// :nohl's entry also clears the editor's own find; the menu's row clears the page's alone
	exempt := map[string]bool{"App.clearFind()": true}
	for _, run := range runs {
		if noArgCall.MatchString(run) && twins[run] == "" && !exempt[run] {
			t.Errorf("%s runs from a menu row or a SPC key, and no catalog entry is its twin", run)
		}
	}
}

// TestAnEntryRunsWhatItsTwinRuns: run by id, a built-in and a feature's entry do what their menu
// rows do.
func TestAnEntryRunsWhatItsTwinRuns(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	if err := onLoop(r, func() error { return r.h.cat.run("explorer.toggle", nil) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "┌ explorer")
	if err := onLoop(r, func() error { return r.h.cat.run("todos.open", nil) }); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "todos · kb")
}
