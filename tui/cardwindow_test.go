package tui

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// A PROTOCOL-12 CARD (cardwindow.go, pluginpanels.go, pluginview.go): its pointer gesture, a press,
// its drags and one release or cancel; its window's icons; and the keys it leaves to the Window.

// installCard12 installs the test plugin, in mode, as a protocol-12 card docked at the right, with
// its [window] controls when controls is not "" (a TOML list's inside: "\"hide\", \"close\"").
func installCard12(t *testing.T, dir, name, mode, controls string) {
	t.Helper()
	extra := rightCard
	if controls != "" {
		extra += "[window]\ncontrols = [" + controls + "]\n"
	}
	installPlugin2(t, dir, name, "dialog", mode, extra)
	setProtocol(t, dir, name, 12)
}

// setProtocol rewrites the installed plugin's manifest to protocol p.
func setProtocol(t *testing.T, dir, name string, p int) {
	t.Helper()
	path := filepath.Join(dir, name, "plugin.toml")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	src := strings.Replace(string(b), "protocol = 2\n", "protocol = "+strconv.Itoa(p)+"\n", 1)
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pointers are the plugin's pointer lines, each "<kind> <x>,<y>", in the order it said them.
func pointers(t *testing.T, logs, name string) []string {
	t.Helper()
	var out []string
	for _, s := range said(t, logs, name) {
		if rest, ok := strings.CutPrefix(s, "pointer "); ok {
			f := strings.Fields(rest) // kind button x,y xN
			out = append(out, f[0]+" "+f[2])
		}
	}
	return out
}

// kinds are the pointer lines' kinds.
func kinds(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i], _, _ = strings.Cut(l, " ")
	}
	return out
}

// mouse is a left-button event of kind at (x, y): a motion with the button held is a drag.
func mouse(kind tuicore.MouseKind, x, y int) tuicore.Event {
	return tuicore.MouseEvent{Kind: kind, Button: tuicore.MouseLeft, X: x, Y: y}
}

// xOf is a pointer line's x.
func xOf(t *testing.T, line string) int {
	t.Helper()
	_, at, _ := strings.Cut(line, " ")
	xs, _, _ := strings.Cut(at, ",")
	x, err := strconv.Atoi(xs)
	if err != nil {
		t.Fatalf("pointer line %q: %v", line, err)
	}
	return x
}

// openCard12 runs the TUI on dir, opens the card and waits for its first frame, and returns where
// its text starts on the screen.
func openCard12(t *testing.T, dir string) (*running, string, int, int) {
	t.Helper()
	_, r, logs := cardTUI(t, dir, nil)
	r.openPlugin("card")
	r.waitShown(t, "open ")
	x, y := r.textAt("open ")
	return r, logs, x, y
}

// typedIntoThePage presses on the page at (x, y) and types Q: the page takes the press and the
// keys, so no stale gesture holds the pointer.
func typedIntoThePage(t *testing.T, r *running, x, y int) {
	t.Helper()
	r.keys(t, mouse(tuicore.MousePress, x, y), mouse(tuicore.MouseRelease, x, y))
	r.keys(t, key('i'), key('Q'))
	r.s.WaitFor(t, "the page takes the keys", func(string) bool { return strings.Contains(r.editorText(), "Q") })
}

