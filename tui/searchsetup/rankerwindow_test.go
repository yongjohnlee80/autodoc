package searchsetup

import (
	"slices"
	"testing"
)

// The ranker's window field opens holding the window in use. Opened before ranker.list answered,
// it was an empty field the user could type in, and the answer then replaced what they typed. The
// answer is held here: the field is not open while it is, and opens once, with the window. Two
// openings answered in the other order open it once, for the later one. The mutant, named here:
// the field opened before the answer.
func TestTheWindowFieldOpensOnlyWithTheWindowInIt(t *testing.T) {
	c := newScripted("kb")
	a := NewAIModels(c)
	window := int64(40)
	c.Answers["ranker.list"] = func([]any) (any, error) {
		return map[string]any{"rankers": []any{}, "window": window}, nil
	}
	c.Deferred = true
	c.Opened = nil
	a.rankers.startWindow()
	if slices.Contains(c.Opened, "rankerWindow") {
		t.Fatalf("the window field opened before the window was read: what the user types there is replaced by the answer")
	}
	c.Deliver(0)
	if got := c.Sets["App.rankerWindow"]; !slices.Equal(c.Opened, []string{"rankerWindow"}) || got != "40" {
		t.Fatalf("once read: opened %v holding %v; want the field opened once, holding 40", c.Opened, got)
	}

	c.Opened = nil
	a.rankers.startWindow()
	window = 60
	a.rankers.startWindow()
	c.Deliver(1, 0) // the later opening's answer first
	if got := c.Sets["App.rankerWindow"]; !slices.Equal(c.Opened, []string{"rankerWindow"}) || got != "60" {
		t.Fatalf("two openings answered in the other order: opened %v holding %v; want once, holding 60", c.Opened, got)
	}
}
