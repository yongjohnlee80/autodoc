package card

import (
	"strings"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// span is text in one style.
type span struct {
	text string
	st   plugin.Style
}

// line is one row of the card as laid out: its spans from the left margin, and what a press on it
// reaches.
type line struct {
	spans []span
	ctl   Control // the control the row is part of; nil for text
	item  int     // a List's or a Tree's row index; -1 for any other row
	btns  []btnAt // a Buttons row's buttons, by their cells from the margin
}

// btnAt is a button's cells in its row, [x0, x1) from the margin.
type btnAt struct{ x0, x1, i int }

func textLine(st plugin.Style, s string) line { return line{spans: []span{{s, st}}, item: -1} }

// segment is n rows of the body, row(i) the i-th, made only for the rows in view: a List or a
// Tree of thousands is never laid out whole.
type segment struct {
	n   int
	row func(i int) line
}

// layout is the page laid out width cells wide: the header (always shown), the body (scrolling,
// keeping its row keep in view), and at the foot the rule, the buttons, the keys, the status and
// the problem.
type layout struct {
	width   int
	header  []line
	body    []segment
	keep    int // the body row to keep in view; -1 none
	buttons []line
	keys    []line
	status  []line
	problem []line
}

func (l *layout) bodyLen() int {
	n := 0
	for _, s := range l.body {
		n += s.n
	}
	return n
}

// bodyRow is the body's i-th row.
func (l *layout) bodyRow(i int) line {
	for _, s := range l.body {
		if i < s.n {
			return s.row(i)
		}
		i -= s.n
	}
	return line{item: -1}
}

// add appends lines to the body as a segment of their own.
func (l *layout) add(ls ...line) {
	ls = append([]line(nil), ls...)
	l.body = append(l.body, segment{n: len(ls), row: func(i int) line { return ls[i] }})
}

// lay is the page laid out to the card's inner width: every block, and the foot.
func (c *Card) lay(p Page, cols int) layout {
	l := layout{width: cols, keep: -1}
	for _, b := range p.Header {
		c.block(&l, b, cols)
	}
	l.header = make([]line, 0, l.bodyLen())
	for i := range l.bodyLen() {
		l.header = append(l.header, l.bodyRow(i))
	}
	l.body, l.keep = nil, -1 // a header's rows always show: nothing to keep
	for _, b := range p.Body {
		c.block(&l, b, cols)
	}
	if p.Buttons != nil {
		l.buttons = c.buttonRows(p.Buttons, cols)
	}
	if p.Keys != "" {
		for _, s := range wrap(p.Keys, cols) {
			l.keys = append(l.keys, textLine(Dim.style(), s))
		}
	}
	if p.Status != "" {
		for _, s := range wrap(p.Status, cols) {
			l.status = append(l.status, textLine(Plain.style(), s))
		}
	}
	if p.Problem != "" {
		for _, s := range wrap(p.Problem, cols) {
			l.problem = append(l.problem, textLine(Problem.style(), s))
		}
	}
	return l
}

// block lays b out at the end of the body.
func (c *Card) block(l *layout, b Block, cols int) {
	switch b := b.(type) {
	case textBlock:
		if b.one {
			l.add(textLine(b.role.style(), elide(b.s, cols)))
			return
		}
		var ls []line
		for _, s := range wrap(b.s, cols) {
			ls = append(ls, textLine(b.role.style(), s))
		}
		l.add(ls...)
	case blank:
		l.add(line{item: -1})
	case *Input:
		if b.Label != "" {
			l.add(textLine(Plain.style(), elide(b.Label, cols)))
		}
		if c.focus == b {
			l.keep = l.bodyLen()
		}
		l.add(c.fieldRow(b, cols))
	case *Buttons:
		l.add(c.buttonRows(b, cols)...)
	case *List:
		b.clamp()
		if c.focus == b && b.Len > 0 {
			l.keep = l.bodyLen() + b.Sel
		}
		sel, item := c.selection(c.active(b)), b.Item
		l.body = append(l.body, segment{n: b.Len, row: func(i int) line {
			text := ""
			if item != nil {
				text = item(i)
			}
			ln := line{ctl: b, item: i}
			if i == b.Sel {
				ln.spans = []span{{pad(elide("> "+text, cols), cols), sel}}
			} else {
				ln.spans = []span{{elide("  "+text, cols), Plain.style()}}
			}
			return ln
		}})
	case *Tree:
		rows := b.rows()
		at := b.index(rows)
		if c.focus == b && len(rows) > 0 {
			l.keep = l.bodyLen() + at
		}
		sel := c.selection(c.active(b))
		l.body = append(l.body, segment{n: len(rows), row: func(i int) line {
			r := rows[i]
			text := strings.Repeat("  ", r.depth)
			switch {
			case r.empty:
				text += r.n.Empty
			case r.n.Leaf:
				text += "  " + r.n.Text
			case r.n.Open:
				text += "▾ " + r.n.Text
			default:
				text += "▸ " + r.n.Text
			}
			ln := line{ctl: b, item: i}
			switch {
			case i == at:
				ln.spans = []span{{pad(elide("> "+text, cols), cols), sel}}
			case r.empty || r.n.Leaf:
				ln.spans = []span{{elide("  "+text, cols), Dim.style()}}
			default:
				ln.spans = []span{{elide("  "+text, cols), Plain.style()}}
			}
			return ln
		}})
	}
}

// The marks drawn in a field.
const (
	maskRune   = '•'
	cursorMark = "█"
)

// fieldRow is in's field width cells wide, underlined across. The focused one wears the
// selection's colours and shows its cursor; a value wider than the field shows the part around the
// cursor, … marking a cut.
func (c *Card) fieldRow(in *Input, cols int) line {
	ln := line{ctl: in, item: -1}
	if cols <= 0 {
		return ln
	}
	focused := c.focus == in
	st := plugin.Style{Underline: true}
	if focused {
		st = c.selection(c.active(in))
		st.Underline = true
	}
	// the characters, the cursor among them when focused
	var parts []string
	if in.Masked {
		for range in.value {
			parts = append(parts, string(maskRune))
		}
	} else {
		parts = clusters(string(in.value))
	}
	cursor := len(parts) // the cursor's index among parts
	if focused {
		starts := in.starts()
		for i, s := range starts {
			if s == in.at {
				cursor = i
				break
			}
		}
		parts = append(parts[:cursor], append([]string{cursorMark}, parts[cursor:]...)...)
	}
	text := strings.Join(parts, "")
	if width(text) > cols {
		text = window(parts, cursor, cols)
	}
	ln.spans = []span{{pad(text, cols), st}}
	return ln
}

// window is parts cut to w cells around parts[at], widening a part at a time on each side while it
// fits, … marking each cut.
func window(parts []string, at, w int) string {
	at = min(at, len(parts)-1)
	lo, hi, used := at, at+1, width(parts[at])
	fits := func(lo, hi, used int) bool {
		marks := 0
		if lo > 0 {
			marks++
		}
		if hi < len(parts) {
			marks++
		}
		return used+marks <= w
	}
	for grew := true; grew; {
		grew = false
		if lo > 0 && fits(lo-1, hi, used+width(parts[lo-1])) {
			lo--
			used += width(parts[lo])
			grew = true
		}
		if hi < len(parts) && fits(lo, hi+1, used+width(parts[hi])) {
			used += width(parts[hi])
			hi++
			grew = true
		}
	}
	text := strings.Join(parts[lo:hi], "")
	if lo > 0 {
		text = ellipsis + text
	}
	if hi < len(parts) {
		text += ellipsis
	}
	return head(text, w)
}

// buttonRows are b's buttons, "[ label ]" each, two spaces apart, centred; buttons too wide for
// one row go on to the next, as words do. The focused one, while the Buttons have the focus, wears
// the selection's style.
func (c *Card) buttonRows(b *Buttons, cols int) []line {
	b.clamp()
	sel := c.selection(c.active(b))
	var rows []line
	cur := line{ctl: b, item: -1}
	n := 0
	flush := func() {
		if indent := (cols - n) / 2; indent > 0 {
			cur.spans = append([]span{{strings.Repeat(" ", indent), Plain.style()}}, cur.spans...)
			for i := range cur.btns {
				cur.btns[i].x0 += indent
				cur.btns[i].x1 += indent
			}
		}
		rows = append(rows, cur)
		cur, n = line{ctl: b, item: -1}, 0
	}
	for i, label := range b.Labels {
		text := "[ " + label + " ]"
		w := width(text)
		if n > 0 && n+2+w > cols {
			flush()
		}
		if n > 0 {
			cur.spans = append(cur.spans, span{"  ", Plain.style()})
			n += 2
		}
		st := Plain.style()
		if i == b.at && c.focus == b {
			st = sel
		}
		cur.spans = append(cur.spans, span{text, st})
		cur.btns = append(cur.btns, btnAt{x0: n, x1: n + w, i: i})
		n += w
	}
	if len(cur.spans) > 0 {
		flush()
	}
	return rows
}

// fit shares h rows among the layout's parts, in turn: the header; one body row (the one to keep);
// one problem row; the buttons; the keys; the rule; the status; the rest of the problem; the rest
// of the body. A status or a problem cut short keeps its end, its first row starting with "…".
// The body's view starts at from, moved only as far as keeps its row in view; fit returns where it
// started.
func (l *layout) fit(h, from int) (top, foot []line, start int) {
	left := max(h, 0)
	take := func(n int) int {
		k := min(max(n, 0), left)
		left -= k
		return k
	}
	content := l.bodyLen()
	nHeader := take(len(l.header))
	nBody := take(min(content, 1))
	nProblem := take(min(len(l.problem), 1))
	nButtons := take(len(l.buttons))
	nKeys := take(len(l.keys))
	nRule := take(1)
	nStatus := take(len(l.status))
	nProblem += take(len(l.problem) - nProblem)
	nBody += take(content - nBody)

	top = append(top, l.header[:nHeader]...)
	if nBody > 0 {
		start = min(max(from, 0), content-nBody)
		if l.keep >= 0 {
			start = min(start, l.keep)
			start = max(start, l.keep-nBody+1)
		}
		for i := start; i < start+nBody; i++ {
			top = append(top, l.bodyRow(i))
		}
	}
	if nRule > 0 {
		foot = append(foot, textLine(Dim.style(), strings.Repeat("─", max(l.width, 0))))
	}
	foot = append(foot, l.buttons[:nButtons]...)
	foot = append(foot, l.keys[:nKeys]...)
	foot = append(foot, l.tail(l.status, nStatus)...)
	foot = append(foot, l.tail(l.problem, nProblem)...)
	return top, foot, start
}

// tail is the last n of rows; when that is fewer than all, its first row starts with …, cut at its
// start to the width.
func (l *layout) tail(rows []line, n int) []line {
	if n <= 0 {
		return nil
	}
	if n >= len(rows) {
		return rows
	}
	out := append([]line(nil), rows[len(rows)-n:]...)
	first := out[0]
	if len(first.spans) > 0 {
		s := first.spans[0]
		cut := elideStart(ellipsis+strings.TrimLeft(s.text, " "), l.width)
		out[0] = line{spans: append([]span{{cut, s.st}}, first.spans[1:]...), item: -1}
	}
	return out
}

// hit is what a frame row of the last draw holds: its line, drawn from the margin.
type hit struct{ line }

// render is the page drawn into a frame of the card's size, the content one cell in from each
// side; it remembers each row's line for the pointer.
func (c *Card) render(p Page) *plugin.Frame {
	f := plugin.NewFrame(c.w, c.h)
	l := c.lay(p, c.w-2)
	top, foot, start := l.fit(c.h, c.from)
	c.from = start
	c.hits = make([]hit, c.h)
	for i := range c.hits {
		c.hits[i] = hit{line{item: -1}}
	}
	draw := func(y int, ln line) {
		x := 1
		for _, s := range ln.spans {
			x = f.Text(x, y, s.text, s.st)
		}
		c.hits[y] = hit{ln}
	}
	for y, ln := range top {
		draw(y, ln)
	}
	for i, ln := range foot {
		draw(c.h-len(foot)+i, ln)
	}
	return f
}