// A protocol-12 card is sent its press, a drag only when the pointer reaches another cell (several
// motions within one cell send one), drags off the view at the view's own (negative) coordinates,
// and one release there; no cancel. The mutants, named here: no capture on the press (no drag
// reaches the card once the pointer leaves it), and a drag sent for every motion (the cell's
// repeats are sent).
func TestAProtocol12CardsGestureIsAPressItsDragsAndOneRelease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", "")
	r, logs, x, y := openCard12(t, dir)
	// one motion at a time, each waited for: motions injected together are coalesced, the last kept
	r.keys(t, mouse(tuicore.MousePress, x, y))
	waitSaid(t, logs, "card", "pointer press")
	r.keys(t, mouse(tuicore.MouseMotion, x+1, y))
	waitCount(t, logs, "card", "pointer drag left 1,0 x1", 1)
	r.keys(t, mouse(tuicore.MouseMotion, x+1, y)) // the same cell again: nothing
	r.keys(t, mouse(tuicore.MouseMotion, x-30, y))
	waitSaid(t, logs, "card", "pointer drag left -30")
	r.keys(t, mouse(tuicore.MouseRelease, x-30, y))
	waitSaid(t, logs, "card", "pointer release")
	got := pointers(t, logs, "card")
	if k := kinds(got); !slices.Equal(k, []string{"press", "drag", "drag", "release"}) {
		t.Fatalf("the card was sent %q, want a press, a drag to the next cell, a drag off the view, one release", got)
	}
	if px, rx := xOf(t, got[0]), xOf(t, got[3]); px < 0 || rx != px-30 {
		t.Errorf("the press at x %d, the release at %d: want the release 30 cells left of it, off the view, unclamped (%q)", px, rx, got)
	}
	gestureCountsOne(t, logs, "card")
	typedIntoThePage(t, r, 5, 5)
}

// A protocol-12 card's panel closed mid-gesture (Esc) ends the gesture with one cancel, never a
// release; shown again, plain motion over it sends no drag, and its next press and release are a
// press and a release, with no stray cancel. The mutant, named here: the capture's loss sending a
// release.
func TestAProtocol12CardClosedMidGestureIsSentOneCancel(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", "")
	r, logs, x, y := openCard12(t, dir)
	r.keys(t, mouse(tuicore.MousePress, x, y), mouse(tuicore.MouseMotion, x+1, y))
	waitSaid(t, logs, "card", "pointer drag")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyEscape})
	r.s.WaitFor(t, "the card's panel closed", func(string) bool { return !r.panelShown("card") })
	waitSaid(t, logs, "card", "pointer cancel")
	r.openPlugin("card")
	r.s.WaitFor(t, "the card's panel again", func(string) bool { return r.panelShown("card") })
	waitSaid(t, logs, "card", "show")
	r.waitShown(t, "open ")
	x, y = r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MouseMotion, Button: tuicore.MouseNone, X: x + 2, Y: y},
		mouse(tuicore.MousePress, x, y), mouse(tuicore.MouseRelease, x, y))
	r.s.WaitFor(t, "the second release", func(string) bool {
		return slices.Equal(kinds(pointers(t, logs, "card")), []string{"press", "drag", "cancel", "press", "release"})
	})
	got := pointers(t, logs, "card")
	if _, drag, _ := strings.Cut(got[1], " "); got[2] != "cancel "+drag {
		t.Errorf("the cancel %q, want it at the last place the gesture was, %s", got[2], drag)
	}
	gestureCountsOne(t, logs, "card")
}

// gestureCountsOne fails when a drag, release or cancel the card was sent carried a count other
// than 1 (the echo plugin says "pointer <kind> <button> <x>,<y> x<count>").
func gestureCountsOne(t *testing.T, logs, name string) {
	t.Helper()
	for _, s := range said(t, logs, name) {
		for _, k := range []string{"pointer drag ", "pointer release ", "pointer cancel "} {
			if strings.HasPrefix(s, k) && !strings.HasSuffix(s, " x1") {
				t.Errorf("%q: a drag, release or cancel carries a count of 1", s)
			}
		}
	}
}

// midGesture opens a protocol-12 card and leaves it pressed and dragged one cell: its view holds the
// pointer. It returns where the drag is, view-local, as the card was told.
func midGesture(t *testing.T) (*running, string, string) {
	t.Helper()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", "")
	r, logs, x, y := openCard12(t, dir)
	r.keys(t, mouse(tuicore.MousePress, x, y))
	waitSaid(t, logs, "card", "pointer press")
	r.keys(t, mouse(tuicore.MouseMotion, x+1, y))
	waitSaid(t, logs, "card", "pointer drag")
	got := pointers(t, logs, "card")
	_, at, _ := strings.Cut(got[len(got)-1], " ")
	return r, logs, at
}

