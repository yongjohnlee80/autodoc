package tui

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// CARD PANELS — a card plugin is a docked panel of its own: it opens at its first placement's edge,
// moves and resizes as every panel does and keeps both, and closing it (Esc, its right-click menu,
// the plugin's host.close) hides it, its plugin running on. A modal dialog is a float, as it was.

// cardTUI is pluginTUIWith, with the daemon, so a test reads the preferences it stored.
func cardTUI(t *testing.T, dir string, prefs map[string]string) (*daemon, *running, string) {
	t.Helper()
	all := maps.Clone(testPrefs)
	maps.Copy(all, prefs)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: all})
	r, logs := pluginTUIOn(t, d, dir, "")
	return d, r, logs
}

// frameOf is the panel titled title on the screen: its frame's top row, left and right columns,
// and bottom row (the right and bottom -1 where another panel covers them); ok false when its
// title is not there.
func frameOf(screen, title string) (top, left, right, bottom int, ok bool) {
	lines := strings.Split(screen, "\n")
	for y, l := range lines {
		c := col(l, "┌ "+title)
		if c < 0 {
			continue
		}
		rs := []rune(l)
		right = -1
		for x := c + 1; x < len(rs); x++ {
			if rs[x] == '┐' {
				right = x
				break
			}
		}
		bottom = -1
		for yy := y + 1; yy < len(lines); yy++ {
			if b := []rune(lines[yy]); c < len(b) && b[c] == '└' {
				bottom = yy
				break
			}
		}
		return y, c, right, bottom, true
	}
	return 0, 0, 0, 0, false
}

// waitFrame waits for the panel titled title, and is where it is.
func waitFrame(t *testing.T, r *running, title string) (top, left, right, bottom int) {
	t.Helper()
	r.s.WaitFor(t, "the panel "+title, func(sc string) bool {
		var ok bool
		top, left, right, bottom, ok = frameOf(sc, title)
		return ok
	})
	return top, left, right, bottom
}

// panelShown is whether the card plugin name's panel is open.
func (r *running) panelShown(name string) bool {
	return onLoop(r, func() bool { return r.h.panelOpen[cardPanelID(name)] })
}

// stillRunning fails the test when run exits within a moment: the card hid, its plugin runs on.
func stillRunning(t *testing.T, run *pluginRun, what string) {
	t.Helper()
	select {
	case <-run.exited:
		t.Fatalf("%s stopped the card's plugin", what)
	case <-time.After(300 * time.Millisecond):
	}
}

var openedAt = regexp.MustCompile(`open (\d+)x(\d+)`)

// openSize is the size the plugin was opened at, from its log.
func openSize(t *testing.T, logs, name string) (w, h int) {
	t.Helper()
	for _, l := range waitSaid(t, logs, name, "open ") {
		if m := openedAt.FindStringSubmatch(l); m != nil {
			w, _ = strconv.Atoi(m[1])
			h, _ = strconv.Atoi(m[2])
			return w, h
		}
	}
	t.Fatal("the plugin never said its size")
	return 0, 0
}

const rightCard = "[dialog]\nmodal = false\nwidth = 36\nheight = 8\nplacements = [\"right\", \"left\"]\n"

// TestACardOpensAsADockedPanelAtItsFirstPlacement: a card whose placements are right, then left,
// opens docked at the right edge, in a panel and not a float, about as wide as its manifest's width,
// and leaves the keys with the editor; Manage › Place moves it to the left, docked there.
func TestACardOpensAsADockedPanelAtItsFirstPlacement(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	d, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	_, left, right, _ := waitFrame(t, r, "Card")
	if right != 99 || left < 50 {
		t.Fatalf("the card's panel spans columns %d–%d, want docked at the right edge (99):\n%s", left, right, r.s)
	}
	if run.float != nil || run.box != nil {
		t.Fatal("a card was given a float of its own")
	}
	if w, _ := openSize(t, logs, "card"); w < 30 || w > 45 || w != right-left-1 {
		t.Fatalf("the card was opened %d wide in a panel %d–%d, want about its manifest's 36, filling it", w, left, right)
	}
	r.keys(t, key('i'), key('Q'))
	r.s.WaitFor(t, "the editor took the keys", func(string) bool { return strings.Contains(r.editorText(), "Q") })
	if slices.Contains(said(t, logs, "card"), "key Q ctrl=false") {
		t.Fatal("a card opened from its menu entry took the keys")
	}
	r.keys(t, esc())

	r.h.p.Post(r.h.plugins.managePlugins)
	r.s.WaitFor(t, "the manager listed", func(string) bool { return onLoop(r, func() int { return len(r.h.plugins.managedPlugins.All()) }) == 1 })
	r.h.p.Post(func() { r.h.plugins.placePlugin(0) })
	storedPref(t, d, r, "tui.plugin.card.placement", "left")
	r.h.p.Post(func() { r.h.closeDialog("pluginManager") })
	r.s.WaitFor(t, "docked at the left", func(sc string) bool { _, l, _, _, ok := frameOf(sc, "Card"); return ok && l == 0 })
}

