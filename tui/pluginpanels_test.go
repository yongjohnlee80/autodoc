package tui

import (
	"context"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/panelgeometry"
	"github.com/yongjohnlee80/golib/tui/style"
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

func (r *running) cardOpenLabel(name string) string {
	return onLoop(r, func() string {
		rows := r.h.plugins.pluginRows
		for i := 0; i < rows.Len(); i++ {
			row := rows.At(i)
			if row["key"] != name {
				continue
			}
			sub, _ := row["rows"].(*tuidecl.ListModel)
			if sub != nil {
				for j := 0; j < sub.Len(); j++ {
					if item := sub.At(j); item["key"] == "open" {
						label, _ := item["label"].(string)
						return label
					}
				}
			}
		}
		return ""
	})
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

// Protocol 6 delivers left presses and wheel movement to a card, while the right press remains
// the host's context menu. Earlier card protocols keep their old mouse behavior.
func TestACardReceivesItsPointerAtProtocolSix(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	path := filepath.Join(dir, "card", "plugin.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "protocol = 2", "protocol = 6", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, r, logs := cardTUI(t, dir, nil)
	r.openPlugin("card")
	r.waitShown(t, "open ")
	before := r.editorText()
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y})
	waitSaid(t, logs, "card", "pointer press left")
	waitSaid(t, logs, "card", "focus true")
	r.keys(t, key('j'))
	waitSaid(t, logs, "card", "key j")
	if r.editorText() != before {
		t.Fatal("a click on the card left the following j with the editor")
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelDown, X: x, Y: y, Count: 2})
	waitSaid(t, logs, "card", "pointer wheel down")
}

// An actual modal dialog has the same protocol-6 pointer path as a docked card.
func TestAModalPluginReceivesItsPointerAtProtocolSix(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", "[dialog]\nwidth = 36\nheight = 8\n")
	path := filepath.Join(dir, "card", "plugin.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "protocol = 2", "protocol = 6", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, r, logs := cardTUI(t, dir, nil)
	r.openPlugin("card")
	r.waitShown(t, "open ")
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y})
	waitSaid(t, logs, "card", "pointer press left")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelDown, X: x, Y: y, Count: 2})
	waitSaid(t, logs, "card", "pointer wheel down")
}

// A protocol-5 card still takes focus from a click but never receives plugin.pointer. The key is
// an ordered witness: a pointer delivered before it would already be in the plugin's log.
func TestAProtocolFiveCardReceivesNoPointer(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	path := filepath.Join(dir, "card", "plugin.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(b), "protocol = 2", "protocol = 5", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	_, r, logs := cardTUI(t, dir, nil)
	r.openPlugin("card")
	r.waitShown(t, "open ")
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y}, key('j'))
	waitSaid(t, logs, "card", "key j")
	for _, line := range said(t, logs, "card") {
		if strings.Contains(line, "pointer ") {
			t.Fatalf("protocol-5 plugin received a pointer: %q", line)
		}
	}
}

// Inspect the host's outbound queue before the SDK can discard an unsupported notification.
// A protocol-5 plugin must not be sent plugin.pointer at all, even if its SDK ignores it.
func TestAProtocolFiveViewQueuesNoPointer(t *testing.T) {
	run := &pluginRun{e: pluginEntry{m: manifest{Protocol: 5}}}
	view := &pluginView{run: run, laidW: 20, laidH: 8}
	for _, ev := range []tuicore.MouseEvent{
		{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: 2, Y: 3},
		{Kind: tuicore.MouseWheel, Button: tuicore.WheelDown, X: 2, Y: 3, Count: 2},
	} {
		if view.HandleEvent(ev) {
			t.Fatalf("protocol-5 view consumed unsupported pointer event: %+v", ev)
		}
	}
	if len(run.early) != 0 {
		t.Fatalf("protocol-5 view queued unsupported notifications: %+v", run.early)
	}
}

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

// Esc hides a focused card and tells the user how to reopen it. Its SPC p toggle shows the same
// running plugin again and gives the card the keys.
func TestEscHidesACardAndTheLeaderReopensIt(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard+"[[commands]]\nid = \"toggle\"\ntitle = \"Show or hide the card\"\nkey = \"o\"\n")
	_, r, logs := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
	r.waitShown(t, "focus true")

	r.keys(t, esc())
	waitSaid(t, logs, "card", "hide")
	r.waitNotice(t, "Card card hidden: SPC p › Card o or Plugins › Card › Reopen card")
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
	r.s.WaitForText(t, "o  Show or hide the card")
	r.keys(t, key('o'))
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
	if got := r.cardOpenLabel("card"); got != "Reopen card" {
		t.Errorf("hidden card's menu action says %q, want Reopen card", got)
	}
	stillRunning(t, run, "host.close")
	r.openPlugin("card")
	waitSaid(t, logs, "card", "show")
	if got := r.cardOpenLabel("card"); got != "Show card" {
		t.Errorf("shown card's menu action says %q, want Show card", got)
	}
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
		_, ok := panelgeometry.ParseFloat(m["tui."+cardPanelID("card")+".float"])
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
		_, ok := panelgeometry.ParseFloat(kept)
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
	// each panel anchors at its own edge alone, whatever else is open, so the card may be drawn over
	// the right end of the results' top row, as the explorer at the right and the terminal at the
	// bottom overlap: the results' right corner may be covered, never their left, their heading or
	// their rows
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