// oneCancelAt waits for the card's gesture to end in exactly one cancel, at, and no release.
func oneCancelAt(t *testing.T, logs, at string) {
	t.Helper()
	waitSaid(t, logs, "card", "pointer cancel")
	got := pointers(t, logs, "card")
	if n := slices.Index(kinds(got), "cancel"); n < 0 || got[n] != "cancel "+at || slices.Contains(kinds(got), "release") {
		t.Fatalf("the card was sent %q, want one cancel at %s and no release", got, at)
	}
	if c := strings.Count(strings.Join(kinds(got), " "), "cancel"); c != 1 {
		t.Fatalf("the card was sent %d cancels, want one: %q", c, got)
	}
}

// A right press while a protocol-12 card's button is down opens its menu: the host takes the
// pointer back, so the gesture ends in one cancel at its last place, and the menu works: its Close
// hides the panel. The mutant, named here: the capture's loss sending nothing (no cancel).
func TestARightPressMidGestureCancelsItAndOpensTheMenu(t *testing.T) {
	t.Parallel()
	r, logs, at := midGesture(t)
	x, y := r.textAt("open ")
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseRight, X: x + 2, Y: y + 1},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseRight, X: x + 2, Y: y + 1})
	r.s.WaitForText(t, "Close (Esc)")
	oneCancelAt(t, logs, at)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyDown}, enter())
	waitSaid(t, logs, "card", "hide")
}

// A backend's pointer cancel (the window lost the pointer) mid-gesture ends it in one cancel at its
// last place; the next press and release reach the card as a press and a release. The mutant, named
// here: the capture's loss sending a release.
func TestABackendPointerCancelMidGestureIsOneCancel(t *testing.T) {
	t.Parallel()
	r, logs, at := midGesture(t)
	r.keys(t, tuicore.PointerCancelEvent{})
	oneCancelAt(t, logs, at)
	x, y := r.textAt("open ")
	r.click(t, x, y)
	r.s.WaitFor(t, "the next press and release", func(string) bool {
		k := kinds(pointers(t, logs, "card"))
		return len(k) >= 2 && k[len(k)-2] == "press" && k[len(k)-1] == "release"
	})
}

// F6 mid-gesture takes the keyboard from the card (a protocol-12 card leaves it to the Window), and
// the focus leaving ends the gesture in one cancel at its last place.
func TestF6MidGestureIsOneCancel(t *testing.T) {
	t.Parallel()
	r, logs, at := midGesture(t)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF6})
	oneCancelAt(t, logs, at)
}

// A protocol-12 card's plugin stopped mid-gesture gives the pointer back as it stops: the view the
// run leaves holds no gesture and no capture once the stop returns, in the same turn, so no press
// of the user's reaches the dead view; a press on the page then takes the keyboard, and typing
// reaches the page. The stop moves the focus off the card first, and golib ends the capture as
// focus leaves its owner, so detach's own end of the gesture finds none here: it guards a view
// holding a gesture without the focus, which no path makes (a mutant removing it survives).
func TestAProtocol12CardStoppedMidGestureGivesThePointerBack(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", "")
	r, logs, x, y := openCard12(t, dir)
	r.keys(t, mouse(tuicore.MousePress, x, y), mouse(tuicore.MouseMotion, x+1, y))
	waitSaid(t, logs, "card", "pointer drag")
	if held := onLoop(r, func() bool {
		run := r.h.plugins.running["card"]
		v := run.view
		run.stopFor(plugin.CloseOff, "")
		return v.dragRun != nil || v.Context() != nil && v.Context().HasPointerCapture()
	}); held {
		t.Fatal("the view its run left still holds the gesture")
	}
	r.s.WaitFor(t, "the card stopped", func(string) bool {
		return onLoop(r, func() bool { return r.h.plugins.running["card"] == nil })
	})
	typedIntoThePage(t, r, 5, 5)
}

// fakeSurface is a pixel surface that does nothing: a run showing it reads the pointer as the
// surface's (surfaceMode).
type fakeSurface struct{}

func (fakeSurface) Resize(surfaceGeometry) {}
func (fakeSurface) Close()                 {}

