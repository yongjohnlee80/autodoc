package tui

import (
	"fmt"
	"slices"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/style"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// FIND — / in the page, the explorer, the relations or any pane a feature registers (Findable), as
// AutoDB's: a word or phrase in the pane that has the keyboard. The cursor moves to the next row holding it; n and N go to the next and the
// previous. It is a literal, case-blind match, read afresh from the pane at every jump. The
// workspace's search (by words and meaning, across every file) is Ctrl+G, SPC / or SPC SPC.
//
// WHILE A FIND IS ON in the page, the page marks its words wherever they are (the Alert style, as
// the search's preview marks its words), and "finding …" shows under the menu at the top right,
// with an X that clears it, except while a drawer at the right edge is open over that corner. Search › Clear find clears it too. Search › Find in page, next and previous reach the
// page's find from the Text mode, where / types a slash.
//
// THE EXPLORER is searched over the rows it has loaded, in the order the tree shows them, a closed
// folder's loaded rows included: a hit there is revealed, its folders opened.

// FindPane is a pane / searches: what find calls it, the view whose keyboard makes it the pane
// searched, and its rows as they are now. Name and View are declarations, the same whenever read: a
// panel's View is its own PanelSpec's, never one looked up.
type FindPane struct {
	Name string // "explorer": "find in the explorer", "n and N find again in the explorer"
	View string // / searches the pane whose view has the keyboard
	// Scope is the pane's keyScope (keys.go), where it answers / n N itself; "" for none, and the
	// Window's / n N, which say where to put the keyboard, answer there
	Scope string
	// Rows are the pane's rows as it shows them, the row under its cursor (-1 for none), and how to put
	// the cursor on row i, the match at col (grapheme columns). The move is bound to this same snapshot,
	// so a pane whose rows are not plain indexes (the explorer's tree) moves to the row it listed.
	Rows func() (rows []string, cursor int, moveTo func(row, col int) error)
}

// Findable is a feature whose pane / searches. The registry takes the panes in the features' order,
// the page first (the host's own).
type Findable interface{ FindPane() FindPane }

// findPage is the page's pane: the host's own, which Search › Find in page, the page's marks and Esc
// name.
const findPage = "page"

type findState struct {
	query, target string
	pending       string // the pane / was pressed in, while the question is open
	// at and total are where the last jump went, x of y rows holding the query: "finding" shows
	// them; 0 before the first jump
	at, total int
}

// findPanes are the panes find searches, in the registry's order: the page first.
func (h *Host) findPanes() []FindPane {
	if h.reg == nil {
		return nil // a host built without its options (tests)
	}
	return h.reg.finds
}

// findPane is the pane named name.
func (h *Host) findPane(name string) (FindPane, bool) {
	i := slices.IndexFunc(h.findPanes(), func(p FindPane) bool { return p.Name == name })
	if i < 0 {
		return FindPane{}, false
	}
	return h.findPanes()[i], true
}

// findTarget is the pane with the keyboard, by its name; "" when no pane find searches has it.
func (h *Host) findTarget() string {
	app := h.p.App()
	for _, p := range h.findPanes() {
		if comp, ok := h.p.Find(p.View); ok && app.FocusWithin(comp) {
			return p.Name
		}
	}
	return ""
}

// openFind asks what to find in the pane with the keyboard.
func (h *Host) openFind() {
	target := h.findTarget()
	if target == "" {
		h.say("find: put the keyboard in " + findPaneList(h.findPanes()) + " first")
		return
	}
	h.askFind(target)
}

// findPaneList names the panes as the hint does: "the page, the explorer or the relations".
func findPaneList(panes []FindPane) string {
	var names []string
	for _, p := range panes {
		names = append(names, "the "+p.Name)
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// openFindInPage is Search › Find in page: the page's find, wherever the keyboard is.
func (h *Host) openFindInPage() { h.askFind(findPage) }

func (h *Host) askFind(target string) {
	h.find.pending = target
	h.set("App.findTitle", "find in the "+target+" — n next, N previous")
	h.set("App.findError", "")
	h.open("findDialog")
}

// startFind is the question's answer: the first row holding it, from the cursor on.
func (h *Host) startFind(pattern string) {
	if strings.TrimSpace(pattern) == "" {
		h.set("App.findError", "type a word to find")
		h.p.Post(func() { h.open("findDialog") })
		return
	}
	if h.find.pending == "" {
		return
	}
	h.find.target, h.find.pending = h.find.pending, ""
	h.find.query = pattern
	h.find.at, h.find.total = 0, 0
	h.core.SetFind("", widget.FindOptions{}) // n and N are this find's, not a replace's leftover
	h.set("App.lastFind", pattern)
	h.showFind()
	h.p.Post(func() { h.findJump(+1, true) }) // once the question has closed
}

// findAgain is Search › Find next and previous. The menu bar hands the keyboard back to the page
// before it runs an item, so n's check that the keyboard is in the find's pane holds for the page.
func (h *Host) findAgain(dir int) {
	if h.find.query == "" || h.find.target == "" {
		h.say("nothing to find again: Search › Find in page asks what to find")
		return
	}
	h.findJump(dir, false)
}

// clearFind ends the find: its words unmarked, "finding …" gone; n and N have nothing to find.
func (h *Host) clearFind() {
	if h.find.query == "" {
		return
	}
	h.core.SetCurrentMatch(widget.TextRange{})
	h.find.query, h.find.target = "", ""
	h.showFind()
	h.say("find cleared")
}

// showFind brings the page's marks and "finding …" up to date with the find: marked and shown while
// one is on, in the page; gone when it ends.
func (h *Host) showFind() {
	var terms []string
	if h.find.query != "" && h.find.target == findPage {
		terms = []string{strings.ToLower(h.find.query)}
	}
	if !slices.Equal(terms, h.findMarks.load()) {
		h.findMarks.Store(terms)
		h.core.InvalidateHighlightPaint()
	}
	if h.findChip == nil {
		return
	}
	// the page's find only (a find in a panel moves its cursor and marks nothing), and not over a
	// drawer open at the right edge, whose title row the chip would cover
	if len(terms) == 0 || h.rightDrawerOpen() {
		h.findChip.Hide()
		return
	}
	label := fmt.Sprintf("finding %q · n next · N previous · Esc clears ", h.find.query)
	if h.find.total > 0 {
		label = fmt.Sprintf("finding %q %d/%d · n next · N previous · Esc clears ", h.find.query, h.find.at, h.find.total)
	}
	h.findLabel.SetText(label)
	h.findChip.Show()
}

// rightDrawerOpen is whether a panel open now is docked at the right edge.
func (h *Host) rightDrawerOpen() bool {
	for id, at := range h.panelEdges() {
		if at == "right" && h.panelOpen[id] {
			return true
		}
	}
	return false
}

// attachFindChip puts the find's card over the page, hidden until a find starts: at the top right, a
// row down so the menu bar stays clear, framed and titled as the replace card is, its X clearing the
// find.
func (h *Host) attachFindChip() {
	host, ok := h.p.Overlay()
	if !ok || h.findChip != nil {
		return
	}
	// the replace card's look: the panel's colours, a frame and a title
	look := style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)
	h.findLabel = widget.NewText("", widget.WithTextStyle(look))
	clear := widget.NewButton("X", widget.WithOnActivate(h.clearFind), widget.WithButtonDecoration(" ", " "))
	row := tuicore.NewFlex(tuicore.Horizontal)
	row.Add(h.findLabel, clear)
	card := widget.NewBox(row, widget.WithTitle("Find"), widget.WithBorder(style.BorderNormal), widget.WithStyle(look.Padding(0, 1)))
	col := tuicore.NewFlex(tuicore.Vertical)
	col.Add(widget.NewText(""), card) // the empty row keeps the menu bar clear above the card
	h.findChip = widget.NewFloat(col, widget.WithAnchor(widget.TopRight))
	host.Attach(h.findChip)
	h.showFind()
}

func (h *Host) findCancelled() { h.find.pending = "" }

func (h *Host) findNext()     { h.findJump(+1, false) }
func (h *Host) findPrevious() { h.findJump(-1, false) }

// findJump moves the cursor of the pane found in to the next (dir +1) or the previous (-1) row
// holding the query, wrapping; includeCurrent lets the row under the cursor count, for the first.
func (h *Host) findJump(dir int, includeCurrent bool) {
	f := &h.find
	if f.query == "" || f.target == "" {
		h.say("nothing to find again: / asks what to find")
		return
	}
	if !includeCurrent && h.findTarget() != f.target {
		h.say("n and N find again in the " + f.target + ": put the keyboard there, or / for a new find")
		return
	}
	pane, ok := h.findPane(f.target)
	if !ok {
		h.say("nothing to find again: / asks what to find")
		return
	}
	rows, cur, moveTo := pane.Rows()
	var hits []int
	cols := map[int]int{}
	for i, row := range rows {
		if col, found := clusterMatch(row, f.query); found {
			hits = append(hits, i)
			cols[i] = col
		}
	}
	if len(hits) == 0 {
		f.at, f.total = 0, 0
		h.showFind()
		h.say(fmt.Sprintf("find: no %q in the %s", f.query, f.target))
		return
	}
	selected := 0
	if dir > 0 {
		for i, row := range hits {
			if row > cur || (includeCurrent && row >= cur) {
				selected = i
				break
			}
		}
	} else {
		selected = len(hits) - 1
		for i := len(hits) - 1; i >= 0; i-- {
			if hits[i] < cur {
				selected = i
				break
			}
		}
	}
	row := hits[selected]
	h.keep(moveTo(row, cols[row]))
	f.at, f.total = selected+1, len(hits)
	h.showFind()
	h.say(fmt.Sprintf("find %q: %d of %d in the %s", f.query, selected+1, len(hits), f.target))
}

// pageFind is the page's pane as a feature, the host's own: first among the features, so first in
// find's order.
type pageFind struct{ h *Host }

func (pageFind) Name() string                 { return "page find" }
func (pageFind) State() map[string]any        { return nil }
func (pageFind) Commands() map[string]Command { return nil }
func (p pageFind) FindPane() FindPane         { return p.h.pageFindPane() }

// pageFindPane is the page's pane, the host's own: a hit puts the cursor on it, highlights it and
// gives the page the keyboard.
func (h *Host) pageFindPane() FindPane {
	return FindPane{Name: findPage, View: "editor", Scope: "page", Rows: func() ([]string, int, func(int, int) error) {
		cur, _ := h.core.Line()
		return h.core.Lines(), cur, func(row, col int) error {
			h.core.SetLine(row, col)
			// the match the find is on, highlighted whatever the mode, so it is plain which it is
			n := len(slices.Collect(tuicore.Graphemes(h.find.query)))
			h.core.SetCurrentMatch(widget.TextRange{From: widget.TextPos{Line: row, Col: col}, To: widget.TextPos{Line: row, Col: col + n}})
			return h.p.Call("editor", "forceActiveFocus")
		}
	}}
}

// clusterMatch is where pattern first appears in line, case-blind, in grapheme columns (what
// Editor.SetLine takes), never byte offsets.
func clusterMatch(line, pattern string) (int, bool) {
	var hay, needle []string
	for c := range tuicore.Graphemes(line) {
		hay = append(hay, c)
	}
	for c := range tuicore.Graphemes(pattern) {
		needle = append(needle, c)
	}
	if len(needle) == 0 || len(needle) > len(hay) {
		return 0, false
	}
	for start := 0; start+len(needle) <= len(hay); start++ {
		if strings.EqualFold(strings.Join(hay[start:start+len(needle)], ""), pattern) {
			return start, true
		}
	}
	return 0, false
}

// escapeFind is the editor's Esc when it has nothing of its own to cancel (Normal mode, or Text
// mode; never Insert or Visual, whose Esc goes back to Normal): the page's find ends, as its X
// does. It reports whether there was one to end; with none the key goes on.
func (h *Host) escapeFind() bool {
	if h.find.query == "" || h.find.target != findPage {
		return false
	}
	h.clearFind()
	return true
}
