package tui

import "testing"

// TestAByteOffsetIsTheCursorsCluster: byteOffset is the editor's position as a byte offset into the
// document's text: the first byte of the cluster at the column, never inside one; a column past its
// line's end is the line's end, and a line past the text's end is the text's end — the cursor of a
// text the snapshot predates (the page's text is taken at the last pause, the cursor now).
func TestAByteOffsetIsTheCursorsCluster(t *testing.T) {
	const family = "\U0001F468‍\U0001F469‍\U0001F467" // one cluster, 18 bytes
	text := "é" + family + "x\n한국어;\n"
	for _, c := range []struct {
		line, col, want int
	}{
		{0, 0, 0}, {0, 1, 3}, {0, 2, 3 + len(family)}, {0, 3, 4 + len(family)},
		{0, 9, 4 + len(family)}, // past the line's end: its end, before the line break
		{1, 0, 5 + len(family)}, {1, 2, 11 + len(family)}, {1, 4, 13 + len(family)},
		{2, 0, len(text)}, {2, 5, len(text)}, // the empty last line
		{7, 0, len(text)}, // a line the text does not have
	} {
		if got := byteOffset(text, c.line, c.col); got != c.want {
			t.Errorf("byteOffset(%d, %d) = %d, want %d", c.line, c.col, got, c.want)
		}
	}
	if got := byteOffset("", 3, 3); got != 0 {
		t.Errorf("in an empty text: %d", got)
	}
}