// A pixel surface shown while a protocol-12 card's button is down takes nothing of the gesture: its
// drag and its one release still reach the card, and the pointer is given back. The mutant, named
// here: the surface's pointer read before the gesture (the release goes to the surface, and the
// capture is never given back).
func TestASurfaceShownMidGestureLeavesTheGestureToTheCard(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", "")
	r, logs, x, y := openCard12(t, dir)
	r.keys(t, mouse(tuicore.MousePress, x, y))
	waitSaid(t, logs, "card", "pointer press")
	onLoop(r, func() bool { r.h.plugins.running["card"].surface = fakeSurface{}; return true })
	r.keys(t, mouse(tuicore.MouseMotion, x+1, y), mouse(tuicore.MouseRelease, x+1, y))
	waitSaid(t, logs, "card", "pointer release")
	onLoop(r, func() bool { r.h.plugins.running["card"].surface = nil; return true })
	if k := kinds(pointers(t, logs, "card")); !slices.Equal(k, []string{"press", "drag", "release"}) {
		t.Fatalf("the card was sent %q, want its press, drag and release", k)
	}
	typedIntoThePage(t, r, 5, 5)
}

// A card below protocol 12 takes no capture and is sent no drag or release: its press, then its
// wheel. A press of it whose release never comes (a terminal can lose one) leaves the pointer
// free, so the user's next press on the page reaches the page and takes the keyboard. The
// mutant, named here: the gesture taken whatever the card's protocol (the SDK's Serve drops what
// a protocol-11 plugin would be sent, so the card's log can't show it: the capture it keeps takes
// the page's press).
func TestACardBelowProtocol12TakesNoGesture(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
	setProtocol(t, dir, "card", 11)
	r, logs, x, y := openCard12(t, dir)
	r.keys(t, mouse(tuicore.MousePress, x, y), mouse(tuicore.MouseMotion, x+1, y), mouse(tuicore.MouseRelease, x+1, y),
		tuicore.MouseEvent{Kind: tuicore.MouseWheel, Button: tuicore.WheelDown, X: x, Y: y, Count: 1})
	waitSaid(t, logs, "card", "pointer wheel")
	if k := kinds(pointers(t, logs, "card")); !slices.Equal(k, []string{"press", "wheel"}) {
		t.Fatalf("a protocol-11 card was sent %q, want its press and its wheel", k)
	}
	r.keys(t, mouse(tuicore.MousePress, x, y)) // its release lost
	waitSaidN(t, logs, "card", "pointer press", 2)
	typedIntoThePage(t, r, 5, 5)
}

// controlAt is where the card's window control glyph (_, + or x) is on its frame's title row.
func controlAt(t *testing.T, r *running, title string, glyph rune) (x, y int) {
	t.Helper()
	r.s.WaitFor(t, "the card's "+string(glyph)+" control", func(sc string) bool {
		top, left, right, _, found := frameOf(sc, title)
		if !found {
			return false
		}
		row := []rune(strings.Split(sc, "\n")[top])
		if right < 0 || right >= len(row) {
			right = len(row) - 1
		}
		for i := right - 1; i > left+1; i-- {
			if row[i] == glyph && row[i-1] == ' ' && row[i+1] == ' ' {
				x, y = i, top
				return true
			}
		}
		return false
	})
	return x, y
}

// click is a left press and release at (x, y).
func (r *running) click(t *testing.T, x, y int) {
	t.Helper()
	r.keys(t, mouse(tuicore.MousePress, x, y), mouse(tuicore.MouseRelease, x, y))
}

// clickControl clicks the card's window control glyph on its frame titled title.
func (r *running) clickControl(t *testing.T, title string, glyph rune) {
	t.Helper()
	x, y := controlAt(t, r, title, glyph)
	r.click(t, x, y)
}

