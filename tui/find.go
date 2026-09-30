package tui

import (
	"fmt"
	"slices"
	"strings"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// FIND — / in the page, the explorer or the links, as AutoDB's: a word or phrase in the pane that
// has the keyboard. The cursor moves to the next row holding it; n and N go to the next and the
// previous. It is a literal, case-blind match, read afresh from the pane at every jump. The
// workspace's search (by words and meaning, across every note) is Ctrl+G, SPC / or SPC SPC.
//
// THE EXPLORER is searched over the rows it has loaded, in the order the tree shows them, a closed
// folder's loaded rows included: a hit there is revealed, its folders opened.

// The panes find searches, by the id of their view.
const (
	findPage     = "page"
	findExplorer = "explorer"
	findLinks    = "links"
)

type findState struct {
	query, target string
	pending       string // the pane / was pressed in, while the question is open
}

// findTarget is the pane with the keyboard: the page, or an open panel.
func (h *Host) findTarget() string {
	app := h.p.App()
	for _, c := range []struct{ id, target string }{
		{"editor", findPage}, {panels["explorer"], findExplorer}, {panels["links"], findLinks},
	} {
		if comp, ok := h.p.Find(c.id); ok && app.FocusWithin(comp) {
			return c.target
		}
	}
	return ""
}

// openFind asks what to find in the pane with the keyboard.
func (h *Host) openFind() {
	target := h.findTarget()
	if target == "" {
		h.setStatus("find: put the keyboard in the page, the explorer or the links first")
		return
	}
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
	h.set("App.lastFind", pattern)
	h.p.Post(func() { h.findJump(+1, true) }) // once the question has closed
}

func (h *Host) findCancelled() { h.find.pending = "" }

func (h *Host) findNext()     { h.findJump(+1, false) }
func (h *Host) findPrevious() { h.findJump(-1, false) }

// findJump moves the cursor of the pane found in to the next (dir +1) or the previous (-1) row
// holding the query, wrapping; includeCurrent lets the row under the cursor count, for the first.
func (h *Host) findJump(dir int, includeCurrent bool) {
	f := &h.find
	if f.query == "" || f.target == "" {
		h.setStatus("nothing to find again: / asks what to find")
		return
	}
	if !includeCurrent && h.findTarget() != f.target {
		h.setStatus("n and N find again in the " + f.target + ": put the keyboard there, or / for a new find")
		return
	}
	var rows []string
	var at []tuidecl.Index
	cur := -1
	switch f.target {
	case findPage:
		rows = h.editor.Lines()
		cur, _ = h.editor.Line()
	case findExplorer:
		rows, at = h.explorerLoaded()
		for i, ix := range at {
			if slices.Equal(h.explorerKeys(ix), h.explorerAt) {
				cur = i
				break
			}
		}
	case findLinks:
		for i := range h.backlinks.Len() {
			rows = append(rows, h.backlinks.At(i)["label"].(string))
		}
		cur = h.linksAt
	}
	var hits []int
	cols := map[int]int{}
	for i, row := range rows {
		if col, found := clusterMatch(row, f.query); found {
			hits = append(hits, i)
			cols[i] = col
		}
	}
	if len(hits) == 0 {
		h.setStatus(fmt.Sprintf("find: no %q in the %s", f.query, f.target))
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
	switch f.target {
	case findPage:
		h.editor.SetLine(row, cols[row])
		h.keep(h.p.Call("editor", "forceActiveFocus"))
	case findExplorer:
		h.keep(h.p.Call(panels["explorer"], "setCurrentIndex", at[row]))
		h.explorerAt = h.explorerKeys(at[row])
	case findLinks:
		h.set("App.linksIndex", -1) // moved away first: the same row twice still reaches the view
		h.set("App.linksIndex", row)
		h.linksAt = row
	}
	h.setStatus(fmt.Sprintf("find %q: %d of %d in the %s", f.query, selected+1, len(hits), f.target))
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

// explorerMoved is the explorer's cursor moving: the row under it, kept by its keys.
func (h *Host) explorerMoved(ix tuidecl.Index) error {
	h.explorerAt = h.explorerKeys(ix)
	return nil
}

// linksMoved is the links' cursor moving.
func (h *Host) linksMoved(i int) { h.linksAt = i }

// explorerKeys is a row's identity in the tree: the keys from the top row down to it.
func (h *Host) explorerKeys(ix tuidecl.Index) []string {
	var keys []string
	for p := &ix; p != nil; p = p.Parent {
		keys = append([]string{h.explorer.Key(*p)}, keys...)
	}
	return keys
}

// explorerLoaded are the explorer's loaded rows in the order the tree shows them, each with its
// Index: a row's loaded children follow it, whether or not its folder is open now.
func (h *Host) explorerLoaded() ([]string, []tuidecl.Index) {
	m := h.explorer
	var labels []string
	var at []tuidecl.Index
	var walk func(parent *tuidecl.Index)
	walk = func(parent *tuidecl.Index) {
		for r := 0; r < m.RowCount(parent); r++ {
			ix := tuidecl.Index{Row: r, Parent: parent}
			labels = append(labels, m.Data(ix, "label").Raw)
			at = append(at, ix)
			if m.RowCount(&ix) > 0 && !m.CanFetchMore(ix) {
				walk(&ix)
			}
		}
	}
	walk(nil)
	return labels, at
}
