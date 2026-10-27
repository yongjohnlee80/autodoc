package tui

import (
	"bytes"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/decl"
	"github.com/yongjohnlee80/golib/tui/panelgeometry"
)

// CARD PANELS MADE AT RUNTIME (ADR 1792848741) — a card plugin added while AutoDoc runs gets its
// panel at once, and a removed one's goes: the registry's change is staged, the document reloaded
// with the new card set, and the change committed only when the reload succeeded.

const toggleCmd = "[[commands]]\nid = \"toggle\"\ntitle = \"Show or hide the card\"\nkey = \"o\"\n"

// leftCard is rightCard docked at the left, so two cards share the screen.
const leftCard = "[dialog]\nmodal = false\nwidth = 36\nheight = 8\nplacements = [\"left\"]\n"

// liveCardTUI is a TUI on the plugins in dir, in the terminal build or as a window (gui), with these
// preferences stored before it starts.
func liveCardTUI(t *testing.T, dir string, prefs map[string]string, gui bool) (*daemon, *running, string) {
	t.Helper()
	all := maps.Clone(testPrefs)
	maps.Copy(all, prefs)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: all})
	logs := t.TempDir()
	r := runTUI(t, NewSession(d.sock, nil), Options{GUI: gui, Plugins: Plugins{Dir: dir, LogDir: logs, Socket: d.sock}})
	r.connected(t)
	t.Cleanup(func() {
		for _, p := range onLoop(r, func() []*pluginRun { return slices.Collect(maps.Values(r.h.plugins.running)) }) {
			p.shutdown()
		}
	})
	return d, r, logs
}

// hasCardPanel reports whether name's panel is the program's: its registry spec, its cardPanel with
// a built view, and its Drawer in the document.
func (r *running) hasCardPanel(name string) bool {
	return onLoop(r, func() bool {
		id := cardPanelID(name)
		_, drawer := r.h.p.Find(id)
		c := r.h.plugins.cards[name]
		return r.h.panelSpec(id) != nil && c != nil && c.view != nil && drawer
	})
}

// listsPanel reports whether the dock's and :go's widgets name panel id.
func (r *running) listsPanel(id string) bool {
	return onLoop(r, func() bool {
		return slices.ContainsFunc(r.h.widgets(), func(w widgetTarget) bool { return w.panel == id })
	})
}

func (r *running) readFolder() { r.h.p.Post(r.h.plugins.loadPlugins) }

func runACardAddedWhileRunningOpensAtOnce(t *testing.T, gui bool) {
	dir := t.TempDir()
	installPlugin2(t, dir, "first", "dialog", "echo", rightCard)
	_, r, _ := liveCardTUI(t, dir, nil, gui)
	if r.hasCardPanel("card") {
		t.Fatal("a card not yet in the folder has a panel")
	}
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard+toggleCmd)
	r.readFolder()
	r.s.WaitFor(t, "the added card's panel", func(string) bool { return r.hasCardPanel("card") })
	if !r.hasCardPanel("first") {
		t.Fatal("adding a card took the first card's panel away")
	}
	run := r.openPlugin("card")
	if run == nil {
		t.Fatal("the added card did not start")
	}
	_, left, right, _ := waitFrame(t, r, "Card")
	if right != 99 || left < 50 {
		t.Fatalf("the added card's panel spans %d–%d, want docked at the right edge (99):\n%s", left, right, r.s)
	}
	r.waitShown(t, "open ")
	if !r.listsPanel(cardPanelID("card")) {
		t.Fatal("the dock does not list the added card")
	}
}

// A card plugin added while AutoDoc runs opens at once, docked at its placement, in both builds.
func TestACardAddedWhileRunningOpensAtOnce(t *testing.T) {
	runACardAddedWhileRunningOpensAtOnce(t, false)
}
func TestACardAddedWhileRunningOpensAtOnceGUI(t *testing.T) {
	runACardAddedWhileRunningOpensAtOnce(t, true)
}