// A protocol-12 card that lists hide, maximize and close shows _ + x on its frame. _ hides its
// panel, its plugin running on (told plugin.hide); + maximizes the panel over the page, and + again
// restores it; x stops its plugin. The mutants, named here: the icons' requests not wired to
// cardWindow (nothing happens), and _ stopping the plugin.
func TestAProtocol12CardsIconsHideMaximizeAndStopIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", `"hide", "maximize", "close"`)
	r, logs, _, _ := openCard12(t, dir)
	id := cardPanelID("card")
	running := func() bool { return onLoop(r, func() bool { return r.h.plugins.running["card"] != nil }) }

	r.clickControl(t, "Card", '_')
	waitSaid(t, logs, "card", "hide")
	r.s.WaitFor(t, "the card's panel hidden", func(string) bool { return !r.panelShown("card") })
	if !running() {
		t.Fatal("_ stopped the plugin: it hides the panel, the plugin running on")
	}

	r.openPlugin("card")
	waitSaid(t, logs, "card", "show")
	r.waitShown(t, "open ")
	before := boundsOf(r, id)
	r.clickControl(t, "Card", '+')
	r.s.WaitFor(t, "the card's panel maximized", func(string) bool { return boundsOf(r, id).W > before.W })
	r.clickControl(t, "Card", '+')
	r.s.WaitFor(t, "the card's panel restored", func(string) bool { return boundsOf(r, id) == before })

	r.clickControl(t, "Card", 'x')
	r.s.WaitFor(t, "the card's plugin stopped", func(string) bool { return !running() })
}

// A protocol-12 card's frame shows only the icons it lists, and a card that lists none shows none:
// its title row ends in its border. A control it does not list is refused, however it is asked
// for (App.cardWindow is reached from the host's own document alone today: a guard); the one it
// lists acts. The mutants, named here: cardWindowQML writing every control, and cardWindow acting
// on any control it is handed (the card hidden).
func TestACardShowsOnlyTheIconsItLists(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", `"close"`)
	r, _, _, _ := openCard12(t, dir)
	controlAt(t, r, "Card", 'x')
	top, left, right, _, _ := frameOf(r.s.String(), "Card")
	row := []rune(strings.Split(r.s.String(), "\n")[top])
	if right < 0 || right >= len(row) {
		right = len(row) - 1
	}
	if seg := string(row[left:right]); strings.Contains(seg, " _ ") || strings.Contains(seg, " + ") {
		t.Fatalf("a card listing close alone shows %q", seg)
	}
	// a control it does not list is refused however it is asked for; the one it lists acts
	before := boundsOf(r, cardPanelID("card"))
	onLoop(r, func() bool {
		r.h.plugins.cardWindow("card", "hide")
		r.h.plugins.cardWindow("card", "maximize")
		return true
	})
	if !r.panelShown("card") || boundsOf(r, cardPanelID("card")) != before ||
		onLoop(r, func() bool { return r.h.plugins.running["card"] == nil }) {
		t.Fatal("a card listing close alone was hidden, maximized or stopped by a control it does not list")
	}
	onLoop(r, func() bool { r.h.plugins.cardWindow("card", "close"); return true })
	r.s.WaitFor(t, "the card stopped by its close", func(string) bool {
		return onLoop(r, func() bool { return r.h.plugins.running["card"] == nil })
	})
}

// F6 on a protocol-12 card is the Window's: the card is not sent it, and the keyboard leaves it
// (told focus false). F11 maximizes its panel when it lists maximize, and the card is not sent it.
// The mutants, named here: the protocol-12 card sent every key (F6 and F11 reach it), and
// CardHostKeys leaving out maximize's keys.
func TestAProtocol12CardLeavesF6AndItsMaximizeToTheWindow(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", `"maximize"`)
	r, logs, x, y := openCard12(t, dir)
	id := cardPanelID("card")
	r.click(t, x, y)
	waitSaid(t, logs, "card", "focus true")
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF6})
	waitSaid(t, logs, "card", "focus false")
	// the keyboard back to the card, as its command gives it (F6 may have opened a panel over it)
	onLoop(r, func() bool { r.h.plugins.cards["card"].view.RequestFocus(); return true })
	waitCount(t, logs, "card", "focus true", 2)
	before := boundsOf(r, id)
	r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF11})
	r.s.WaitFor(t, "the card's panel maximized by F11", func(string) bool { return boundsOf(r, id).W > before.W })
	for _, s := range said(t, logs, "card") {
		if strings.HasPrefix(s, "key F6") || strings.HasPrefix(s, "key F11") {
			t.Fatalf("the card was sent %q: the Window's keys", s)
		}
	}
}

