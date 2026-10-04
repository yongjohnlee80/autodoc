package tui

import (
	"strings"
	"testing"
)

// Two TUIs on one daemon keep their own workspace and their own unsaved file while each sees what
// the other changed: a pattern edit is announced to the peer only, and a rename of the peer's
// workspace keeps its file open and unsaved.
func TestTwoClientsStayIndependentAndSeePeerChanges(t *testing.T) {
	d := startManaged(t, map[string]string{
		"alpha": fileDir(t, "a.md", "# Alpha\n\nalpha note\n"),
		"beta":  fileDir(t, "b.md", "# Beta\n\nbeta note\n"),
	})
	one := runTUI(t, NewSession(d.sock, nil), Options{Workspace: "alpha"})
	two := runTUI(t, NewSession(d.sock, nil), Options{Workspace: "beta"})
	one.s.WaitForText(t, "· alpha")
	two.s.WaitForText(t, "· beta")

	one.h.p.Post(func() { one.h.openPath("a.md") })
	one.waitFile(t, "a.md")
	one.h.p.Post(func() {
		one.h.editor.SetValue("# Alpha\n\nalpha note, edited and unsaved\n")
		one.h.edited()
	})
	one.s.WaitFor(t, "one's note dirty", func(string) bool { return one.file().dirty })

	// two edits alpha's rules: one is told, two is not told of its own change
	two.openSettings(t, "alpha", 0)
	two.saveForm(func(f *settingsForm) { f.include, f.exclude = "**/*.md", ".git/**" })
	two.waitNoticed(t, "alpha: saved rules")
	one.waitNoticed(t, "workspace alpha: its rules were changed by another client")
	// two has read the log past that event before it is asked what it was told
	past := onLoop(one, func() int64 { return one.h.evCursor })
	two.s.WaitFor(t, "two past the event", func(string) bool { return onLoop(two, func() int64 { return two.h.evCursor }) >= past })
	if onLoop(two, func() bool {
		for _, n := range two.h.notices {
			if strings.Contains(n.text, "changed by another client") {
				return true
			}
		}
		return false
	}) {
		t.Fatal("two was told of its own change as a peer's")
	}
	if ws := onLoop(one, func() string { return one.h.ws }); ws != "alpha" {
		t.Fatalf("one's workspace = %q after a peer's edit", ws)
	}
	if ws := onLoop(two, func() string { return two.h.ws }); ws != "beta" {
		t.Fatalf("two's workspace = %q: changing another workspace moved it", ws)
	}
	if n := one.file(); !n.open || !n.dirty || n.path != "a.md" || !strings.Contains(one.editorText(), "edited and unsaved") {
		t.Fatalf("one's note after a peer's edit: %+v", n)
	}

	// two renames alpha: one follows the name, its file still open and unsaved
	two.openSettings(t, "alpha", 0)
	two.saveForm(func(f *settingsForm) { f.name = "alpha2" })
	one.s.WaitFor(t, "one on alpha2", func(string) bool { return onLoop(one, func() string { return one.h.ws }) == "alpha2" })
	one.s.WaitForText(t, "· alpha2")
	if n := one.file(); !n.open || !n.dirty || n.path != "a.md" || !strings.Contains(one.editorText(), "edited and unsaved") {
		t.Fatalf("one's note after a peer's rename: %+v", n)
	}
}

// A peer deleting the workspace this TUI has an unsaved file in keeps the file's text, as the
// untitled draft, in the workspace entered next.
func TestAPeersRemovalKeepsTheUnsavedText(t *testing.T) {
	d := startManaged(t, map[string]string{
		"alpha": fileDir(t, "a.md", "# Alpha\n"),
		"beta":  fileDir(t, "b.md", "# Beta\n"),
	})
	one := runTUI(t, NewSession(d.sock, nil), Options{Workspace: "alpha"})
	two := runTUI(t, NewSession(d.sock, nil), Options{Workspace: "beta"})
	one.s.WaitForText(t, "· alpha")
	two.s.WaitForText(t, "· beta")
	one.h.p.Post(func() { one.h.openPath("a.md") })
	one.waitFile(t, "a.md")
	one.h.p.Post(func() {
		one.h.editor.SetValue("# Alpha\n\nwords nobody saved\n")
		one.h.edited()
	})
	one.s.WaitFor(t, "dirty", func(string) bool { return one.file().dirty })
	two.h.p.Post(func() {
		two.h.removing = "alpha"
		two.h.removeWorkspaceConfirmed()
	})
	one.s.WaitForText(t, "· beta")
	one.waitNoticed(t, "kept as an untitled draft")
	if n := one.file(); n.open || !n.dirty || !strings.Contains(one.editorText(), "words nobody saved") {
		t.Fatalf("after the peer's removal: %+v, text %q", n, one.editorText())
	}
}
