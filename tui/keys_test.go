package tui

import (
	"flag"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	"github.com/yongjohnlee80/golib/tui/widget"
)

var updateKeys = flag.Bool("update", false, "rewrite the files generated from the key catalog (keys.go)")

// TestKeysAreGenerated: main.qml's Window keys and its cards' declarations, and each card's
// views/<View>.qml, are the catalog's, the features' keys and cards included; -update rewrites them.
func TestKeysAreGenerated(t *testing.T) {
	h := newHost(NewSession("unused", nil), Options{})
	fs := h.features()
	if _, err := newRegistry(fs...); err != nil { // checks the keys, against the catalog
		t.Fatal(err)
	}
	k := keyCatalog(fs)
	mainQML, err := os.ReadFile("qml/main.qml")
	if err != nil {
		t.Fatal(err)
	}
	wantMain, err := k.withGenerated(string(mainQML))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"qml/main.qml": wantMain}
	for _, c := range k.cards {
		files["qml/views/"+c.View+".qml"] = cardQML(c, k.on(c.ID))
	}
	for path, want := range files {
		if *updateKeys {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != want {
			t.Errorf("%s is not the key catalog's: go test ./tui -run TestKeysAreGenerated -update", path)
		}
	}
}

// Every Window key is named in the help, as help names it; every card key is on its card, and
// every card is in the help under its heading.
func TestEveryKeyIsInTheHelp(t *testing.T) {
	k := keyCatalog(nil)
	help := helpText(k, nil, nil)
	for _, key := range k.on("") {
		if !strings.Contains(help, key.Show) {
			t.Errorf("the Window's %s (%s) is not in the help", key.Seq, key.Show)
		}
	}
	for _, c := range k.cards {
		text := cardText(c.Groups, k.on(c.ID))
		for _, key := range k.on(c.ID) {
			if !strings.Contains(text, key.Show+"  "+key.Does) {
				t.Errorf("%s is not on the card %s", key.Show, c.ID)
			}
		}
		if !strings.Contains(help, c.Help+"\n  "+strings.ReplaceAll(text, "\n", "\n  ")) {
			t.Errorf("the help does not hold the card %s", c.ID)
		}
	}
}

// A card a key opens: its own file and text, its keys closing it before they run, Backspace back
// to the card it came from, and its keys in the help.
func TestACardOpensFromAKey(t *testing.T) {
	agents := &keyedFake{fake: fake{name: "agents"},
		cards: []Card{{ID: "agentsCard", View: "AgentsCard", Title: "SPC a — agents", Back: leader, Help: "SPC a — AGENTS"}},
		keys: []Key{
			{Card: leader, Seq: "Shift+B", Show: "B", Does: "agents…", Opens: "agentsCard"},
			{Card: "agentsCard", Seq: "b", Show: "b", Does: "send the buffer", Run: `App.runCommand("agent.buffer")`},
			{Card: "agentsCard", Seq: "f", Show: "f", Does: "send the selection", Run: `App.runCommand("agent.selection")`},
		}}
	k := keyCatalog([]Feature{agents})
	if err := k.check(mainIDs, nil); err != nil {
		t.Fatal(err)
	}
	c, _ := k.card("agentsCard")
	qml := cardQML(c, k.on("agentsCard"))
	for _, want := range []string{
		"id: agentsCard",
		`title: "SPC a — agents"`,
		"text: App.agentsCardText",
		`Shortcut { sequence: "Backspace"; onActivated: { agentsCard.close(); leader.open() } }`,
		`Shortcut { sequence: "b"; onActivated: { agentsCard.close(); App.runCommand("agent.buffer") } }`,
		"Backspace goes back",
	} {
		if !strings.Contains(qml, want) {
			t.Errorf("the card's QML lacks %q:\n%s", want, qml)
		}
	}
	if lq, _ := k.card(leader); !strings.Contains(cardQML(lq, k.on(leader)), `Shortcut { sequence: "Shift+B"; onActivated: { leader.close(); agentsCard.open() } }`) {
		t.Error("SPC B does not close SPC's card and open the agents'")
	}
	if got := k.cardState()["App.agentsCardText"]; got != "b  send the buffer  f  send the selection" {
		t.Errorf("the card's text: %q", got)
	}
	main, err := k.withGenerated(keysBegin + keysEnd + cardsBegin + cardsEnd + scopeBlocks())
	if err != nil || !strings.Contains(main, "    AgentsCard { id: agentsCard }\n") || !strings.Contains(main, "    Leader { id: leader }\n") {
		t.Errorf("the cards' declarations: %v\n%s", err, main)
	}
	if !strings.Contains(helpText(k, nil, nil), "SPC a — AGENTS\n  b  send the buffer") {
		t.Error("the help lacks the agents card")
	}
	if _, err := keyCatalog(nil).withGenerated("no markers"); err == nil {
		t.Error("a main.qml without the markers was rewritten")
	}
	// a title is a QML string, quoted whatever it holds
	if q := cardQML(Card{ID: "x", View: "X", Title: `say "hi" \ bye`}, nil); !strings.Contains(q, `title: "say \"hi\" \\ bye"`) {
		t.Errorf("the title is not quoted:\n%s", q)
	}
}