// A protocol-12 card that does not list maximize is sent F11, and still leaves F6; a card below
// protocol 12 is sent both, as before. The mutant, named here: hostKey ignoring the protocol (the
// protocol-11 card loses F6).
func TestACardKeepsF11WithoutMaximizeAndEverythingBelowProtocol12(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		protocol int
		wantF6   bool
	}{{"twelve", 12, false}, {"eleven", 11, true}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			installPlugin2(t, dir, "card", "dialog", "echo", rightCard)
			setProtocol(t, dir, "card", c.protocol)
			r, logs, x, y := openCard12(t, dir)
			r.click(t, x, y)
			waitSaid(t, logs, "card", "focus true")
			r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF11})
			waitSaid(t, logs, "card", "key F11")
			r.keys(t, tuicore.KeyEvent{Kind: tuicore.KeyPress, Code: tuicore.KeyF6})
			if c.wantF6 {
				waitSaid(t, logs, "card", "key F6")
			} else {
				waitSaid(t, logs, "card", "focus false")
				if slices.ContainsFunc(said(t, logs, "card"), func(s string) bool { return strings.HasPrefix(s, "key F6") }) {
					t.Fatal("a protocol-12 card was sent F6")
				}
			}
		})
	}
}

// SPC z on the page maximizes a protocol-12 card that lists maximize, the panel opened most recently
// that Maximize reaches, as it does the terminal's. The mutant, named here: maximizablePanels
// without the cards (SPC z says only the terminal and the agents maximize).
func TestSpcZOnThePageMaximizesACardThatListsMaximize(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	installCard12(t, dir, "card", "echo", `"maximize"`)
	r, _, _, _ := openCard12(t, dir)
	id := cardPanelID("card")
	before := boundsOf(r, id)
	r.click(t, 5, 5)
	r.keys(t, key(' '), key('z'))
	r.s.WaitFor(t, "the card's panel maximized by SPC z", func(string) bool { return boundsOf(r, id).W > before.W })
}

// A protocol-12 card's icons are its frame's window controls, the Box's TitleWindowControls that
// golib's gui painter draws as the title bar's dots (golib gui's TestABoxsWindowControlsAreTheTitleBarsDots
// pins the painting; a decltest screen places no native views, so this reads what the painter
// reads): one per control the manifest lists, in the order they are drawn, whatever order it lists
// them in. In the terminal and the window builds alike.
func runACardsIconsAreItsFramesWindowControls(t *testing.T, gui bool) {
	dir := t.TempDir()
	installPlugin2(t, dir, "card", "dialog", "echo", rightCard+"[window]\ncontrols = [\"close\", \"hide\", \"maximize\"]\n")
	setProtocol(t, dir, "card", 12)
	_, r, _ := liveCardTUI(t, dir, nil, gui)
	r.openPlugin("card")
	r.s.WaitFor(t, "the card's panel", func(string) bool { return r.panelShown("card") })
	want := []widgetcore.WindowOperation{widgetcore.WindowMinimize, widgetcore.WindowMaximize, widgetcore.WindowClose}
	r.s.WaitFor(t, "the card frame's window controls", func(string) bool {
		return onLoop(r, func() bool {
			c := r.h.plugins.cards["card"]
			if c == nil || c.view == nil || c.view.Context() == nil {
				return false
			}
			box, ok := c.view.Context().Ancestor(func(x tuicore.Component) bool { _, ok := x.(*widgetcore.Box); return ok }).(*widgetcore.Box)
			if !ok {
				return false
			}
			var got []widgetcore.WindowOperation
			for _, ctl := range box.TitleWindowControls() {
				got = append(got, ctl.Op)
			}
			return slices.Equal(got, want)
		})
	})
}

func TestACardsIconsAreItsFramesWindowControls(t *testing.T) {
	t.Parallel()
	runACardsIconsAreItsFramesWindowControls(t, false)
}

func TestACardsIconsAreItsFramesWindowControlsGUI(t *testing.T) {
	t.Parallel()
	runACardsIconsAreItsFramesWindowControls(t, true)
}