// TestArrangingACardPanelNamesItAndMovesIt: Go › Arrange panel (SPC L) with a card's panel open
// reaches that panel, names it by the plugin's title, moves it by its keys, and keeps where Enter
// leaves it under the panel's id.
func TestArrangingACardPanelNamesItAndMovesIt(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	d, r, _ := cardTUI(t, dir, nil)
	r.openPlugin("card")
	top, left, _, _ := waitFrame(t, r, "Card")
	onLoop(r, func() bool { r.h.arrangePanel(); return true }) // the card's panel, the one opened last
	r.s.WaitForText(t, "arrange Card")
	if strings.Contains(r.s.String(), "arrange the "+cardPanelID("card")) {
		t.Fatalf("Arrange panel named the card by its panel's id:\n%s", r.s)
	}
	r.keys(t, key('h'), key('h'), key('j'), enter()) // the arrange card covers the panel's title until Enter
	r.s.WaitFor(t, "four columns left, a row down", func(sc string) bool {
		t2, l2, _, _, ok := frameOf(sc, "Card")
		return ok && l2 == left-4 && t2 == top+1
	})
	r.s.WaitFor(t, "kept once", func(string) bool {
		m, _ := d.db.Preferences(context.Background())
		_, ok := panelgeometry.ParseFloat(m["tui."+cardPanelID("card")+".float"])
		return ok
	})
}

// cellsDrawn is a Surface that counts the cells drawn into it, beyond its fill.
type cellsDrawn struct {
	tuicore.Surface
	w, h  int
	cells []string
}

func (s *cellsDrawn) Size() tuicore.Size                        { return tuicore.Size{W: s.w, H: s.h} }
func (s *cellsDrawn) Fill(tuicore.Rect, string, style.Style)    {}
func (s *cellsDrawn) StringWidth(g string) int                  { return len([]rune(g)) }
func (s *cellsDrawn) SetCell(x, y int, g string, _ style.Style) { s.cells = append(s.cells, g) }

// TestACardViewBetweenRunsDrawsNothingAndTakesNothing: a card's view whose plugin is not running
// draws no frame, takes no key or focus, and lays out without telling anyone; one whose plugin
// runs draws its frame only within the view, leaves key releases and keys it cannot name alone, and
// sends no resize for a size the plugin already has.
func TestACardViewBetweenRunsDrawsNothingAndTakesNothing(t *testing.T) {
	v := &pluginView{card: "card"}
	s := &cellsDrawn{w: 4, h: 1}
	v.Render(s)
	if len(s.cells) != 0 {
		t.Fatalf("a card with no plugin running drew %q", s.cells)
	}
	if v.HandleEvent(key('a')) || v.HandleEvent(tuicore.FocusEvent{Gained: true}) {
		t.Fatal("a card with no plugin running took an event")
	}
	v.resized()    // nothing to open or resize
	v.closePanel() // no feature to close it through: nothing happens

	r := &pluginRun{opened: true, frame: [][]pluginCell{{{text: "ab"}}, {{text: "cd"}}}}
	v.attach(r)
	v.Render(s)
	if strings.Join(s.cells, "") != "ab" {
		t.Fatalf("a one-row view drew %q, want its first row only", s.cells)
	}
	if v.HandleEvent(tuicore.KeyEvent{Kind: tuicore.KeyRelease, Code: 'a'}) {
		t.Fatal("a key release was taken")
	}
	if _, ok := pluginKey(tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyMenu}); ok {
		t.Fatal("a key the protocol has no name for was named")
	}
	v.laidW, v.laidH, v.sentW, v.sentH = 4, 1, 4, 1
	v.resized() // the size the plugin has: nothing sent, which would need a link
	v.detach(&pluginRun{})
	if v.run != r {
		t.Fatal("detaching another run left the view")
	}
	v.detach(r)
	if v.run != nil {
		t.Fatal("the view still shows a run that went")
	}
}

// A card the dock hid is shown again by its plugin's commands, the toggle among them: the dock's
// hide is a minimize, the panel still open, and a command that brings the card up restores it, as
// the dock's own icon does. The toggle does not close a card the dock hid.
func TestACardTheDockHidComesBackForItsCommands(t *testing.T) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard+"[[commands]]\nid = \"toggle\"\ntitle = \"Show or hide the card\"\nkey = \"o\"\n"+
		"[[commands]]\nid = \"again\"\ntitle = \"Run again\"\nkey = \"a\"\n")
	_, r, _ := cardTUI(t, dir, nil)
	run := r.openPlugin("card")
	r.waitShown(t, "open ")
	id := cardPanelID("card")
	hide := func() {
		onLoop(r, func() bool { r.h.hidePanel(id); return true }) // the dock's click on a shown card
		r.s.WaitFor(t, "hidden by the dock", func(string) bool { return r.hidden(id) })
	}

	hide()
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("card", "again"); return true })
	r.s.WaitFor(t, "shown for its command", func(string) bool { return !r.hidden(id) && r.panelShown("card") })

	hide()
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("card", pluginToggle); return true })
	r.s.WaitFor(t, "shown for its toggle, not closed", func(string) bool { return !r.hidden(id) && r.panelShown("card") })
	if onLoop(r, func() *pluginRun { return r.h.plugins.running["card"] }) != run {
		t.Fatal("bringing the card back started its plugin again")
	}
}

// noticesSaying counts the notifications that say text.
func (r *running) noticesSaying(text string) int {
	return onLoop(r, func() int {
		n := 0
		for _, it := range r.h.noticeLog.items {
			if strings.Contains(it.text, text) {
				n++
			}
		}
		return n
	})
}

// pluginRowLabel is the Plugins menu's row for name, and whether it is a plain item.
func (r *running) pluginRowLabel(name string) (string, bool) {
	return loopPair(r, func() (string, bool) {
		rows := r.h.plugins.pluginRows
		for i := 0; i < rows.Len(); i++ {
			if row := rows.At(i); row["key"] == name {
				label, _ := row["label"].(string)
				return label, row["kind"] == "item"
			}
		}
		return "", false
	})
}
