package tui

import "testing"

// Enter on a document in the Links drawer opens it in the editor and closes the drawer, as Enter
// on a file in the explorer does: the editor has the keyboard.
func TestEnterInTheLinksOpensAndCloses(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.leader(t, 'l')
	r.s.WaitForText(t, "links (1)")
	r.s.WaitFor(t, "the drawer's keyboard", func(string) bool { return r.focusedOn("relationsList") })
	r.keys(t, enter())
	r.s.WaitFor(t, "b.md open, the drawer closed, the editor's keyboard", func(string) bool {
		return r.shownPath() == "b.md" && !r.panelIsOpen("links") && r.focusedOn("editor")
	})
}
