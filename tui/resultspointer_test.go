package tui

import (
	"encoding/csv"
	"fmt"
	"strings"
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// THE POINTER IN THE RESULTS — a drag over cells, over the row numbers and over the titles, a
// title clicked, and a right press, through the panel's DataTable, each followed by Copy: the
// copy is the cells selected, never a row's number or a field's title. The terminal's cells here;
// the GUI's pixels in gui_resultspanel_test.go.

// pointer drives the pointer over the results' table for a cell, a row's number or a field's
// title (hit): a left press, a drag to another of the same, the release, and a right press. Each
// returns once the table has had the event.
type pointer interface {
	press(row, col int, hit widget.TableHit)
	dragTo(row, col int, hit widget.TableHit)
	release()
	rightPress(row, col int, hit widget.TableHit)
}

// screenPointer is the terminal's pointer: mouse events at the screen's cells, through the App's
// own routing (the table takes the pointer while a drag lasts). A field's title is the header's
// line, a row's number the table's first cells, then each field in its width and the gap after it.
type screenPointer struct {
	t      *testing.T
	r      *running
	x0, y0 int // the table's top left, on the screen: where its header line and row numbers are
	last   [2]int
}

// resultsHeaderRows is the rows above the table's first: the titles and the rule under them.
const resultsHeaderRows = 2

// resultsGutter is the cells before the first field: the row numbers, then the gap, the gutter's
// rule and the gap again.
func resultsGutter(tc *widget.TableCore) int { return tc.RowNumberDigits() + 3 }

func newScreenPointer(t *testing.T, r *running) *screenPointer {
	t.Helper()
	// the panel tall enough for the header and every row of the kinds table
	onLoop(r, func() bool { r.h.plugins.c.Set("App.resultsSize", 70); return true })
	p := &screenPointer{t: t, r: r}
	gutter := onLoop(r, func() int { return resultsGutter(r.resultsCore()) })
	last := fmt.Sprint(len(kindsRows))
	// the table where the screen shows it: its titles line, the first two titles at the gutter's
	// width, and under the rule the last row's number in sight
	r.s.WaitFor(t, "the results' table on the screen, its rows in sight", func(sc string) bool {
		lines := strings.Split(sc, "\n")
		for y, line := range lines {
			runes := []rune(line)
			x := strings.Index(line, "id  name  ")
			if x < 0 || y+resultsHeaderRows-1+len(kindsRows) >= len(lines) {
				continue
			}
			x = len([]rune(line[:x])) - gutter
			below := []rune(lines[y+resultsHeaderRows-1+len(kindsRows)])
			if x < 0 || x >= len(runes) || x+len(last) > len(below) || string(below[x:x+len(last)]) != last {
				continue
			}
			p.x0, p.y0 = x, y
			return true
		}
		return false
	})
	return p
}

func (p *screenPointer) local(row, col int, hit widget.TableHit) (int, int) {
	xy := onLoop(p.r, func() [2]int {
		tc := p.r.resultsCore()
		x := resultsGutter(tc)
		for c := range col {
			x += tc.Width(c) + 2
		}
		switch hit {
		case widget.TableHitHeader:
			return [2]int{x, 0}
		case widget.TableHitGutter:
			return [2]int{0, row + resultsHeaderRows}
		}
		return [2]int{x, row + resultsHeaderRows}
	})
	return xy[0], xy[1]
}

func (p *screenPointer) send(kind tuicore.MouseKind, button tuicore.MouseButton, x, y int) {
	p.t.Helper()
	p.r.keys(p.t, tuicore.MouseEvent{Kind: kind, Button: button, X: p.x0 + x, Y: p.y0 + y})
	p.last = [2]int{x, y}
}

func (p *screenPointer) press(row, col int, hit widget.TableHit) {
	p.t.Helper()
	x, y := p.local(row, col, hit)
	p.send(tuicore.MousePress, tuicore.MouseLeft, x, y)
	waitHandled(p.t, p.r, "the press", func(tc *widget.TableCore) bool { return tc.Dragging() && pointerAt(tc, row, col, hit) })
}

func (p *screenPointer) dragTo(row, col int, hit widget.TableHit) {
	p.t.Helper()
	x, y := p.local(row, col, hit)
	p.send(tuicore.MouseMotion, tuicore.MouseLeft, x, y)
	waitHandled(p.t, p.r, "the drag", func(tc *widget.TableCore) bool { return pointerAt(tc, row, col, hit) })
}

func (p *screenPointer) release() {
	p.t.Helper()
	p.send(tuicore.MouseRelease, tuicore.MouseLeft, p.last[0], p.last[1])
	waitHandled(p.t, p.r, "the release", func(tc *widget.TableCore) bool { return !tc.Dragging() })
}

func (p *screenPointer) rightPress(row, col int, hit widget.TableHit) {
	p.t.Helper()
	x, y := p.local(row, col, hit)
	p.send(tuicore.MousePress, tuicore.MouseRight, x, y)
	waitHandled(p.t, p.r, "the right press", func(*widget.TableCore) bool {
		return p.r.h.plugins.c.ResultsTable(resultsView).Menu().IsOpen()
	})
}

// waitHandled waits until the table shows an event's effect: an injected event and a posted
// function are drained in either order, so what the event does is what says it arrived.
func waitHandled(t *testing.T, r *running, what string, done func(*widget.TableCore) bool) {
	t.Helper()
	r.s.WaitFor(t, what+" handled", func(string) bool { return onLoop(r, func() bool { return done(r.resultsCore()) }) })
}

// pointerAt reports whether the cursor is where a press or a drag over (row, col, hit) puts it: the
// cell; the row, for a row's number; the field, for a title.
func pointerAt(tc *widget.TableCore, row, col int, hit widget.TableHit) bool {
	cur := tc.Cursor()
	switch hit {
	case widget.TableHitGutter:
		return cur.Row == row
	case widget.TableHitHeader:
		return cur.Col == col
	}
	return cur == widget.TablePos{Row: row, Col: col}
}

// kindsTSV is the kinds table's rows from..to, fields lo..hi (each inclusive), as Copy writes
// them: tabs between, a line per row, no names.
func kindsTSV(from, to, lo, hi int) string {
	var lines []string
	for r := from; r <= to; r++ {
		lines = append(lines, strings.Join(kindsRows[r][lo:hi+1], "\t"))
	}
	return strings.Join(lines, "\n")
}

// wantPointerSelections drives the pointer p over the kinds table, and checks each selection's
// Copy (TSV) exactly; then the CSV and Markdown copies' names line, and a right press keeping the
// selection while the menu opens.
func wantPointerSelections(t *testing.T, r *running, p pointer) {
	t.Helper()
	drag := func(from, to [2]int, hit widget.TableHit) {
		t.Helper()
		p.press(from[0], from[1], hit)
		if from != to {
			p.dragTo(to[0], to[1], hit)
		}
		p.release()
	}
	copied := func(format copyFormat) string {
		t.Helper()
		onLoop(r, func() bool { r.h.plugins.copyResults(format); return true })
		return string(r.s.Backend.Clipboard())
	}
	selection := func() string {
		return onLoop(r, func() string {
			tc := r.resultsCore()
			from, to, ok := tc.Selection()
			return fmt.Sprintf("%v %v %v %v", ok, tc.SelectionKind(), from, to)
		})
	}
	for _, c := range []struct {
		what     string
		from, to [2]int
		hit      widget.TableHit
		kind     widget.TableSelectKind
		tsv      string
	}{
		{"a drag over cells", [2]int{0, 1}, [2]int{2, 2}, widget.TableHitCell, widget.TableSelectCells,
			kindsTSV(0, 0, 1, 5) + "\n" + kindsTSV(1, 1, 0, 5) + "\n" + kindsTSV(2, 2, 0, 2)},
		{"a drag over the row numbers", [2]int{1, 0}, [2]int{3, 0}, widget.TableHitGutter, widget.TableSelectRows, kindsTSV(1, 3, 0, 5)},
		{"a title clicked", [2]int{0, 2}, [2]int{0, 2}, widget.TableHitHeader, widget.TableSelectBlock, kindsTSV(0, 4, 2, 2)},
		{"a drag over the titles", [2]int{0, 1}, [2]int{0, 3}, widget.TableHitHeader, widget.TableSelectBlock, kindsTSV(0, 4, 1, 3)},
	} {
		drag(c.from, c.to, c.hit)
		if got := onLoop(r, func() widget.TableSelectKind { return r.resultsCore().SelectionKind() }); got != c.kind || !strings.HasPrefix(selection(), "true") {
			t.Fatalf("%s: selection %s, want kind %v", c.what, selection(), c.kind)
		}
		if got := copied(formatTSV); got != c.tsv {
			t.Errorf("%s: Copy wrote\n%q\nwant the cells alone\n%q", c.what, got, c.tsv)
		}
	}

	// the titles drag's fields, name, amount and flag: CSV and Markdown open with their names, and
	// with those alone; TSV has none
	records, err := csv.NewReader(strings.NewReader(copied(formatCSV))).ReadAll()
	if err != nil || len(records) != len(kindsRows)+1 || strings.Join(records[0], ",") != "name,amount,flag" || strings.Join(records[1], ",") != "carol,12345678901234567890.124,true" {
		t.Errorf("Copy as CSV: %q, %v; want the names line name,amount,flag, then the rows", records, err)
	}
	md := strings.Split(copied(formatMarkdown), "\n")
	var head []string
	for _, cell := range strings.Split(strings.Trim(md[0], "|"), "|") {
		head = append(head, strings.TrimSpace(cell))
	}
	if strings.Join(head, ",") != "name,amount,flag" || !strings.HasPrefix(md[1], "| --") {
		t.Errorf("Copy as Markdown's first lines: %q", md[:min(2, len(md))])
	}

	// a right press on a cell outside it: the menu opens over the selection, which stays, and Copy
	// still writes it
	before := selection()
	p.rightPress(3, 4, widget.TableHitCell)
	if got := selection(); got != before {
		t.Errorf("the right press changed the selection: %s, was %s", got, before)
	}
	if got := copied(formatTSV); got != kindsTSV(0, 4, 1, 3) {
		t.Errorf("Copy after the right press: %q", got)
	}
	onLoop(r, func() bool { r.h.plugins.c.ResultsTable(resultsView).Menu().Close(); return true })
}

// TestThePointerSelectsWhatIsCopied: in the terminal, a drag over cells selects them in reading
// order, a drag over the row numbers whole rows, a title clicked its field and a drag over the
// titles several; Copy writes the cells alone, no number and no title. CSV and Markdown open with
// the selected fields' names. A right press keeps the selection and opens the menu.
func TestThePointerSelectsWhatIsCopied(t *testing.T) {
	r, _ := kindsTUI(t, "tablestall")
	wantPointerSelections(t, r, newScreenPointer(t, r))
}
