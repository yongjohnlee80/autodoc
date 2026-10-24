package tui

import (
	"context"
	"errors"
	"maps"
	"slices"
	"strings"
	"testing"
)

// THE EXPLORER'S TABS — the explorer and the links, one drawer of two tabs (explorer.go): SPC e and
// SPC l open it on their tab, or go to it, or close it on their own; the strip's _ hides it to the
// dock and x closes it; the links' kept place became the explorer's where it kept none.

// explorerTab is the tab the explorer's drawer shows, as its feature has it.
func (r *running) explorerTab() int { return onLoop(r, func() int { return r.h.explorer.tab }) }

// SPC e and SPC l are the drawer's two tabs: each opens it on its tab, goes to its tab from the other,
// and closes it from its own, the keyboard in the tab's list and the heading the tab's. The dock has
// the one icon.
func TestTheExplorerAndTheLinksAreTabsOfOneDrawer(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	onTab := func(what string, tab int, view, heading string) {
		t.Helper()
		r.s.WaitFor(t, what, func(sc string) bool {
			return r.panelIsOpen("explorer") && !r.hidden("explorer") && r.explorerTab() == tab &&
				r.focusedOn(view) && strings.Contains(sc, "┌ "+heading)
		})
	}

	r.leader(t, 'e')
	onTab("SPC e: the Explorer tab", explorerTab, "explorerTree", "explorer")
	r.waitDock(t, "one icon", "editor", "explorer")
	r.leader(t, 'l')
	onTab("SPC l from the Explorer tab: the Links tab", linksTab, "relationsList", "links (1)")
	r.waitDock(t, "still one icon", "editor", "explorer")
	r.leader(t, 'e')
	onTab("SPC e from the Links tab: the Explorer tab", explorerTab, "explorerTree", "explorer")
	r.leader(t, 'e')
	r.s.WaitFor(t, "SPC e on its own tab closed it", func(string) bool { return !r.panelIsOpen("explorer") && r.focusedOn("editor") })

	r.leader(t, 'l')
	onTab("SPC l, closed: open on the Links tab", linksTab, "relationsList", "links (1)")
	r.leader(t, 'l')
	r.s.WaitFor(t, "SPC l on its own tab closed it", func(string) bool { return !r.panelIsOpen("explorer") && r.focusedOn("editor") })
	r.waitDock(t, "the closed drawer gone", "editor")

	// :go relations, the Links drawer's old name, opens it on its tab
	if err := r.luaRun("go relations"); err != nil {
		t.Fatal(err)
	}
	onTab(":go relations: the Links tab", linksTab, "relationsList", "links (1)")
}

// The strip's _ hides the drawer, as the dock's own click does: open and in the dock, dimmed, and the
// dock's icon shows it again as it was, on its Links tab. x closes it, asking nothing, and it leaves
// the dock.
func TestTheExplorersStripHidesToTheDockAndCloses(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "[[b]]\n", "b.md", "bbb\n"}})
	r := attached(t, d)
	r.waitListed(t, 2)
	r.openByPicker(t, "a.md")
	r.waitFile(t, "a.md")
	r.leader(t, 'l')
	r.s.WaitFor(t, "the Links tab", func(string) bool { return r.focusedOn("relationsList") })

	windowControl(t, r, "_")
	r.s.WaitFor(t, "_ hid it, open and in the dock, dimmed", func(sc string) bool {
		return r.panelIsOpen("explorer") && r.hidden("explorer") && r.dockHiddenRow("explorer") && !strings.Contains(sc, "┌ links")
	})
	r.waitDock(t, "the hidden drawer kept", "editor", "explorer")
	r.s.WaitFor(t, "the keyboard in the editor", func(string) bool { return r.focusedOn("editor") })
	onLoop(r, func() bool { r.h.goDock("explorer"); return true })
	r.s.WaitFor(t, "its icon showed it again on the Links tab", func(sc string) bool {
		return !r.hidden("explorer") && r.explorerTab() == linksTab && r.focusedOn("relationsList") && strings.Contains(sc, "┌ links (1)")
	})

	windowControl(t, r, "x") // closed at once: a question would hold it open
	r.s.WaitFor(t, "x closed it", func(string) bool { return !r.panelIsOpen("explorer") && r.focusedOn("editor") })
	r.waitDock(t, "the closed drawer gone", "editor")
}

