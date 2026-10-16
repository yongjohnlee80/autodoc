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

// pointAt is where a pointer event over the results' table goes, in its own frame, for the field
// col's title (hit TableHitHeader), row's number (TableHitGutter), or the cell (TableHitCell).
type pointAt func(row, col int, hit widget.TableHit) (x, y int, subX, subY float32)

// tuiPoint is pointAt for the terminal's DataTable: the header is its first line, the row numbers
// its first cells, then each field in its width and the gap after it.
func tuiPoint(r *running) pointAt {
	return func(row, col int, hit widget.TableHit) (int, int, float32, float32) {
		xy := onLoop(r, func() [2]int {
			tc := r.resultsCore()
			x := tc.RowNumberDigits() + 2
			for c := range col {
				x += tc.Width(c) + 2
			}
			switch hit {
			case widget.TableHitHeader:
				return [2]int{x, 0}
			case widget.TableHitGutter:
				return [2]int{0, row + 1}
			}
			return [2]int{x, row + 1}
		})
		return xy[0], xy[1], 0, 0
	}
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

// wantPointerSelections drives the pointer over the kinds table at, and checks each selection's
// Copy (TSV) exactly; then the CSV and Markdown copies' names line, and a right press keeping the
// selection while the menu opens.
func wantPointerSelections(t *testing.T, r *running, at pointAt) {
	t.Helper()
	send := func(kind tuicore.MouseKind, button tuicore.MouseButton, row, col int, hit widget.TableHit) {
		t.Helper()
		x, y, sx, sy := at(row, col, hit)
		onLoop(r, func() bool {
			return r.h.plugins.c.ResultsTable(resultsView).HandleEvent(tuicore.MouseEvent{Kind: kind, Button: button, X: x, Y: y, SubX: sx, SubY: sy})
		})
	}
	drag := func(from, to [2]int, hit widget.TableHit) {
		t.Helper()
		send(tuicore.MousePress, tuicore.MouseLeft, from[0], from[1], hit)
		if from != to {
			send(tuicore.MouseMotion, tuicore.MouseLeft, to[0], to[1], hit)
		}
		send(tuicore.MouseRelease, tuicore.MouseLeft, to[0], to[1], hit)
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
	send(tuicore.MousePress, tuicore.MouseRight, 3, 4, widget.TableHitCell)
	r.s.WaitFor(t, "the menu open", func(string) bool {
		return onLoop(r, func() bool { return r.h.plugins.c.ResultsTable(resultsView).Menu().IsOpen() })
	})
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
	wantPointerSelections(t, r, tuiPoint(r))
}