// An open, focused card keeps its plugin, its panel and the keys while another card is added and
// removed: the reload patches the document around it.
func TestAnOpenCardSurvivesAnotherCardsAddAndRemoval(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "first", "dialog", "echo", rightCard+toggleCmd)
	_, r, logs := liveCardTUI(t, dir, nil, false)
	r.h.p.Post(func() { r.h.plugins.runPluginCommand("first", "toggle") })
	waitSaid(t, logs, "first", "focus true")
	run := onLoop(r, func() *pluginRun { return r.h.plugins.running["first"] })
	steady := func(what string, k rune) {
		t.Helper()
		if onLoop(r, func() *pluginRun { return r.h.plugins.running["first"] }) != run || !r.panelShown("first") {
			t.Fatalf("%s: the open card lost its run or its panel:\n%s", what, r.s)
		}
		if slices.Contains(said(t, logs, "first"), "focus false") {
			t.Fatalf("%s: the open card was told it lost the keys", what)
		}
		r.keys(t, key(k))
		waitSaid(t, logs, "first", "key "+string(k))
	}
	installPlugin2(t, dir, "card", "dialog", "echo", leftCard)
	r.readFolder()
	r.s.WaitFor(t, "the added card's panel", func(string) bool { return r.hasCardPanel("card") })
	steady("another card added", 'z')
	if err := os.RemoveAll(filepath.Join(dir, "card")); err != nil {
		t.Fatal(err)
	}
	r.readFolder()
	r.s.WaitFor(t, "the removed card's panel gone", func(string) bool { return onLoop(r, func() bool { return r.h.panelSpec(cardPanelID("card")) == nil }) })
	steady("another card removed", 'y')
}

// Removing a card stops its plugin and closes its open panel before the reload, while its Drawer is
// there: its Closed hook gives the keys back to the page. Then its panel, its dock icon and its
// Plugins menu row go, and its panel can no longer be opened.
func TestRemovingACardTakesItsPanelAway(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard+toggleCmd)
	_, r, logs := liveCardTUI(t, dir, nil, false)
	r.h.p.Post(func() { r.h.plugins.runPluginCommand("card", "toggle") })
	waitSaid(t, logs, "card", "focus true")
	run := onLoop(r, func() *pluginRun { return r.h.plugins.running["card"] })
	id := cardPanelID("card")
	if err := os.RemoveAll(filepath.Join(dir, "card")); err != nil {
		t.Fatal(err)
	}
	// loadPlugins runs here on the loop; the reload it asks for is only posted, so what follows is
	// the moment before it
	type moment struct{ open, drawer, running, editor bool }
	m := onLoop(r, func() moment {
		r.h.plugins.loadPlugins()
		_, drawer := r.h.p.Find(id)
		ed, ok := r.h.p.Find("editor")
		return moment{r.h.panelOpen[id], drawer, r.h.plugins.running["card"] != nil, ok && r.h.p.App().FocusWithin(ed)}
	})
	if open, drawer, running, editor := m.open, m.drawer, m.running, m.editor; open || !drawer || running || !editor {
		t.Fatalf("before the reload: panel open %v (want false), Drawer there %v (want true), running %v (want false), the page has the keys %v (want true)",
			open, drawer, running, editor)
	}
	r.s.WaitFor(t, "the panel gone", func(string) bool { return onLoop(r, func() bool { return r.h.panelSpec(id) == nil }) })
	select {
	case <-run.exited:
	case <-time.After(pluginGrace + pluginTermGrace + 5*time.Second):
		t.Fatal("the removed card's plugin did not exit")
	}
	if r.listsPanel(id) {
		t.Fatal("the dock still lists the removed card")
	}
	if label, _ := r.pluginRowLabel("card"); label != "" {
		t.Fatalf("the Plugins menu still has the removed card: %q", label)
	}
	if err := onLoop(r, func() error { return r.h.p.Call(id, "open") }); err == nil {
		t.Fatal("the removed card's panel still opens")
	}
}

// The same card added again opens with its kept geometry (tui.card_<name>.*, read again once it has
// a panel), and nothing is declared twice.
func TestACardAddedAgainKeepsItsGeometry(t *testing.T) {
	dir := t.TempDir()
	id := cardPanelID("card")
	_, r, _ := liveCardTUI(t, dir, map[string]string{panelgeometry.SizeKey(id, "right"): "60"}, false)
	for round := range 2 {
		installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
		r.readFolder()
		r.s.WaitFor(t, "the card's panel", func(string) bool { return r.hasCardPanel("card") })
		if r.openPlugin("card") == nil {
			t.Fatalf("round %d: the card did not start", round)
		}
		r.s.WaitFor(t, "the card at its kept 60% width", func(sc string) bool {
			_, left, right, _, ok := frameOf(sc, "Card")
			return ok && right == 99 && left <= 42
		})
		if n := r.noticesSaying("already"); n != 0 {
			t.Fatalf("round %d: %d notices of a source declared twice", round, n)
		}
		if err := os.RemoveAll(filepath.Join(dir, "card")); err != nil {
			t.Fatal(err)
		}
		r.readFolder()
		r.s.WaitFor(t, "the card's panel gone", func(string) bool { return onLoop(r, func() bool { return r.h.panelSpec(id) == nil }) })
	}
}