// TestEscHidesACardAndACommandShowsIt: Esc in a focused card closes its panel, the keys back with
// the editor, the plugin told plugin.focus false and plugin.hide and running on; a command opens
// the panel again, focused, the plugin told plugin.show, and is sent.
func TestEscHidesACardAndACommandShowsIt(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard+"[[commands]]\nid = \"hello\"\ntitle = \"Say hello\"\nkey = \"h\"\n")
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.waitShown(t, "focus true")

	r.keys(t, esc())
	waitSaid(t, logs, "card", "hide")
	if got := said(t, logs, "card"); !slices.Contains(got, "focus false") {
		t.Fatalf("Esc did not give the keys back: it said %q", got)
	}
	if r.panelShown("card") {
		t.Fatalf("Esc left the panel open:\n%s", r.s)
	}
	stillRunning(t, run, "Esc")
	if onLoop(r, func() *pluginRun { return r.h.plugins.running["card"] }) != run {
		t.Fatal("the hidden card's plugin is not kept")
	}
	r.keys(t, key('i'), key('W'))
	r.s.WaitFor(t, "the editor has the keys", func(string) bool { return strings.Contains(r.editorText(), "W") })
	r.keys(t, esc())

	r.pluginCardOf(t, "card")
	r.keys(t, key('h'))
	waitSaid(t, logs, "card", "command hello")
	waitSaid(t, logs, "card", "show")
	waitCount(t, logs, "card", "focus true", 2)
	if !r.panelShown("card") {
		t.Fatal("the command did not open the card's panel")
	}
	if onLoop(r, func() *pluginRun { return r.h.plugins.running["card"] }) != run {
		t.Fatal("the command started the hidden card's plugin again")
	}
	r.keys(t, key('z'))
	r.waitShown(t, "key z")
}

// TestQTypedIntoACardIsThePlugins: AutoDoc never takes q from a focused card: a plugin typing into
// a field of its own is sent it, and its panel stays open.
func TestQTypedIntoACardIsThePlugins(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "typist", rightCard)
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.waitShown(t, "focus true")
	r.keys(t, key('q'))
	r.waitShown(t, "key q")
	stillRunning(t, run, "q")
	if !r.panelShown("card") || slices.Contains(said(t, logs, "card"), "hide") {
		t.Fatalf("q closed the card's panel:\n%s", r.s)
	}
}

// TestHostCloseFromACardHidesItsPanel: a card's host.close (the test plugin's on q) closes its
// panel as Esc does: the plugin is told plugin.hide and runs on, and its menu entry shows it again.
func TestHostCloseFromACardHidesItsPanel(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.waitShown(t, "focus true")
	r.keys(t, key('q')) // the plugin's own quit: host.close
	waitSaid(t, logs, "card", "hide")
	r.s.WaitFor(t, "the panel closed", func(string) bool { return !r.panelShown("card") })
	stillRunning(t, run, "host.close")
	r.openPlugin("card")
	waitSaid(t, logs, "card", "show")
	if !r.panelShown("card") {
		t.Fatal("the menu entry did not show the hidden card")
	}
}

// TestACardsRightClickMenuClosesIt: a right press on a card opens its menu; Close (Esc) closes the
// panel as Esc does, the plugin running on.
func TestACardsRightClickMenuClosesIt(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, X: x + 2, Y: y + 1},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseRight, X: x + 2, Y: y + 1})
	r.s.WaitForText(t, "Close (Esc)")
	r.keys(t, enter())
	waitSaid(t, logs, "card", "hide")
	r.s.WaitFor(t, "the panel closed", func(string) bool { return !r.panelShown("card") })
	stillRunning(t, run, "the menu's Close")
	if strings.Contains(r.s.String(), "Close (Esc)") {
		t.Fatalf("the menu stayed open:\n%s", r.s)
	}
}

