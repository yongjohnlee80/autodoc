package plugin

import (
	"reflect"
	"testing"
)

// rowText is a frame row's runs, their texts run together.
func rowText(r Row) string {
	s := ""
	for _, run := range r {
		s += run.Text
	}
	return s
}

// TestAFramePlacesGraphemeClustersByWidth: each cluster is one character in as many cells as the
// screen gives it, sent once (its continuation left out), so the host's measure puts what follows
// in the right column.
func TestAFramePlacesGraphemeClustersByWidth(t *testing.T) {
	for _, c := range []struct {
		name, text string
		x          int    // Text's return
		row        string // the row as sent, 6 cells wide
	}{
		{"ascii", "ab", 2, "ab    "},
		{"cjk", "表a", 3, "表a   "},
		{"decomposed accent", "e\u0301a", 2, "e\u0301a    "},
		{"zwj emoji", "\U0001f468\u200d\U0001f469\u200d\U0001f467b", 3, "\U0001f468\u200d\U0001f469\u200d\U0001f467b   "},
		{"flag", "\U0001f1f0\U0001f1f7c", 3, "\U0001f1f0\U0001f1f7c   "},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := NewFrame(6, 1)
			if x := f.Text(0, 0, c.text, Style{}); x != c.x {
				t.Errorf("Text returned %d, want %d", x, c.x)
			}
			if got := rowText(f.Rows()[0]); got != c.row {
				t.Errorf("row = %q, want %q", got, c.row)
			}
		})
	}
}

// TestAWideClusterKeepsBothHalvesOrNeither: overwriting either half of a wide cell blanks the other,
// in its own style; a wide cluster at the right edge, or half off the left, is a space.
func TestAWideClusterKeepsBothHalvesOrNeither(t *testing.T) {
	red, blue := Style{FG: "red"}, Style{FG: "blue"}
	for _, c := range []struct {
		name string
		draw func(f *Frame)
		want []Row
	}{
		{"overwrite the continuation", func(f *Frame) { f.Text(0, 0, "表", red); f.Text(1, 0, "x", blue) },
			[]Row{{{Text: " ", Style: red}, {Text: "x", Style: blue}, {Text: "  "}}}},
		{"overwrite the head", func(f *Frame) { f.Text(0, 0, "表", red); f.Text(0, 0, "y", blue) },
			[]Row{{{Text: "y", Style: blue}, {Text: " ", Style: red}, {Text: "  "}}}},
		{"a wide over a wide, one cell on", func(f *Frame) { f.Text(0, 0, "表", red); f.Text(1, 0, "字", blue) },
			[]Row{{{Text: " ", Style: red}, {Text: "字", Style: blue}, {Text: " "}}}},
		{"at the right edge", func(f *Frame) {
			if x := f.Text(3, 0, "表", red); x != 5 {
				t.Errorf("Text at the edge returned %d, want 5", x)
			}
		}, []Row{{{Text: "   "}, {Text: " ", Style: red}}}},
		{"half off the left", func(f *Frame) { f.Text(-1, 0, "表b", red) },
			[]Row{{{Text: " b", Style: red}, {Text: "  "}}}},
		{"Set of a wide rune", func(f *Frame) { f.Set(0, 0, '表', red) },
			[]Row{{{Text: " ", Style: red}, {Text: "   "}}}},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := NewFrame(4, 1)
			c.draw(f)
			if got := f.Rows(); !reflect.DeepEqual(got, c.want) {
				t.Fatalf("Rows:\n got %#v\nwant %#v", got, c.want)
			}
		})
	}
}

// TestAZeroWidthClusterJoinsTheCellBefore: a combining mark written on its own joins the character
// before it, a wide one through its continuation; with nothing before it, it is a cell of its own.
func TestAZeroWidthClusterJoinsTheCellBefore(t *testing.T) {
	f := NewFrame(5, 1)
	f.Text(0, 0, "e", Style{})
	if x := f.Text(1, 0, "\u0301", Style{FG: "red"}); x != 1 {
		t.Errorf("a joined mark moved x to %d, want 1", x)
	}
	f.Text(1, 0, "表", Style{})
	f.Text(3, 0, "\u0301", Style{})
	if got, want := rowText(f.Rows()[0]), "e\u0301表\u0301  "; got != want {
		t.Fatalf("row = %q, want %q", got, want)
	}
	g := NewFrame(2, 1)
	if x := g.Text(0, 0, "\u0301", Style{}); x != 1 {
		t.Errorf("a mark with nothing before it took %d cells, want 1", x)
	}
}
