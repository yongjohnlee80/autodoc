package tui

import (
	"regexp"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// TestEveryLeaderKeyHasAGroupOfItsCard: a key SPC's card lists is under one of the card's own
// headings, so a new key cannot fall under MORE unnoticed.
func TestEveryLeaderKeyHasAGroupOfItsCard(t *testing.T) {
	k := keyCatalog(nil)
	c, _ := k.card(leader)
	headings := slices.Concat(c.Groups...)
	for _, k := range k.on(leader) {
		if k.Does != "" && !slices.Contains(headings, k.Group) {
			t.Errorf("SPC %s (%s) is in the group %q, which SPC's card does not head", k.Show, k.Does, k.Group)
		}
	}
}

// TestTheLeaderCardsGroupsAlign: each group's keys are under its heading, in the catalog's order,
// starting in its column; a column's groups are in order, a blank row between two; and the card
// fits the smallest screen, 80 × 20 less its frame.
func TestTheLeaderCardsGroupsAlign(t *testing.T) {
	k := keyCatalog(nil)
	c, _ := k.card(leader)
	text := k.cardState()[textSource(leader)].(string)
	lines := strings.Split(text, "\n")
	if len(lines) > 16 {
		t.Errorf("SPC's card is %d rows, more than the 16 the screen leaves it:\n%s", len(lines), text)
	}
	for _, l := range lines {
		if n := utf8.RuneCountInString(l); n > 76 {
			t.Errorf("a row of SPC's card is %d cells, more than the 76 the screen leaves it: %q", n, l)
		}
	}
	under := map[string][]string{}
	for _, key := range k.on(leader) {
		if key.Does != "" {
			under[key.Group] = append(under[key.Group], key.Show+"  "+key.Does)
		}
	}
	if len(c.Groups) == 0 {
		t.Fatal("SPC's card has no groups")
	}
	prev := -1
	for _, col := range c.Groups {
		at, end := -1, -1
		for _, g := range col {
			row, c := headingAt(t, lines, g)
			if at >= 0 && c != at {
				t.Errorf("%s starts at column %d, the column's first heading at %d", g, c, at)
			}
			if at < 0 && c <= prev {
				t.Errorf("%s starts at column %d, not right of the column before it (%d)", g, c, prev)
			}
			if end >= 0 && (row != end+2 || cellAt(lines, end+1, c) != "") {
				t.Errorf("%s is at row %d, not after a blank row below the group above (its last row %d)", g, row, end)
			}
			at = c
			for i, want := range under[g] {
				if got := cellAt(lines, row+1+i, c); got != want {
					t.Errorf("row %d under %s holds %q, want %q", row+1+i, g, got, want)
				}
			}
			end = row + len(under[g])
		}
		prev = at
	}
}

// headingAt is the row and column where heading starts a cell of lines.
func headingAt(t *testing.T, lines []string, heading string) (row, col int) {
	t.Helper()
	re := regexp.MustCompile(`(^|  )` + regexp.QuoteMeta(heading) + `(  |$)`)
	for r, l := range lines {
		if m := re.FindStringSubmatchIndex(l); m != nil {
			return r, utf8.RuneCountInString(l[:m[3]])
		}
	}
	t.Fatalf("no heading %s in:\n%s", heading, strings.Join(lines, "\n"))
	return 0, 0
}

// cellAt is the command starting at column col of lines' row: "" for none, or for a row past the
// card's last.
func cellAt(lines []string, row, col int) string {
	if row >= len(lines) {
		return ""
	}
	rs := []rune(lines[row])
	if col >= len(rs) || rs[col] == ' ' {
		return ""
	}
	// a key and what it does are two cells apart, and the next column at least two after
	key, rest, ok := strings.Cut(string(rs[col:]), "  ")
	if !ok {
		return key
	}
	does, _, _ := strings.Cut(rest, "  ")
	return key + "  " + does
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
		"file.open":     "file.open — · SPC o · open a document",
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
