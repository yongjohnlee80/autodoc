package card

import (
	"strings"

	"github.com/yongjohnlee80/golib/tui"
)

// The text helpers measure as AutoDoc's screen does: grapheme clusters, each tui.StringWidth wide.

const ellipsis = "…"

// width is s's width in cells.
func width(s string) int { return tui.StringWidth(s) }

// clusters is s cut into grapheme clusters.
func clusters(s string) []string {
	var out []string
	for g := range tui.Graphemes(s) {
		out = append(out, g)
	}
	return out
}

// head is the clusters of s that fit in w cells from its start.
func head(s string, w int) string {
	var b strings.Builder
	n := 0
	for g := range tui.Graphemes(s) {
		gw := tui.StringWidth(g)
		if n+gw > w {
			break
		}
		b.WriteString(g)
		n += gw
	}
	return b.String()
}

// tail is the clusters of s that fit in w cells from its end.
func tail(s string, w int) string {
	cs := clusters(s)
	n, i := 0, len(cs)
	for i > 0 && n+width(cs[i-1]) <= w {
		i--
		n += width(cs[i])
	}
	return strings.Join(cs[i:], "")
}

// elide is s cut to w cells, an ellipsis marking the cut.
func elide(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) <= w {
		return s
	}
	return head(s, w-1) + ellipsis
}

// elideStart is s cut at its start to w cells, an ellipsis marking the cut.
func elideStart(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if width(s) <= w {
		return s
	}
	return ellipsis + tail(s, w-1)
}

// pad is s with spaces to w cells.
func pad(s string, w int) string {
	if n := w - width(s); n > 0 {
		return s + strings.Repeat(" ", n)
	}
	return s
}

// wrap breaks text at spaces into rows w cells wide. A word wider than a whole row is the only
// thing cut: after its last / or - that fits (a path's, a URL's, a file name's), else at the row's
// end. Newlines start a new row.
func wrap(text string, w int) []string {
	if w <= 0 {
		return nil
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		out = append(out, wrapLine(para, w)...)
	}
	return out
}

func wrapLine(text string, w int) []string {
	var out []string
	var cur strings.Builder
	n := 0
	flush := func() {
		out = append(out, cur.String())
		cur.Reset()
		n = 0
	}
	for i, word := range strings.Split(text, " ") {
		wn := width(word)
		if i > 0 {
			if n > 0 && n+1+wn > w {
				flush()
			} else {
				cur.WriteByte(' ')
				n++
			}
		}
		for wn > w-n {
			cut := head(word, w-n)
			if at := strings.LastIndexAny(cut, "/-"); at > 0 && at+1 < len(cut) {
				cut = cut[:at+1]
			}
			if cut == "" { // a cluster wider than what is left of the row: the next row
				flush()
				cut = head(word, w)
				if cut == "" { // wider than any row (a wide cluster in a 1-cell row): dropped
					word, wn = "", 0
					break
				}
			}
			cur.WriteString(cut)
			word = word[len(cut):]
			wn = width(word)
			flush()
		}
		cur.WriteString(word)
		n += wn
	}
	flush()
	return out
}
