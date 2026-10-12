package tui

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// TestTheLeaderCardsColumnsAlign: every row's second command starts in the same column, so its
// key letters line up. The last row may hold one command, when their number is odd.
func TestTheLeaderCardsColumnsAlign(t *testing.T) {
	row := regexp.MustCompile(`^(\S+)  (.+?) {2,}(\S+)  `)
	lone := regexp.MustCompile(`^\S+  \S.*\S$`)
	col := -1
	lines := strings.Split(cardText(leaderKeys), "\n")
	for i, line := range lines {
		m := row.FindStringSubmatchIndex(line)
		if m == nil && i == len(lines)-1 && lone.MatchString(line) && !strings.Contains(line, "   ") {
			continue
		}
		if m == nil {
			t.Fatalf("row %d is not two commands: %q", i, line)
		}
		at := utf8.RuneCountInString(line[:m[6]])
		if col < 0 {
			col = at
		}
		if at != col {
			t.Errorf("row %d's second command starts at column %d, the first row's at %d: %q", i, at, col, line)
		}
	}
}

// TestTheHelpsCommandsAreTheCatalogs (ADR 1791762474 §5.5, §4.2 r2): F1's COMMANDS lists every
// entry under its source, with the ':' names it owns, its keys and its title; the command line and
// its completion resolve each name to that same owner, a Lua command's taken name included.
func TestTheHelpsCommandsAreTheCatalogs(t *testing.T) {
	_, r := luaTUI(t, "a\n", map[string]string{"over.lua": `
autodoc.command{ name = "find", title = "my find", run = function(ed, args, line) ed:insert("F:" .. line) end }
autodoc.command{ name = "term", help = "my term", run = function(ed) ed:insert("T") end }
`})
	r.s.WaitFor(t, "F1 lists the Lua commands", func(string) bool {
		return strings.Contains(onLoop(r, func() string { return r.h.helpNow }), "lua.term")
	})
	help := onLoop(r, func() string { return r.h.helpNow })
	line := func(id string) string {
		for _, l := range strings.Split(help, "\n") {
			if strings.HasPrefix(l, "    "+id+" ") {
				return strings.Join(strings.Fields(l), " ")
			}
		}
		t.Fatalf("F1 lists no %s:\n%s", id, help)
		return ""
	}
	type owner struct{ name, id, help string }
	var owners []owner
	onLoop(r, func() bool {
		for _, e := range r.h.cat.entries() {
			names := r.h.cat.owned(e.ID)
			for i, n := range names {
				owners = append(owners, owner{n, e.ID, e.Help})
				names[i] = ":" + n
			}
			want := e.ID + " " + orDash(strings.Join(names, " ")) + " · "
			if l := line(e.ID); !strings.HasPrefix(l, want) || !strings.HasSuffix(l, " · "+e.Title) {
				t.Errorf("F1's %s is %q, want %q… · %s", e.ID, l, want, e.Title)
			}
		}
		return true
	})
	for _, o := range owners {
		cmd := onLoop(r, func() widget.Command { c, _ := r.h.core.Commands().Lookup(o.name); return c })
		if cmd.Help != o.help || !slices.Contains(append([]string{cmd.Name}, cmd.Aliases...), o.name) {
			t.Errorf(":%s is %+v on the command line, F1 says %s's", o.name, cmd, o.id)
		}
		if cands := onLoop(r, func() []widget.Command { return r.h.core.Commands().Complete(o.name) }); !slices.ContainsFunc(cands, func(c widget.Command) bool { return c.Help == o.help }) {
			t.Errorf("completing :%s does not offer %s's", o.name, o.id)
		}
	}
	for id, want := range map[string]string{
		"lua.find":      "lua.find :find · — · my find",
		"find.term":     "find.term — · — · find a term in the page",
		"lua.term":      "lua.term :term · — · my term",
		"terminal.open": "terminal.open :terminal · — · open the terminal",
		"file.open":     "file.open — · Ctrl+O, SPC o · open a document",
		"help.keys":     "help.keys — · F1, SPC ? · help",
		"todos.open":    "todos.open :todos · SPC x · todos",
	} {
		if got := line(id); got != want {
			t.Errorf("F1's %s: %q, want %q", id, got, want)
		}
	}
	a, b, c := strings.Index(help, "\n  AutoDoc's\n"), strings.Index(help, "\n  the features'\n"), strings.Index(help, "\n  yours, in Lua")
	if a < 0 || b < a || c < b || strings.Index(help, "file.open") > b || strings.Index(help, "todos.open") < b {
		t.Errorf("F1's commands are not grouped by source:\n%s", help)
	}
	r.command(t, "find x")
	r.waitPage(t, "F:x")
}

// TestAKeyOnACardIsNamedFromThePage: F1 names a card's key after the keys that open its card.
func TestAKeyOnACardIsNamedFromThePage(t *testing.T) {
	f := &keyedCatFake{keyedFake: keyedFake{fake: fake{name: "zz"},
		cards: []Card{{ID: "zzCard", View: "ZzCard", Back: leader}},
		keys: []Key{{Card: leader, Seq: "Shift+Z", Show: "Z", Opens: "zzCard"}, {Card: "zzCard", Seq: "b", Show: "b", Cmd: "zz.go"},
			{Seq: "F9", Cmd: "zz.go"}}},
		entries: []Entry{{ID: "zz.go", Title: "go", Run: noRun}}}
	r, err := newRegistry(f)
	if err != nil {
		t.Fatal(err)
	}
	got := keyCatalog([]Feature{f}).helpCommands(r.catalog, map[string][]string{"zz.go": {"SPC p g"}})
	if !strings.Contains(got, "zz.go  — · SPC Z b, F9, SPC p g · go") {
		t.Errorf("F1's line:\n%s", got)
	}
	// a key on a card no key opens (the registry refuses that one) is named as it shows
	if got := (keyset{keys: []Key{{Card: "lone", Seq: "x"}}}).keyName(Key{Card: "lone", Seq: "x"}, 0); got != "x" {
		t.Errorf("a key on a card nothing opens: %q", got)
	}
	if helpText(keyset{}, nil, nil) != helpKeys+"\n\n\n\n"+helpRest {
		t.Error("help without a catalog has a COMMANDS section")
	}
}
