package tui

import "testing"

// Enter on a document in the Links drawer opens it in the editor and hides the drawer, not closed
// (ADR 1792397302 r3): it stays in the dock, hidden, and the editor has the keyboard.
func TestEnterInTheLinksOpensAndHides(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.leader(t, 'l')
	r.s.WaitForText(t, "links (1)")
	r.s.WaitFor(t, "the drawer's keyboard", func(string) bool { return r.focusedOn("relationsList") })
	r.keys(t, enter())
	r.s.WaitFor(t, "b.md open, the drawer hidden, the editor's keyboard", func(string) bool {
		return r.shownPath() == "b.md" && r.panelIsOpen("links") && r.hidden("links") && r.focusedOn("editor")
	})
	r.waitDock(t, "the hidden links kept", "editor", "relations") // the Links drawer's :go name
}
