package plugin

import "testing"

// The pointer wire carries a card-local cell and bounded wheel steps; malformed events cannot
// reach a plugin as an accidental click on its selected row.
func TestProtocolSixPointerRoundTripAndBounds(t *testing.T) {
	for _, p := range []Pointer{
		{Kind: "press", Button: "left", X: 2, Y: 9, Count: 1},
		{Kind: "wheel", Button: "down", X: 4, Y: 5, Count: 3},
	} {
		got, err := ReadPointer(PointerParams(p))
		if err != nil || got != p {
			t.Errorf("pointer %+v became %+v (%v)", p, got, err)
		}
	}
	for _, p := range []Pointer{
		{Kind: "press", Button: "right", X: 2, Y: 9, Count: 1},
		{Kind: "wheel", Button: "left", X: 2, Y: 9, Count: 1},
		{Kind: "press", Button: "left", X: -1, Y: 9, Count: 1},
		{Kind: "wheel", Button: "down", X: 2, Y: 9, Count: 17},
	} {
		if _, err := ReadPointer(PointerParams(p)); err == nil {
			t.Errorf("malformed pointer %+v was accepted", p)
		}
	}
}