// A feature's key on SPC's card is under the group it names, the agents' under AGENTS after the
// host's two; one naming a group the card lacks, or none, is under MORE, last in the last column,
// and a card without groups lists its keys two a row, as ever.
func TestAFeaturesKeyIsUnderItsGroupOrMore(t *testing.T) {
	f := &keyedFake{fake: fake{name: "zz"},
		cards: []Card{{ID: "zzCard", View: "ZzCard", Title: "SPC Z", Back: leader, Help: "SPC Z — ZZ"}},
		keys: []Key{
			{Card: leader, Seq: "a", Show: "a", Does: "agents…", Group: groupAgents, Opens: "zzCard"},
			{Card: leader, Seq: "Shift+Z", Show: "Z", Does: "zz's own", Group: "ZZ", Run: "App.x()"},
			{Card: leader, Seq: "Shift+Y", Show: "Y", Does: "no group", Run: "App.y()"},
			{Card: leader, Seq: "Shift+X", Show: "X", Group: "ZZ", Run: "App.z()"}, // listed nowhere: no Does
			{Card: "zzCard", Seq: "b", Show: "b", Does: "send the buffer", Run: "App.b()"},
			{Card: "zzCard", Seq: "f", Show: "f", Does: "forward", Run: "App.f()"},
			{Card: "zzCard", Seq: "Shift+F", Show: "F", Does: "forward all", Group: groupAgents, Run: "App.g()"},
		}}
	k := keyCatalog([]Feature{f})
	if err := k.check(mainIDs, nil); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(k.cardState()[textSource(leader)].(string), "\n")
	row, col := headingAt(t, lines, groupAgents)
	if got := cellAt(lines, row+3, col); got != "a  agents…" {
		t.Errorf("the agents' key is %q under AGENTS, after its two:\n%s", got, strings.Join(lines, "\n"))
	}
	last := leaderGroups[len(leaderGroups)-1]
	lastRow, lastCol := headingAt(t, lines, last[len(last)-1])
	row, col = headingAt(t, lines, groupMore)
	if col != lastCol || row <= lastRow {
		t.Errorf("MORE is at row %d column %d, not under %s (row %d column %d)", row, col, last[len(last)-1], lastRow, lastCol)
	}
	for i, want := range []string{"Z  zz's own", "Y  no group", ""} {
		if got := cellAt(lines, row+1+i, col); got != want {
			t.Errorf("row %d under MORE holds %q, want %q", i, got, want)
		}
	}
	if text := strings.Join(lines, "\n"); strings.Contains(text, "ZZ") || strings.Contains(text, "X  ") {
		t.Errorf("a group the card lacks is headed, or a key without Does listed:\n%s", text)
	}
	if got := k.cardState()[textSource("zzCard")]; got != "b  send the buffer  f  forward\nF  forward all" {
		t.Errorf("the card without groups: %q", got)
	}
}

