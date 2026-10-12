package tui

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
)

// testPick is a picker over paths, its preview the path's text, recording what it chose, entered,
// submitted and cancelled.
type testPick struct {
	*pick[string]
	c                          *scriptedCore
	chosen, entered, submitted []string
	cancelled, read            int
}

func newTestPick() *testPick {
	c := newScripted("kb")
	t := &testPick{c: c}
	t.pick = &pick[string]{c: c, dialog: "testDialog", name: "test",
		list:   newRows(tuidecl.NewListModel("key", "path"), pathRowOf),
		field:  "App.testField",
		status: "App.testStatus", noun: "files",
		match: func(p string) string { return p },
		preview: func(p string) func(context.Context) answerOf[pane] {
			return func(context.Context) answerOf[pane] {
				t.read++
				return answerOf[pane]{v: pane{title: p, text: "text of " + p, at: 3}}
			}
		},
		empty:  func() pane { return pane{title: "nothing"} },
		scope:  TicketWorkspace,
		choose: func(p string) { t.chosen = append(t.chosen, p) },
		cancel: func() { t.cancelled++ }}
	t.setAll([]string{"Notes/A.md", "b.md", "notes/c.md"})
	return t
}

func (t *testPick) shown() []string { return slices.Clone(t.list.All()) }
func (t *testPick) title() any      { return t.c.set["App.testPreviewTitle"] }

// The filter keeps the items whose text holds it, case and the spaces around it aside, counts them
// in the list's title, and previews the first; no match previews nothing.
func TestAPickFilters(t *testing.T) {
	p := newTestPick()
	p.filter("  NOTES ")
	if got := p.shown(); !slices.Equal(got, []string{"Notes/A.md", "notes/c.md"}) {
		t.Fatalf("the filter kept %v", got)
	}
	if got := p.c.set["App.testStatus"]; got != "files (2 of 3)" {
		t.Errorf("the list's title = %v", got)
	}
	if p.title() != "Notes/A.md" || p.c.set["App.testPreviewText"] != "text of Notes/A.md" || p.c.set["App.testPreviewAt"] != 3 {
		t.Errorf("the first row previewed: %v %v %v", p.title(), p.c.set["App.testPreviewText"], p.c.set["App.testPreviewAt"])
	}
	if rows := p.list.Model().Len(); rows != 2 {
		t.Errorf("the model shows %d rows, want 2", rows)
	}
	p.filter("zzz")
	if got := p.shown(); len(got) != 0 || p.c.set["App.testStatus"] != "files (0 of 3)" || p.title() != "nothing" {
		t.Errorf("no match: rows %v, title %v, preview %v", got, p.c.set["App.testStatus"], p.title())
	}

	// open empties the field, lists every item, and opens the dialog
	p.open()
	if p.c.set["App.testField"] != "" || len(p.shown()) != 3 || !slices.Equal(p.c.opened, []string{"testDialog"}) {
		t.Errorf("open: field %q, rows %v, opened %v", p.c.set["App.testField"], p.shown(), p.c.opened)
	}
}

// The preview shows the latest row asked for: an answer to an earlier one, arriving later, is
// dropped, and so is one still being read when the filter leaves no row or the picker closes.
func TestAPickPreviewsTheLatestOnly(t *testing.T) {
	p := newTestPick()
	p.c.deferred = true
	p.filter("") // asks for row 0
	p.move(2)    // then row 2
	p.c.deliver(1, 0)
	if p.title() != "notes/c.md" {
		t.Fatalf("after the later answer, then the earlier: the preview shows %v, want notes/c.md", p.title())
	}

	p.move(1)       // b.md, still being read …
	p.filter("zzz") // … when nothing matches
	p.c.deliver(0)
	if p.title() != "nothing" {
		t.Errorf("a preview read before the filter emptied the list replaced the empty one: %v", p.title())
	}

	p.filter("")
	p.c.deliver(0)
	p.move(1) // b.md, still being read …
	p.dismiss()
	p.c.deliver(0)
	if p.title() != "Notes/A.md" {
		t.Errorf("a preview read before the picker closed was shown: %v", p.title())
	}
}

// A row from QML may be -1, or one a filter since has taken away: nothing previews, chooses,
// enters or closes.
func TestAPickIgnoresARowItDoesNotShow(t *testing.T) {
	p := newTestPick()
	p.filter("b.md")
	p.read = 0
	for _, i := range []int{-1, 1, 7} {
		p.move(i)
		p.enterRow(i)
		p.accept(i, "")
	}
	if p.read != 0 || len(p.chosen) != 0 || len(p.c.closed) != 0 {
		t.Errorf("rows out of range: %d previews, chose %v, closed %v", p.read, p.chosen, p.c.closed)
	}
}

