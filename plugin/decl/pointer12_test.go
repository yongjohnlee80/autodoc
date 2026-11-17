package decl

import (
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/decl"
	"github.com/yongjohnlee80/golib/parse/qml"
	"github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// PROTOCOL 12'S POINTER IN A DECL CARD: the card reads a pointer event by the protocol it was opened
// with. From 12 a press is a press alone, its release follows, a drag is the button's motion and a
// cancel is golib's PointerCancelEvent: a Button clicks only on a release inside it, and a cancel
// clicks nothing. Below 12 a press is a whole click, and a drag, release or cancel is nothing.

// mouseEvents at 12 is the press alone, the drag a motion, the release a release, the cancel
// golib's PointerCancelEvent, each at the coordinates it came with; below 12, a drag, release or
// cancel is nothing. The mutant, named here: decl translating by the Protocol constant (a card opened
// at 11 by a newer kit takes a release as a second click).
func TestMouseEventsReadByTheProtocolTheCardWasOpenedWith(t *testing.T) {
	press := mouseEvents(plugin.Pointer{Kind: "press", Button: "left", X: 3, Y: 1, Count: 1}, 12)
	if len(press) != 1 || press[0].(tui.MouseEvent).Kind != tui.MousePress {
		t.Errorf("a protocol-12 press = %+v, want the press alone", press)
	}
	if e := mouseEvents(plugin.Pointer{Kind: "drag", Button: "left", X: -2, Y: 9, Count: 1}, 12); len(e) != 1 ||
		e[0] != (tui.MouseEvent{Kind: tui.MouseMotion, Button: tui.MouseLeft, X: -2, Y: 9}) {
		t.Errorf("a protocol-12 drag = %+v, want the left button's motion where it came", e)
	}
	if e := mouseEvents(plugin.Pointer{Kind: "release", Button: "left", X: 40, Y: -1, Count: 1}, 12); len(e) != 1 ||
		e[0] != (tui.MouseEvent{Kind: tui.MouseRelease, Button: tui.MouseLeft, X: 40, Y: -1}) {
		t.Errorf("a protocol-12 release = %+v, want the left release where it came", e)
	}
	if e := mouseEvents(plugin.Pointer{Kind: "cancel", Button: "left", Count: 1}, 12); len(e) != 1 {
		t.Errorf("a protocol-12 cancel = %+v, want golib's PointerCancelEvent", e)
	} else if _, ok := e[0].(tui.PointerCancelEvent); !ok {
		t.Errorf("a protocol-12 cancel = %T, want tui.PointerCancelEvent", e[0])
	}
	for _, kind := range []string{"drag", "release", "cancel"} {
		if e := mouseEvents(plugin.Pointer{Kind: kind, Button: "left", Count: 1}, 11); e != nil {
			t.Errorf("a %s at protocol 11 = %+v, want nothing", kind, e)
		}
	}
	if e := mouseEvents(plugin.Pointer{Kind: "press", Button: "left", X: 3, Y: 1, Count: 1}, 11); len(e) != 2 {
		t.Errorf("a protocol-11 press = %+v, want a press and its release", e)
	}
}

// clickCard opens, at protocol 12, a card of two Buttons, aaaa above bbbb, each telling its clicks.
func clickCard(t *testing.T) (*Card, *fakeHost, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var clicks []string
	c := New(Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(layoutHead+` Flex { direction: Tui.Vertical
  Button { text: "aaaa"; onClicked: App.click("a") }
  Button { text: "bbbb"; onClicked: App.click("b") } } }`)),
		tuidecl.Singleton("hello", "1.0", "App"),
		tuidecl.Handlers(map[string]decl.HandlerFunc{"App.click": func(args []qml.SpecValue) error {
			mu.Lock()
			clicks = append(clicks, args[0].Raw)
			mu.Unlock()
			return nil
		}}),
	}})
	host := &fakeHost{}
	c.open(host, nil, plugin.Open{Protocol: 12, Width: 30, Height: 6, Theme: themeA})
	t.Cleanup(c.Close)
	lastHas(t, host, "bbbb")
	return c, host, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), clicks...)
	}
}

// at is where text starts in the card's last frame.
func at(t *testing.T, host *fakeHost, s string) (x, y int) {
	t.Helper()
	f := lastHas(t, host, s)
	for row, line := range strings.Split(text(f), "\n") {
		if i := strings.Index(line, s); i >= 0 {
			return len([]rune(line[:i])), row
		}
	}
	t.Fatalf("%q is not in the frame", s)
	return 0, 0
}

// A protocol-12 decl card clicks a Button only on a release inside it: a release off it, or off the
// view (negative coordinates), clicks nothing, nor does a cancel; a drag out and back before the
// release inside clicks once. A later click on the other Button says every gesture before it was
// handled. The mutants, named here: a protocol-12 press translated as a press and its release (the
// first press clicks at once), and a cancel translated as a release where the pointer was.
func TestAProtocol12DeclCardClicksOnlyOnAReleaseInside(t *testing.T) {
	c, host, clicks := clickCard(t)
	ax, ay := at(t, host, "aaaa")
	bx, by := at(t, host, "bbbb")
	p := func(kind string, x, y int) plugin.Pointer {
		return plugin.Pointer{Kind: kind, Button: "left", X: x, Y: y, Count: 1}
	}
	// released off the Button, then off the view
	c.Pointer(p("press", ax+1, ay))
	c.Pointer(p("release", ax+1, ay+4))
	c.Pointer(p("press", ax+1, ay))
	c.Pointer(p("drag", -5, ay))
	c.Pointer(p("release", -5, ay))
	// cancelled where it was pressed
	c.Pointer(p("press", ax+1, ay))
	c.Pointer(p("cancel", ax+1, ay))
	// dragged out and back, released inside: one click
	c.Pointer(p("press", ax+1, ay))
	c.Pointer(p("drag", ax+1, ay+4))
	c.Pointer(p("drag", ax+2, ay))
	c.Pointer(p("release", ax+2, ay))
	// the sentinel
	c.Pointer(p("press", bx+1, by))
	c.Pointer(p("release", bx+1, by))
	waitFor(t, "bbbb clicked", func() bool {
		got := clicks()
		return len(got) > 0 && got[len(got)-1] == "b"
	})
	if got := clicks(); len(got) != 2 || got[0] != "a" {
		t.Fatalf("clicks %q, want one on aaaa (the release inside after the drag back) then the sentinel's", got)
	}
}