// The registry refuses a sequence two keys of one place take, a card no key opens or declared twice,
// and a key on or opening a card nobody declared.
func TestTheRegistryRefusesKeysThatDoNotFit(t *testing.T) {
	for want, f := range map[string]*keyedFake{
		"the card leader's x is taken twice": {keys: []Key{{Card: leader, Seq: "x", Show: "x", Does: "mine", Run: "App.x()"}}},
		"the Window's Ctrl+S is taken twice": {keys: []Key{{Seq: "F2", Also: []string{"Ctrl+S"}, Run: "App.x()"}}},
		"no key opens the card lonely":       {cards: []Card{{ID: "lonely", View: "Lonely"}}},
		"the card leader is declared twice":  {cards: []Card{{ID: leader, View: "Other"}}, keys: []Key{{Seq: "F3", Opens: leader}}},
		"which is not declared":              {keys: []Key{{Card: "nowhere", Seq: "z", Run: "App.x()"}}},
		"opens the card gone, which is not":  {keys: []Key{{Card: leader, Seq: "Shift+Z", Opens: "gone"}}},
		// what every card answers itself: q and Escape close it, Backspace goes back
		"the card leader's q is taken twice":      {keys: []Key{{Card: leader, Seq: "q", Run: "App.x()"}}},
		"the card leader's Escape is taken twice": {keys: []Key{{Card: leader, Seq: "F4", Also: []string{"Escape"}, Run: "App.x()"}}},
		"the card subCard's Backspace is taken twice": {cards: []Card{{ID: "subCard", View: "SubCard", Back: leader}},
			keys: []Key{{Card: leader, Seq: "Shift+Y", Opens: "subCard"}, {Card: "subCard", Seq: "Backspace", Run: "App.x()"}}},
		// a card's ID and View are QML names, and the ID is no other of main.qml's
		"needs a lower-camel ID":           {cards: []Card{{ID: "agents-card", View: "AgentsCard"}}, keys: []Key{{Seq: "F5", Opens: "agents-card"}}},
		"and an upper-camel View":          {cards: []Card{{ID: "agentsCard", View: "agentsCard"}}, keys: []Key{{Seq: "F5", Opens: "agentsCard"}}},
		"takes an id main.qml already has": {cards: []Card{{ID: "help", View: "HelpCard"}}, keys: []Key{{Seq: "F5", Opens: "help"}}},
		// Backspace goes where the card came from
		"whose Backspace goes back to the card leader's": {cards: []Card{{ID: "subCard", View: "SubCard", Back: leader}},
			keys: []Key{{Seq: "F6", Opens: "subCard"}}},
		"whose Backspace goes back to the Window's": {cards: []Card{{ID: "subCard", View: "SubCard"}},
			keys: []Key{{Card: leader, Seq: "Shift+Y", Opens: "subCard"}}},
		"goes back to the card missing, which is not declared": {cards: []Card{{ID: "subCard", View: "SubCard", Back: "missing"}},
			keys: []Key{{Card: leader, Seq: "Shift+Y", Opens: "subCard"}}},
		"subCard's Backspace is taken twice": {cards: []Card{{ID: "subCard", View: "SubCard", Back: leader}},
			keys: []Key{{Card: leader, Seq: "Shift+Y", Opens: "subCard"}, {Card: "subCard", Seq: "z", Also: []string{"Backspace"}, Run: "App.x()"}}},
		// a key runs exactly one of Run, Opens and Cmd; Cmd a built-in's or a feature's entry
		"the card leader's Shift+Z runs 2 of Run, Opens and Cmd":                                 {keys: []Key{{Card: leader, Seq: "Shift+Z", Run: "App.x()", Cmd: "b.hello"}}},
		"the card leader's Shift+Z runs 0 of Run, Opens and Cmd":                                 {keys: []Key{{Card: leader, Seq: "Shift+Z", Show: "Z"}}},
		"the card leader's Shift+Z runs no.such, which no command is":                            {keys: []Key{{Card: leader, Seq: "Shift+Z", Cmd: "no.such"}}},
		"the card leader's Shift+Z runs lua.upper: a key runs a built-in or a feature's command": {keys: []Key{{Card: leader, Seq: "Shift+Z", Cmd: "lua.upper"}}},
		"the Window's F7 runs plugin.tetris.new: a key runs a built-in":                          {keys: []Key{{Seq: "F7", Cmd: "plugin.tetris.new"}}},
	} {
		f.fake = fake{name: "b"}
		if _, err := newRegistry(f); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("want %q: %v", want, err)
		}
	}
	if _, err := newRegistry(&keyedFake{fake: fake{name: "b"}, keys: []Key{{Card: leader, Seq: "Shift+X", Show: "X", Does: "mine", Run: "App.x()"}}}); err != nil {
		t.Errorf("a key of a feature's own: %v", err)
	}
}