// The links' own panel kept its place under tui.links.*: the explorer, which holds the Links tab now,
// takes each key of it it keeps none of, its own winning, an empty one too. Nothing is written or
// deleted.
func TestTheLinksPanelsPlaceMovesToTheExplorer(t *testing.T) {
	in := map[string]any{
		"tui.links.edge": "bottom", "tui.links.bottom.size": "40", "tui.links.float": "10,10,30,50",
		"tui.links.right.length":    "70",
		"tui.explorer.float":        "", // docked by its user: kept
		"tui.explorer.right.length": "60",
	}
	before := maps.Clone(in)
	got := migrateLinksPanel(in)
	want := maps.Clone(in)
	want["tui.explorer.edge"], want["tui.explorer.bottom.size"] = "bottom", "40"
	if !maps.Equal(got, want) {
		t.Errorf("migrated %v, want %v", got, want)
	}
	if !maps.Equal(in, before) {
		t.Errorf("the store's map was changed: %v", in)
	}
	if p := prefsFor(in, false); p.explorerEdge != "bottom" {
		t.Errorf("the explorer opens from %q, want the links' bottom", p.explorerEdge)
	} else if s, _ := p.panelGeometry("explorer", "bottom"); s != 40 {
		t.Errorf("the explorer's size at the bottom is %d, want the links' 40", s)
	}
	if p := prefsFor(map[string]any{"tui.links.edge": "bottom", "tui.explorer.edge": "top"}, false); p.explorerEdge != "top" {
		t.Errorf("the explorer's own edge lost to the links': %q", p.explorerEdge)
	}

	// on a load, from the store: the explorer opens where the links did, and the links' key stays
	d, r := runShellTUI(t, map[string]string{"tui.links.edge": "top"})
	r.s.WaitFor(t, "the explorer at the top", func(string) bool {
		return onLoop(r, func() string { return r.h.panelEdges()["explorer"] }) == "top"
	})
	m, err := d.db.Preferences(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, wrote := m["tui.explorer.edge"]; wrote || m["tui.links.edge"] != "top" {
		t.Errorf("the store holds %v: want the links' key alone, as it was", m)
	}
}

// The explorer's tab as its feature keeps it: a tab the view never sends is refused, and a keyboard or
// a close its view refuses is said, not lost.
func TestTheExplorersTabRefusesWhatTheViewNeverSends(t *testing.T) {
	c := newScripted("kb")
	e := newExplorer(c)
	e.tabShown(linksTab)
	e.tabShown(7) // no such tab
	if e.tab != linksTab || c.set["App.explorerTab"] != linksTab || c.set["App.linksTabShown"] != true || e.view() != relationsView {
		t.Fatalf("after tab 7: tab %d, App.explorerTab %v, App.linksTabShown %v, view %q; want the Links tab kept",
			e.tab, c.set["App.explorerTab"], c.set["App.linksTabShown"], e.view())
	}
	c.viewErr = errors.New("no such view")
	e.showTab(explorerTab) // the scripted core runs a Post at once
	e.close()
	if want := []string{"explorerTree.forceActiveFocus", "explorer.close"}; !slices.Equal(c.views, want) {
		t.Errorf("views called %v, want %v", c.views, want)
	}
	if want := []string{"explorer: the keyboard: no such view", "explorer: closing: no such view"}; !slices.Equal(c.failed, want) {
		t.Errorf("failures said %v, want %v", c.failed, want)
	}
}