// TestACardPanelMovesAndResizesAndKeepsThem: an Alt/Option-left drag floats the card's panel where
// it is left, an Alt/Option-right drag resizes it and the plugin is told its new size; the store
// keeps the rectangle under the panel's id, the panel opens there again after Esc hid it, and a TUI
// started on the store opens it there.
func TestACardPanelMovesAndResizesAndKeepsThem(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	d, r, logs := cardTUI(t, dir, nil)
	r.openPlugin("card")
	top, left, right, bottom := waitFrame(t, r, "Card")
	w, h := openSize(t, logs, "card")

	altDrag(t, r, tuicore.MouseLeft, left+3, top+2, -20, 3)
	r.s.WaitFor(t, "moved twenty columns left and three rows down", func(sc string) bool {
		t2, l2, _, _, ok := frameOf(sc, "Card")
		return ok && l2 == left-20 && t2 == top+3
	})
	r.s.WaitFor(t, "the move kept", func(string) bool {
		m, _ := d.db.Preferences(context.Background())
		_, ok := parseFloat(m["tui."+cardPanelID("card")+".float"])
		return ok
	})

	moved := func() string {
		m, _ := d.db.Preferences(context.Background())
		return m["tui."+cardPanelID("card")+".float"]
	}()
	top, left, right, bottom = waitFrame(t, r, "Card")
	// a right-edge panel resizes from its bottom-left corner: six columns wider to the left, two rows taller
	altDrag(t, r, tuicore.MouseRight, left+1, bottom-1, -6, 2)
	waitSaid(t, logs, "card", "resize "+strconv.Itoa(w+6)+"x"+strconv.Itoa(h+2))
	var kept string
	r.s.WaitFor(t, "the resize kept", func(string) bool {
		m, _ := d.db.Preferences(context.Background())
		kept = m["tui."+cardPanelID("card")+".float"]
		_, ok := parseFloat(kept)
		return ok && kept != moved
	})
	r.s.WaitFor(t, "resized on the screen", func(sc string) bool {
		_, l2, r2, _, ok := frameOf(sc, "Card")
		return ok && r2-l2 == right-left+6
	})
	top, left, right, bottom = waitFrame(t, r, "Card")

	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.waitShown(t, "focus true")
	r.keys(t, esc())
	r.s.WaitFor(t, "hidden", func(string) bool { return !r.panelShown("card") })
	r.openPlugin("card")
	if t2, l2, r2, b2 := waitFrame(t, r, "Card"); t2 != top || l2 != left || r2 != right || b2 != bottom {
		t.Fatalf("shown again at %d,%d–%d,%d, want where it was left, %d,%d–%d,%d", l2, t2, r2, b2, left, top, right, bottom)
	}

	again, _ := pluginTUIOn(t, d, dir, "")
	again.s.WaitFor(t, "the kept rectangle read", func(string) bool {
		return onLoop(again, func() bool { _, ok := again.h.prefs.panelFloat[cardPanelID("card")]; return ok })
	})
	again.openPlugin("card")
	if t2, l2, r2, b2 := waitFrame(t, again, "Card"); t2 != top || l2 != left || r2 != right || b2 != bottom {
		t.Fatalf("a new TUI opens the card at %d,%d–%d,%d, want the kept %s: %d,%d–%d,%d", l2, t2, r2, b2, kept, left, top, right, bottom)
	}
}

// TestACardOnTheRightAndTheResultsAtTheBottom: a card placed right, then left, and the table it
// sends open together, the card docked at the right edge and the results along the bottom, each in
// its own panel, both on the screen.
func TestACardOnTheRightAndTheResultsAtTheBottom(t *testing.T) {
	dir := t.TempDir()
	installPlugin4(t, dir, "db", "dialog", "table", rightCard+tableManifest)
	_, r, _ := cardTUI(t, dir, nil)
	r.openPlugin("db")
	_, left, right, _ := waitFrame(t, r, "Db")
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("db", "run"); return true })
	r.s.WaitFor(t, "the results open", func(string) bool { return onLoop(r, func() bool { return r.h.panelOpen[resultsPanel] }) })
	rTop, rLeft, rRight, rBottom := waitFrame(t, r, "results: Rows")
	cTop, cLeft, cRight, _ := waitFrame(t, r, "Db")
	if cRight != 99 || cLeft != left || cRight != right {
		t.Fatalf("the card moved when the results opened: %d–%d, was %d–%d:\n%s", cLeft, cRight, left, right, r.s)
	}
	// the card is drawn over the corner of the results' top row: the results' right corner may be
	// covered, never their left, their heading or their rows
	if rTop < 15 || rBottom < 25 || rLeft != 0 {
		t.Fatalf("the results are not along the bottom (rows %d–%d, columns %d–%d):\n%s", rTop, rBottom, rLeft, rRight, r.s)
	}
	if !r.panelShown("db") || cTop >= rTop {
		t.Fatalf("the card (from row %d) is not shown above the results (from row %d):\n%s", cTop, rTop, r.s)
	}
	if !strings.Contains(r.s.String(), "row 1") {
		t.Fatalf("the results show no rows:\n%s", r.s)
	}
}

// TestAModalDialogIsAFloatAsItWas: a modal dialog plugin is not a panel: it opens in a float of its
// own, its title saying what Esc does, and Esc closes it and stops its plugin.
func TestAModalDialogIsAFloatAsItWas(t *testing.T) {
	dir := t.TempDir()
	installTestPlugin(t, dir, "game", "echo", 30, 6)
	_, r, _ := cardTUI(t, dir, nil)
	run := r.openPlugin("game")
	r.waitShown(t, "╭ Game · Esc closes")
	if run.float == nil || onLoop(r, func() bool { return r.h.panelSpec(cardPanelID("game")) != nil }) {
		t.Fatal("a modal dialog was made a panel")
	}
	r.keys(t, esc())
	select {
	case <-run.exited:
	case <-time.After(10 * time.Second):
		t.Fatal("Esc did not close the modal dialog")
	}
}