// The registry stages a contributor's panels under the build's checks and changes nothing until
// commit: a duplicate id, a feature with no panels and a PanelBound feature are refused.
func TestTheRegistryStagesThenCommits(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	_, r, _ := liveCardTUI(t, dir, nil, false)
	none := onLoop(r, func() Feature {
		for _, f := range r.h.reg.features {
			if _, ok := f.(Panel); !ok {
				return f
			}
		}
		return nil
	})
	if none == nil {
		t.Fatal("every feature contributes panels: this cell needs one that does not")
	}
	onLoop(r, func() any {
		reg := r.h.reg.panels
		before, host := slices.Clone(reg.specs), reg.hosts[resultsPanel]
		dup := append(r.h.plugins.Panels(), PanelSpec{ID: "explorer", View: "elsewhere", Edge: func(prefs) string { return "left" }})
		if _, _, err := reg.stage(core{r.h}, r.h.plugins, dup); err == nil || !strings.Contains(err.Error(), "explorer") {
			t.Errorf("a panel id two features claim: %v", err)
		}
		if _, _, err := reg.stage(core{r.h}, r.h.agents, nil); err == nil || !strings.Contains(err.Error(), "holds its panel host") {
			t.Errorf("a PanelBound feature's panels: %v, want refused for holding its host", err)
		}
		if _, _, err := reg.stage(core{r.h}, none, nil); err == nil || !strings.Contains(err.Error(), "contributes no panels") {
			t.Errorf("a feature contributing no panels: %v, want refused", err)
		}
		own := slices.DeleteFunc(r.h.plugins.Panels(), func(s PanelSpec) bool { return s.ID == cardPanelID("card") })
		specs, commit, err := reg.stage(core{r.h}, r.h.plugins, own)
		if err != nil {
			t.Error(err)
			return nil
		}
		if !slices.EqualFunc(reg.specs, before, func(a, b PanelSpec) bool { return a.ID == b.ID }) || reg.hosts[resultsPanel] != host {
			t.Error("staging changed the registry")
		}
		commit()
		if len(reg.specs) != len(specs) || findPanel(reg.specs, cardPanelID("card")) != nil || reg.hosts[resultsPanel] == host {
			t.Error("the commit did not swap the plugins' panels and rebuild their host")
		}
		if _, ok := reg.hosts[cardPanelID("card")]; ok {
			t.Error("the commit kept the dropped card's host")
		}
		return nil
	})
}

// A reload refused after a successful stage changes nothing: the card set is put back, the registry
// has no spec for the card, the dock does not list it, the plugins' host is the one it was, and the
// document has no Drawer for it. Opening it says why.
func TestAReloadRefusedAfterAStageChangesNothing(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "first", "dialog", "echo", rightCard)
	_, r, _ := liveCardTUI(t, dir, nil, false)
	host, cards := loopPair(r, func() (PanelHost, int) { return r.h.reg.panels.hosts[resultsPanel], len(r.h.plugins.cardOrder) })
	onLoop(r, func() any {
		r.h.reloadCards = func([]byte, map[string]any) (decl.Result, error) {
			return decl.Result{}, errors.New("refused for the test")
		}
		return nil
	})
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	r.readFolder()
	r.waitNotice(t, "refused for the test")
	id := cardPanelID("card")
	type after struct{ spec, set, sameHost, n bool }
	a := onLoop(r, func() after {
		_, inSet := r.h.plugins.cards["card"]
		return after{r.h.panelSpec(id) != nil, inSet, r.h.reg.panels.hosts[resultsPanel] == host, len(r.h.plugins.cardOrder) == cards}
	})
	spec, set, sameHost, n := a.spec, a.set, a.sameHost, a.n
	if spec || set || !sameHost || !n {
		t.Fatalf("after the refused reload: spec %v, in the card set %v (want both false); the plugins' host the same %v, the set's size the same %v (want both true)", spec, set, sameHost, n)
	}
	if r.listsPanel(id) {
		t.Fatal("the dock lists a card with no Drawer")
	}
	if onLoop(r, func() bool { _, ok := r.h.p.Find(id); return ok }) {
		t.Fatal("the document has the refused card's Drawer")
	}
	if r.openPlugin("card") != nil {
		t.Fatal("a card with no panel started")
	}
	r.waitNotice(t, "Card has no panel yet: it opens after AutoDoc restarts")
}

