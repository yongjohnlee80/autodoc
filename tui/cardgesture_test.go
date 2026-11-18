package tui

import (
	"slices"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// A PROTOCOL-12 CARD'S GESTURE ENDED BY THE VIEW ITSELF (pluginview.go endDrag): a press with one
// in hand. The capture's loss is cardwindow_test.go's.

// A left press while a protocol-12 card's gesture is held, its release lost (a terminal can lose
// one), ends that gesture in one cancel at its last place, never a release, and begins the next:
// the card is sent a press, a drag, a cancel and a press, and the next release ends the new
// gesture. The mutant, named here: endDrag sending a release. No other cell reaches endDrag with a
// gesture held: on every other path the capture's loss ends the gesture first.
func TestAPressWithTheReleaseLostEndsTheGestureInOneCancel(t *testing.T) {
	t.Parallel()
	r, logs, at := midGesture(t)
	x, y := r.textAt("open ")
	r.keys(t, mouse(tuicore.MousePress, x+2, y))
	waitSaidN(t, logs, "card", "pointer press", 2)
	r.keys(t, mouse(tuicore.MouseRelease, x+2, y))
	waitSaid(t, logs, "card", "pointer release")
	got := pointers(t, logs, "card")
	if k := kinds(got); !slices.Equal(k, []string{"press", "drag", "cancel", "press", "release"}) {
		t.Fatalf("the card was sent %q, want a press, a drag, one cancel, then the next press and its release", got)
	}
	if got[2] != "cancel "+at {
		t.Errorf("the cancel %q, want it at the last place the gesture was, %s", got[2], at)
	}
	gestureCountsOne(t, logs, "card")
	typedIntoThePage(t, r, 5, 5)
}