type keyedFake struct {
	fake
	keys  []Key
	cards []Card
}

func (f *keyedFake) Keys() []Key   { return f.keys }
func (f *keyedFake) Cards() []Card { return f.cards }

// keyedCatFake is a feature with keys and catalog entries: its card's keys run its entries by id.
type keyedCatFake struct {
	keyedFake
	entries []Entry
}

func (f *keyedCatFake) Catalog() []Entry { return f.entries }

// TestAKeyRunsACatalogEntryByItsID (ADR 1791762474 §4.3, §5.4): a feature's key with Cmd runs
// App.run with the id quoted, and its card names it by the entry's title when the key does not.
func TestAKeyRunsACatalogEntryByItsID(t *testing.T) {
	f := &keyedCatFake{keyedFake: keyedFake{fake: fake{name: "zz"},
		cards: []Card{{ID: "zzCard", View: "ZzCard", Title: "SPC Z", Back: leader, Help: "SPC Z — ZZ"}},
		keys: []Key{
			{Card: leader, Seq: "Shift+Z", Show: "Z", Does: "zz…", Opens: "zzCard"},
			{Card: "zzCard", Seq: "b", Show: "b", Cmd: "zz.send_buffer"},
			{Card: "zzCard", Seq: "f", Show: "f", Does: "its own words", Cmd: "zz.send_selection"},
		}},
		entries: []Entry{{ID: "zz.send_buffer", Title: "send the buffer", Run: noRun}, {ID: "zz.send_selection", Title: "send the selection", Run: noRun}}}
	if _, err := newRegistry(f); err != nil {
		t.Fatal(err)
	}
	k := keyCatalog([]Feature{f})
	if got := k.cardState()["App.zzCardText"]; got != "b  send the buffer  f  its own words" {
		t.Errorf("the card's text: %q", got)
	}
	c, _ := k.card("zzCard")
	if q := cardQML(c, k.on("zzCard")); !strings.Contains(q, `Shortcut { sequence: "b"; onActivated: { zzCard.close(); App.run("zz.send_buffer") } }`) {
		t.Errorf("the card's QML does not run the entry:\n%s", q)
	}
	if got := shortcuts([]Key{{Seq: "F9", Cmd: `a.b"c`}}, "", ""); got != `Shortcut { sequence: "F9"; onActivated: App.run("a.b\"c") }`+"\n" {
		t.Errorf("an id is a QML string, quoted: %q", got)
	}
}