// Choosing a row closes the picker and takes the item: Enter on the row, or accept with it under
// the cursor. A picker may do its own thing with Enter on a row, or take the field's text on
// accept; dismissing it cancels.
func TestAPickChoosesAndCancels(t *testing.T) {
	p := newTestPick()
	p.filter("")
	p.enterRow(1)
	p.accept(2, "typed")
	if !slices.Equal(p.chosen, []string{"b.md", "notes/c.md"}) || !slices.Equal(p.c.closed, []string{"testDialog", "testDialog"}) {
		t.Fatalf("chose %v, closed %v", p.chosen, p.c.closed)
	}
	p.dismiss()
	if p.cancelled != 1 {
		t.Errorf("dismissed: %d cancels", p.cancelled)
	}

	// the draft's name: Enter on a row takes its folder, accept the path typed
	p.chosen, p.c.closed = nil, nil
	p.enter = func(x string) { p.entered = append(p.entered, x) }
	p.submit = func(text string) { p.submitted = append(p.submitted, text) }
	p.enterRow(2)
	p.accept(0, "ideas/draft")
	if !slices.Equal(p.entered, []string{"notes/c.md"}) || !slices.Equal(p.submitted, []string{"ideas/draft"}) {
		t.Errorf("entered %v, submitted %v", p.entered, p.submitted)
	}
	if len(p.chosen) != 0 || len(p.c.closed) != 0 {
		t.Errorf("Enter and accept chose %v and closed %v; the picker's own did", p.chosen, p.c.closed)
	}
}

// File › Open opens with its field empty, whatever was typed in it the last time: Go empties the
// field's source (App.openField), as the dialog's own onOpened did before Picker.qml.
func TestTheOpenPickerOpensEmpty(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "# A\n", "b/zq.md", "# C\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.keys(t, decltest.Ctrl('o'))
	r.s.WaitForText(t, "files (2 of 2)")
	r.keys(t, decltest.Type("zq")...)
	r.s.WaitForText(t, "files (1 of 2)")
	r.keys(t, esc())
	r.s.WaitFor(t, "the picker closed", func(sc string) bool { return !strings.Contains(sc, "open a document") })
	r.keys(t, decltest.Ctrl('o'))
	r.s.WaitForText(t, "files (2 of 2)")
	for _, line := range strings.Split(r.s.String(), "\n") {
		if strings.Contains(line, "│ zq") {
			t.Fatalf("the field kept the last filter:\n%s", r.s)
		}
	}
}

// Naming the draft, Enter on a file takes its folder into the path, the picker staying open;
// Create makes the note at the path typed.
func TestTheDraftsNameTakesAFilesFolder(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"notes/a.md", "# A\n"}})
	r := attached(t, d)
	r.waitListed(t, 1)
	r.typeInEditor(t, "draft text")
	r.keys(t, decltest.Ctrl('s'))
	r.s.WaitForText(t, "save the draft")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyTab}, enter())
	r.s.WaitFor(t, "the folder in the path", func(string) bool {
		return onLoop(r, func() bool {
			v, _ := r.h.p.Tree().Source("App.newFilePath")
			return v.Raw == "notes/"
		})
	})
	if !strings.Contains(r.s.String(), "save the draft") {
		t.Fatalf("Enter on a file closed the picker:\n%s", r.s)
	}
	r.keys(t, decltest.Ctrl('k'))
	r.keys(t, decltest.Type("b")...)
	r.keys(t, enter())
	r.waitFile(t, "notes/b.md")
	if got := d.read(t, "kb", "notes/b.md"); got != "draft text" {
		t.Errorf("the note holds %q, want the draft", got)
	}
}

// Picker.qml's handlers reach a pick by the name they pass; a name no picker has is refused.
func TestThePickCommandsGoByName(t *testing.T) {
	p := newTestPick()
	cmds := pickCommands(map[string]picker{"test": p})
	str := func(s string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueString, Raw: s} }
	num := func(s string) qml.SpecValue { return qml.SpecValue{Kind: qml.SpecValueNumber, Raw: s} }
	run := func(name string, args ...qml.SpecValue) error { return cmds[name].handler(name)(args) }
	if err := run("App.pickFilter", str("test"), str("notes")); err != nil || len(p.shown()) != 2 {
		t.Fatalf("pickFilter: %v, rows %v", err, p.shown())
	}
	if err := run("App.pickMove", str("test"), num("1")); err != nil || p.title() != "notes/c.md" {
		t.Errorf("pickMove: %v, preview %v", err, p.title())
	}
	if err := run("App.pickChoose", str("test"), num("0")); err != nil || !slices.Equal(p.chosen, []string{"Notes/A.md"}) {
		t.Errorf("pickChoose: %v, chose %v", err, p.chosen)
	}
	if err := run("App.pickAccept", str("test"), num("1"), str("typed")); err != nil || !slices.Equal(p.chosen, []string{"Notes/A.md", "notes/c.md"}) {
		t.Errorf("pickAccept: %v, chose %v", err, p.chosen)
	}
	if err := run("App.pickCancel", str("test")); err != nil || p.cancelled != 1 {
		t.Errorf("pickCancel: %v, %d cancels", err, p.cancelled)
	}
	for name, args := range map[string][]qml.SpecValue{
		"App.pickFilter": {str("nope"), str("x")},
		"App.pickMove":   {str("nope"), num("0")},
		"App.pickChoose": {str("nope"), num("0")},
		"App.pickAccept": {str("nope"), num("0"), str("x")},
		"App.pickCancel": {str("nope")},
	} {
		if err := run(name, args...); err == nil || err.Error() != name+` takes a picker's name, not "nope"` {
			t.Errorf("%s on a picker nobody has: %v", name, err)
		}
	}
	if err := run("App.pickMove", str("test"), num("1.5")); err == nil || err.Error() != "App.pickMove takes a picker's name and a row, not 1.5" {
		t.Errorf("a row that is not whole: %v", err)
	}
}
