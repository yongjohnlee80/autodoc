package tui

import (
	"math/rand"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// TestLineHunksTurnOneTextIntoTheOther: for random texts, the hunks applied from the last up, each
// as its blockEdit, turn the old text into the new one exactly, its last line break or none
// included; and a line both texts keep, alone between changed ones, is in no hunk.
func TestLineHunksTurnOneTextIntoTheOther(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	words := []string{"a", "b", "c", "d", ""}
	text := func() string {
		n := rng.Intn(8)
		lines := make([]string, n)
		for i := range lines {
			lines[i] = words[rng.Intn(len(words))]
		}
		s := strings.Join(lines, "\n")
		if rng.Intn(2) == 0 {
			s += "\n"
		}
		return s
	}
	for i := 0; i < 3000; i++ {
		before, after := text(), text()
		ed := widget.NewEditorCore(widget.CoreInitialText(before))
		old, now := ed.Lines(), strings.Split(after, "\n")
		hunks := lineHunks(old, now)
		for j := len(hunks) - 1; j >= 0; j-- {
			hk := hunks[j]
			r, s := blockEdit(old, hk.oldFrom, hk.oldTo, now[hk.newFrom:hk.newTo])
			if _, err := ed.Replace(r, s); err != nil {
				t.Fatalf("%q → %q: %v", before, after, err)
			}
		}
		if got := ed.Value(); got != after {
			t.Fatalf("%q → %q: hunks %v gave %q", before, after, hunks, got)
		}
	}
	// zero above, four below: three, between, is in no hunk
	hunks := lineHunks([]string{"one", "two", "three", ""}, []string{"zero", "one", "two", "three", "four", ""})
	for _, hk := range hunks {
		if hk.oldFrom <= 2 && 2 < hk.oldTo {
			t.Fatalf("hunks %v take in the line kept between the changes", hunks)
		}
	}
}
