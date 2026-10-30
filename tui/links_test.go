package tui

import "testing"

// Enter on a document in the Links tab opens it in the editor and hides the explorer's drawer, not
// closed (docs/decisions/editor.md#dock-clicks-hide-and-show): it stays in the dock, hidden, and
// the editor has the keyboard. Its icon shows it again on the Links tab, the keyboard in the list.
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
		return r.shownPath() == "b.md" && r.panelIsOpen("explorer") && r.hidden("explorer") && r.focusedOn("editor")
	})
	r.waitDock(t, "the hidden drawer kept", "editor", "explorer")
	onLoop(r, func() bool { r.h.goDock("explorer"); return true })
	r.s.WaitFor(t, "shown again on the Links tab, the keyboard in its list", func(string) bool {
		return !r.hidden("explorer") && r.focusedOn("relationsList")
	})
}
