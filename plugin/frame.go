package plugin

import "github.com/yongjohnlee80/golib/tui"

// Frame is a plugin's dialog as a grid of cells, drawn and then sent whole (Peer.Frame). A cell
// holds one character as the screen shows it: a grapheme cluster ("é" written as e and a combining
// accent, a ZWJ emoji, a flag), measured as AutoDoc's screen measures it (golib's
// tui.StringWidth). A wide character (CJK, most emoji) takes two cells; the second is its
// continuation, which Rows leaves out, since the host's own measure gives the column back.
type Frame struct {
	w, h  int
	cells []cell
}

type cell struct {
	s  string // one grapheme cluster; "" is the continuation of the wide cell to its left
	st Style
}

// NewFrame is a blank frame w cells wide and h high.
func NewFrame(w, h int) *Frame {
	w, h = max(w, 0), max(h, 0)
	f := &Frame{w: w, h: h, cells: make([]cell, w*h)}
	f.Clear(Style{})
	return f
}

// Size is the frame's width and height.
func (f *Frame) Size() (w, h int) { return f.w, f.h }

// Clear fills the frame with spaces in st.
func (f *Frame) Clear(st Style) {
	for i := range f.cells {
		f.cells[i] = cell{" ", st}
	}
}

// Set puts r at (x, y) in st, in that one cell: a wide r, which needs two, is a space. Outside
// the frame it does nothing.
func (f *Frame) Set(x, y int, r rune, st Style) {
	s := string(r)
	if tui.StringWidth(s) > 1 {
		s = " "
	}
	f.put(x, y, s, 1, st)
}

// Text writes s from (x, y) rightwards in st, a grapheme cluster at a time, each taking its width:
// two cells for a wide one. A zero-width cluster (a combining mark on its own) joins the cell
// before it. A wide cluster that would cross the right edge is a space. Writing over half of a wide
// cell blanks its other half. It returns the x after the last cluster, past the edge if s runs
// over it.
func (f *Frame) Text(x, y int, s string, st Style) int {
	for g := range tui.Graphemes(s) {
		w := tui.StringWidth(g)
		switch {
		case w == 0 && f.join(x-1, y, g):
			continue
		case w == 0:
			w = 1 // nothing before it to join: a cell of its own, as the host gives it
		case w == 2 && x+1 == f.w:
			f.put(x, y, " ", 1, st)
			x += 2
			continue
		}
		f.put(x, y, g, w, st)
		x += w
	}
	return x
}

// Fill sets every cell of the rectangle at (x, y), w wide and h high, to r in st, as Set does.
func (f *Frame) Fill(x, y, w, h int, r rune, st Style) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			f.Set(i, j, r, st)
		}
	}
}

// put writes cluster g, w cells wide (1 or 2), at (x, y). The cells it covers that are outside the
// frame are dropped; a wide cluster half outside is a space in the half inside.
func (f *Frame) put(x, y int, g string, w int, st Style) {
	if y < 0 || y >= f.h || x+w <= 0 || x >= f.w {
		return
	}
	if w == 2 && x < 0 {
		x, g, w = 0, " ", 1
	}
	f.unpair(x, y)
	if w == 2 {
		f.unpair(x+1, y)
		f.cells[y*f.w+x+1] = cell{"", st}
	}
	f.cells[y*f.w+x] = cell{g, st}
}

// unpair frees (x, y) from a wide cell it is half of: the other half becomes a space in that
// cell's style, so no head is left without its continuation, or a continuation without its head.
func (f *Frame) unpair(x, y int) {
	i := y*f.w + x
	switch {
	case f.cells[i].s == "" && x > 0: // a continuation: its head, to the left, goes
		f.cells[i-1].s = " "
	case x+1 < f.w && f.cells[i+1].s == "": // a wide head: its continuation, to the right, goes
		f.cells[i+1].s = " "
	}
}

// join adds a zero-width cluster to the cell at (x, y), or to its head when it is a continuation;
// false when there is no cell there to take it.
func (f *Frame) join(x, y int, g string) bool {
	if y < 0 || y >= f.h || x < 0 || x >= f.w {
		return false
	}
	i := y*f.w + x
	if f.cells[i].s == "" && x > 0 {
		i--
	}
	f.cells[i].s += g
	return true
}

// Rows are the frame as host.frame sends it: each row's cells, run together where the style is the
// same, continuations left out. The host segments each run's text into grapheme clusters again, so
// two cells whose text would join into one cluster (two regional indicators written apart, a
// character and a variation selector written after it) are sent in runs of their own: the host
// never joins clusters across runs, and each keeps the column it was given.
func (f *Frame) Rows() []Row {
	rows := make([]Row, f.h)
	buf := make([]byte, 0, f.w)
	for y := range f.h {
		var row Row
		line := f.cells[y*f.w : (y+1)*f.w]
		for i := 0; i < len(line); {
			st := line[i].st
			buf = buf[:0]
			last := "" // the run's last cell, to check the next one does not join it
			for ; i < len(line) && line[i].st == st; i++ {
				s := line[i].s
				if s == "" {
					continue // a continuation: its head is already in the run
				}
				if last != "" && joins(last, s) {
					break
				}
				buf = append(buf, s...)
				last = s
			}
			row = append(row, Run{Text: string(buf), Style: st})
		}
		rows[y] = row
	}
	return rows
}

// joins reports whether cells a and b, side by side, would be segmented as fewer clusters than they
// hold apart. Printable ASCII next to printable ASCII never joins, and is answered at once.
func joins(a, b string) bool {
	if len(a) == 1 && len(b) == 1 && a[0] >= ' ' && a[0] < 0x7f && b[0] >= ' ' && b[0] < 0x7f {
		return false
	}
	return count(a+b) < count(a)+count(b)
}

// count is how many grapheme clusters s holds.
func count(s string) int {
	n := 0
	for range tui.Graphemes(s) {
		n++
	}
	return n
}
