package plugin

import (
	"fmt"
	"reflect"
	"testing"
)

// PROTOCOL 12'S POINTER: a left press is followed by exactly one release or cancel, with the drags
// between, and their coordinates are the pointer's, off the view included.

// The wire carries a drag, a release and a cancel at any coordinates, negative ones included; a
// press and a wheel stay in the view, and only the left button drags. The mutants, named here:
// ReadPointer keeping nonnegative coordinates for every kind (an off-view release is refused), and
// taking a right drag.
func TestAProtocol12GestureCrossesTheWireAtAnyCoordinates(t *testing.T) {
	for _, p := range []Pointer{
		{Kind: "drag", Button: "left", X: -3, Y: 40, Count: 1},
		{Kind: "release", Button: "left", X: 80, Y: -1, Count: 1},
		{Kind: "cancel", Button: "left", X: 0, Y: 0, Count: 1},
	} {
		got, err := ReadPointer(PointerParams(p))
		if err != nil || got != p {
			t.Errorf("pointer %+v became %+v (%v)", p, got, err)
		}
	}
	for _, p := range []Pointer{
		{Kind: "drag", Button: "right", X: 2, Y: 9, Count: 1},
		{Kind: "release", Button: "up", X: 2, Y: 9, Count: 1},
		{Kind: "cancel", Button: "left", X: 2, Y: 9, Count: 0},
		{Kind: "hover", Button: "left", X: 2, Y: 9, Count: 1},
		{Kind: "wheel", Button: "down", X: -1, Y: 9, Count: 1},
	} {
		if _, err := ReadPointer(PointerParams(p)); err == nil {
			t.Errorf("malformed pointer %+v was accepted", p)
		}
	}
}

// pointing is a recorder that takes the pointer.
type pointing struct{ recorder }

func (p *pointing) Pointer(at Pointer) { p.add(fmt.Sprintf("pointer %s %d,%d", at.Kind, at.X, at.Y)) }

// Serve gives a plugin opened below 12 no drag, release or cancel, as before: a press is its whole
// click. (A plugin opened at 12, which gets them, is the raise to 12's cell: this SDK opens nothing
// newer than plugin.Protocol.) The mutant, named here: Serve's protocol-12 gate gone (a protocol-11
// plugin sees a release after every press, and would click twice).
func TestServePassesNoGestureBelowProtocol12(t *testing.T) {
	for _, c := range []struct {
		protocol int
		want     []string
	}{
		{11, []string{"open 10x5  ", "pointer press 1,1", "pointer wheel 1,1", "close"}},
	} {
		t.Run(fmt.Sprint(c.protocol), func(t *testing.T) {
			p := &pointing{}
			hs := start(t, p)
			hs.send(t, MethodOpen, OpenParams(Open{Protocol: c.protocol, Width: 10, Height: 5}))
			hs.next(t)
			for _, at := range []Pointer{
				{Kind: "press", Button: "left", X: 1, Y: 1, Count: 1},
				{Kind: "drag", Button: "left", X: -3, Y: 2, Count: 1},
				{Kind: "release", Button: "left", X: -3, Y: 2, Count: 1},
				{Kind: "cancel", Button: "left", Count: 1},
				{Kind: "wheel", Button: "up", X: 1, Y: 1, Count: 1},
			} {
				hs.send(t, MethodPointer, PointerParams(at))
			}
			hs.send(t, MethodClose, EmptyParams())
			if err := hs.served(t); err != nil {
				t.Fatal(err)
			}
			if got := p.all(); !reflect.DeepEqual(got, c.want) {
				t.Errorf("at protocol %d the plugin's calls:\n got %q\nwant %q", c.protocol, got, c.want)
			}
		})
	}
}