// TestAFeaturesKeyRunsItsEntryInTheProgram (ADR 1791762474 §5.4): in the running program, a
// feature's Window key with Cmd runs its entry, through App.run, on the attached editor; App.run of
// an id no entry has says so.
func TestAFeaturesKeyRunsItsEntryInTheProgram(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	ran := make(chan *widget.EditorCore, 1)
	f := &keyedCatFake{keyedFake: keyedFake{fake: fake{name: "zz"}, keys: []Key{{Seq: "F12", Show: "F12", Cmd: "zz.mark"}}},
		entries: []Entry{{ID: "zz.mark", Title: "mark", Run: func(inv Invocation) error { ran <- inv.Core; return nil }}}}
	h := newHost(NewSession(d.sock, nil), Options{})
	h.moreFeatures = []Feature{f}
	main, err := keyCatalog(h.features()).withGenerated(string(layout))
	if err != nil {
		t.Fatal(err)
	}
	opt := Options{Layout: []byte(main)}
	s := decltest.RunWith(t, 100, 30, h.attach, h.options(opt)...)
	t.Cleanup(h.cancel)
	r := &running{h: h, s: s}
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF12})
	select {
	case core := <-ran:
		if core != onLoop(r, func() *widget.EditorCore { return r.h.core }) {
			t.Fatal("the entry ran on another core than the attached editor's")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("F12 did not run zz.mark")
	}
	onLoop(r, func() error {
		return r.h.reg.handlers["App.run"]([]qml.SpecValue{{Kind: qml.SpecValueString, Raw: "no.such"}})
	})
	r.waitNotice(t, "unknown: no.such")
}

// scopeBlocks are every pane's generated block, empty, as main.qml holds them.
func scopeBlocks() string {
	var b strings.Builder
	for _, s := range keyScopes {
		begin, end := scopeMarkers(s)
		b.WriteString(begin + end)
	}
	return b.String()
}

// TestAPanesKeysAreItsOwn: a key scoped to a pane is generated into that pane's FocusScope and not
// the Window's; / n N stay the Window's too, for where find does not reach, while d is the
// relations' alone. The check refuses a scoped key on a card, one of a pane main.qml does not
// scope, and a sequence a pane takes twice.
func TestAPanesKeysAreItsOwn(t *testing.T) {
	fs := newHost(NewSession("unused", nil), Options{}).features()
	k := keyCatalog(fs) // the find panes give the scopes their / n N
	finds := map[string]bool{}
	for _, f := range fs {
		if p, ok := f.(Findable); ok {
			finds[p.FindPane().Scope] = true
		}
	}
	if err := k.check(mainIDs, nil); err != nil {
		t.Fatal(err)
	}
	main, err := k.withGenerated(keysBegin + keysEnd + cardsBegin + cardsEnd + scopeBlocks())
	if err != nil {
		t.Fatal(err)
	}
	block := func(begin, end string) string {
		return main[strings.Index(main, begin)+len(begin) : strings.Index(main, end)]
	}
	window := block(keysBegin, keysEnd)
	for _, seq := range []string{"/", "n", "Shift+N"} {
		if !strings.Contains(window, `sequence: "`+seq+`"`) {
			t.Errorf("the Window lost %s, its fallback where find does not reach", seq)
		}
	}
	if strings.Contains(window, `sequence: "d"`) {
		t.Error("d is still the Window's")
	}
	for _, s := range keyScopes {
		begin, end := scopeMarkers(s)
		got := block(begin, end)
		if has := strings.Contains(got, s.Indent+`Shortcut { sequence: "/"; onActivated: App.openFind() }`); has != finds[s.ID] {
			t.Errorf("the %s's keys hold /: %v, but a find pane scoped there: %v\n%s", s.ID, has, finds[s.ID], got)
		}
		if hasD := strings.Contains(got, `sequence: "d"`); hasD != (s.ID == "links") {
			t.Errorf("the %s's keys hold d: %v", s.ID, hasD)
		}
	}
	for want, key := range map[string]Key{
		"a card's keys are the card's":      {Card: leader, Scope: "page", Seq: "x", Run: "App.x()"},
		"which main.qml does not scope":     {Scope: "nowhere", Seq: "x", Run: "App.x()"},
		"the pane links's d is taken twice": {Scope: "links", Seq: "d", Run: "App.x()"},
	} {
		bad := keyCatalog(nil)
		bad.keys = append(bad.keys, key)
		if err := bad.check(mainIDs, nil); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%+v: err = %v, want %q", key, err, want)
		}
	}
}
