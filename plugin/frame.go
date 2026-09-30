package plugin

import "unicode/utf8"

// Frame is a plugin's dialog as a grid of cells, one character each, drawn and then sent whole
// (Peer.Frame). A character is one cell wide: box drawing, blocks and Latin text are; a wide
// character (CJK, most emoji) takes two cells on the screen and pushes the rest of its row right.
type Frame struct {
	w, h  int
	cells []cell
}

type cell struct {
	r  rune
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
		f.cells[i] = cell{' ', st}
	}
}

// Set puts r at (x, y) in st. Outside the frame it does nothing.
func (f *Frame) Set(x, y int, r rune, st Style) {
	if x < 0 || y < 0 || x >= f.w || y >= f.h {
		return
	}
	f.cells[y*f.w+x] = cell{r, st}
}

// Text writes s from (x, y) rightwards in st, one cell per character, clipped at the frame's edge.
// It returns the x after the last cell written.
func (f *Frame) Text(x, y int, s string, st Style) int {
	for _, r := range s {
		f.Set(x, y, r, st)
		x++
	}
	return x
}

// Fill sets every cell of the rectangle at (x, y), w wide and h high, to r in st.
func (f *Frame) Fill(x, y, w, h int, r rune, st Style) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			f.Set(i, j, r, st)
		}
	}
}

// Rows are the frame as host.frame sends it: each row's cells, run together where the style is the
// same.
func (f *Frame) Rows() []Row {
	rows := make([]Row, f.h)
	buf := make([]byte, 0, f.w)
	for y := range f.h {
		var row Row
		line := f.cells[y*f.w : (y+1)*f.w]
		for i := 0; i < len(line); {
			st := line[i].st
			buf = buf[:0]
			for ; i < len(line) && line[i].st == st; i++ {
				buf = utf8.AppendRune(buf, line[i].r)
			}
			row = append(row, Run{Text: string(buf), Style: st})
		}
		rows[y] = row
	}
	return rows
}
