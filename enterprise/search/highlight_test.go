package search

import "testing"

func TestWordHighlights(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"a \x02word.\x03", "a \x02word\x03."},
		{"\x02“fox!”\x03 and \x02(grapes).\x03", "“\x02fox\x03!” and (\x02grapes\x03)."},
		{"\x02don't\x03 \x02你好。\x03", "\x02don't\x03 \x02你好\x03。"},
		{"plain text.", "plain text."},
		{"\x02!!!\x03", "!!!"},
		{"unfinished \x02word.", "unfinished \x02word."},
	} {
		if got := wordHighlights(c.in); got != c.want {
			t.Errorf("%q: got %q, want %q", c.in, got, c.want)
		}
	}
}