// A theme switch after a card was added writes the card set as it is: the added card keeps its
// panel, and its running plugin its run.
func TestAThemeSwitchKeepsAnAddedCard(t *testing.T) {
	dir := t.TempDir()
	_, r, _ := liveCardTUI(t, dir, nil, false)
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	r.readFolder()
	r.s.WaitFor(t, "the added card's panel", func(string) bool { return r.hasCardPanel("card") })
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	theme := onLoop(r, func() string {
		for _, n := range themeNames {
			if n != r.h.theme {
				return n
			}
		}
		return ""
	})
	r.h.p.Post(func() { r.h.switchTheme(theme) })
	r.s.WaitFor(t, "the theme switched", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
	if !r.hasCardPanel("card") || !r.panelShown("card") {
		t.Fatalf("the theme switch dropped the added card's panel:\n%s", r.s)
	}
	if onLoop(r, func() *pluginRun { return r.h.plugins.running["card"] }) != run {
		t.Fatal("the theme switch restarted the added card")
	}
}

// A card reload whose new card is refused as it is built (its PluginCard names no card) is refused
// before the tree is touched: the tree is not latched, the registry, the card set and the document
// are as they were, and the card already there and the page still work.
func TestARefusedCardConstructorChangesNothing(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "first", "dialog", "echo", rightCard)
	_, r, _ := liveCardTUI(t, dir, nil, false)
	var tried, latched bool
	onLoop(r, func() any {
		r.h.reloadCards = func(src []byte, sources map[string]any) (decl.Result, error) {
			bad := bytes.Replace(src, []byte(`PluginCard { id: card_cardView; plugin: "card" }`), []byte(`PluginCard { id: card_cardView; plugin: "nosuch" }`), 1)
			res, err := r.h.p.ReloadWithSources(bad, sources)
			tried, latched = true, r.h.p.Tree().Failed()
			return res, err
		}
		return nil
	})
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	r.readFolder()
	r.waitNotice(t, `"nosuch" is not a card plugin`)
	if !onLoop(r, func() bool { return tried }) || onLoop(r, func() bool { return latched || r.h.p.Tree().Failed() }) {
		t.Fatal("the refused constructor latched the tree, or the reload never ran")
	}
	if onLoop(r, func() bool {
		_, drawer := r.h.p.Find(cardPanelID("card"))
		return r.h.panelSpec(cardPanelID("card")) != nil || r.h.plugins.cards["card"] != nil || drawer
	}) {
		t.Fatal("the refused card is in the registry, the set or the document")
	}
	if !r.hasCardPanel("first") || r.openPlugin("first") == nil {
		t.Fatal("the card already there lost its panel or no longer starts")
	}
	r.waitShown(t, "open ")
	r.keys(t, key('i'), key('Q'))
	r.s.WaitFor(t, "the page takes typing", func(string) bool { return strings.Contains(r.editorText(), "Q") })
}

// A card reload that fails part-way (a colour refused while applying, on the Window that exists)
// latches the tree. AutoDoc does not remount it (a remount would strand the host on destroyed
// widgets): it says it must restart and puts the card set back. Once latched, a later change tries no
// reload and says the same.
func TestALatchedTreeAsksForARestart(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "first", "dialog", "echo", rightCard)
	_, r, _ := liveCardTUI(t, dir, nil, false)
	reloads := 0
	onLoop(r, func() any {
		r.h.reloadCards = func(src []byte, sources map[string]any) (decl.Result, error) {
			reloads++
			bad := bytes.Replace(src, []byte("    palette.window: Theme.app.window\n"), []byte("    palette.window: \"bleu\"\n"), 1)
			return r.h.p.ReloadWithSources(bad, sources)
		}
		return nil
	})
	const restart = "AutoDoc could not rebuild its screen: restart AutoDoc to show the card plugins"
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	r.readFolder()
	r.waitNotice(t, restart)
	failed, inSet, n := false, true, 0
	onLoop(r, func() any {
		failed, n = r.h.p.Tree().Failed(), reloads
		_, inSet = r.h.plugins.cards["card"]
		return nil
	})
	if !failed {
		t.Fatal("the colour was refused before the tree was touched; this cell needs a part-way failure")
	}
	if inSet || n != 1 {
		t.Fatalf("after the latch: the card in the set %v (want false), %d reloads (want 1)", inSet, n)
	}
	installPlugin2(t, dir, "other", "dialog", "echo", leftCard)
	r.readFolder()
	r.s.WaitFor(t, "the restart said again", func(string) bool { return r.noticesSaying(restart) == 2 })
	onLoop(r, func() any {
		failed, n = r.h.p.Tree().Failed(), reloads
		_, inSet = r.h.plugins.cards["other"]
		return nil
	})
	if !failed || inSet || n != 1 {
		t.Fatalf("a change after the latch: still latched %v (want true), the card in the set %v (want false), %d reloads (want 1)", failed, inSet, n)
	}
}
