package decl

import (
	"strings"
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/controls"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// THE DIVIDER, DRAGGED (protocol 12): a press on a Panes' divider, the drag and its release move it,
// as golib's Split moves it under its pointer capture; a release off the card ends the drag at the
// nearest place the divider may be; a cancel ends it where it is; and below 12 a press alone never
// starts one.

// dragPanes opens, at protocol, a card of two panes side by side, Left and Right, w columns wide.
func dragPanes(t *testing.T, protocol, w int) (*Card, *fakeHost) {
	t.Helper()
	c := New(Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(layoutHead+`Panes { Pane { title: "Left"; Text { text: "l" } }
  Pane { title: "Right"; Text { text: "r" } } } }`)),
		tuidecl.Singleton("hello", "1.0", "App"),
		tuidecl.Types(controls.Types()...),
	}})
	host := &fakeHost{}
	c.open(host, nil, plugin.Open{Protocol: protocol, Width: w, Height: 8, Theme: themeA})
	t.Cleanup(c.Close)
	lastHas(t, host, "Right")
	return c, host
}

// dividerAt is the divider's column in the latest frame: the middle of "│││", the left pane's right
// border, the divider and the right pane's left border, on the row given; -1 when there is none.
func dividerAt(host *fakeHost, row int) int {
	_, frames, _ := host.snapshot()
	if len(frames) == 0 {
		return -1
	}
	rows := strings.Split(text(frames[len(frames)-1]), "\n")
	if row >= len(rows) {
		return -1
	}
	i := strings.Index(rows[row], "│││")
	if i < 0 {
		return -1
	}
	return len([]rune(rows[row][:i])) + 1
}

// waitDivider waits for the divider to be at x.
func waitDivider(t *testing.T, host *fakeHost, what string, x int) {
	t.Helper()
	waitFor(t, what, func() bool { return dividerAt(host, 3) == x })
}

// oneMoreFrame sends F7, which moves the focused pane and so its frame's look, and waits for the
// frame after it: every event sent before it has been handled by then, the program's events being
// handled in order.
func oneMoreFrame(t *testing.T, c *Card, host *fakeHost) {
	t.Helper()
	_, before, _ := host.snapshot()
	c.Key(plugin.Key{Key: "F7"})
	waitFor(t, "a frame after F7", func() bool { _, frames, _ := host.snapshot(); return len(frames) > len(before) })
}

func ptr(kind string, x int) plugin.Pointer {
	return plugin.Pointer{Kind: kind, Button: "left", X: x, Y: 3, Count: 1}
}

// At protocol 12 the divider follows a drag and stays where its release leaves it; a release off
// the card ends the drag at the left pane's least width; and a cancel mid-drag ends it where it had
// reached, as golib's Split keeps a revoked drag's division. A drag after the release or the cancel
// moves nothing.
func TestTheDividerFollowsADragAtProtocol12(t *testing.T) {
	c, host := dragPanes(t, 12, 41)
	waitDivider(t, host, "the divider at the half", 20)

	c.Pointer(ptr("press", 20))
	c.Pointer(ptr("drag", 12))
	waitDivider(t, host, "the divider following the drag to 12", 12)
	c.Pointer(ptr("release", 12))
	c.Pointer(ptr("drag", 30)) // no press: no drag
	oneMoreFrame(t, c, host)
	waitDivider(t, host, "the divider left at 12 by its release", 12)

	c.Pointer(ptr("press", 12))
	c.Pointer(ptr("drag", -9))
	c.Pointer(ptr("release", -9)) // off the card
	waitDivider(t, host, "a release off the card: the left pane's least width", 3)

	c.Pointer(ptr("press", 3))
	c.Pointer(ptr("drag", 25))
	waitDivider(t, host, "the divider following to 25", 25)
	c.Pointer(plugin.Pointer{Kind: "cancel", Button: "left", X: 25, Y: 3, Count: 1})
	c.Pointer(ptr("drag", 33))
	oneMoreFrame(t, c, host)
	waitDivider(t, host, "a cancel: the divider where it had reached", 25)
}

// Below 12 a press on the divider is a whole click (a press and its release), and the host sends no
// drag: the divider stays where it is, however the press lands.
func TestTheDividerDoesNotMoveBelowProtocol12(t *testing.T) {
	c, host := dragPanes(t, 11, 41)
	waitDivider(t, host, "the divider at the half", 20)
	c.Pointer(ptr("press", 20))
	c.Pointer(ptr("drag", 12)) // not sent by a host below 12; read as nothing if it were
	oneMoreFrame(t, c, host)
	waitDivider(t, host, "the divider still at the half", 20)
}

// The ratio a document gives a Panes places its divider, and a drag moves it from there.
func TestADocumentsRatioPlacesTheDivider(t *testing.T) {
	c := New(Options{Program: []tuidecl.ProgramOption{
		tuidecl.LayoutSource("card.qml", []byte(layoutHead+`Panes { ratio: 0.25; Pane { title: "Left"; Text { text: "l" } }
  Pane { title: "Right"; Text { text: "r" } } } }`)),
		tuidecl.Singleton("hello", "1.0", "App"),
		tuidecl.Types(controls.Types()...),
	}})
	host := &fakeHost{}
	c.open(host, nil, plugin.Open{Protocol: 12, Width: 41, Height: 8, Theme: themeA})
	t.Cleanup(c.Close)
	waitDivider(t, host, "the divider at a quarter", 10)
	c.Pointer(ptr("press", 10))
	c.Pointer(ptr("drag", 30))
	c.Pointer(ptr("release", 30))
	waitDivider(t, host, "the divider dragged to 30", 30)
}
