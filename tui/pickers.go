package tui

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync/atomic"
	"unicode"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/highlight"
	"github.com/yongjohnlee80/golib/parse/markdown"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// THE PICKERS — search, open and new note: the picker's layout (the fields over the list on the
// left, the note under the cursor on the right, the buttons beneath), each over the workspace's
// notes through the daemon.

// hit is one section a search found.
type hit struct {
	path, breadcrumb string
	byteStart        int
	relevance        float64 // 0 to 1: 1 is first in every retriever the search ran
}

// relevanceText is a hit's relevance as the list shows it, a percentage.
func relevanceText(r float64) string { return fmt.Sprintf("%3.0f%%", 100*min(max(r, 0), 1)) }

// searchLive runs the search as it is typed: the latest answers, an older one dropped.
func (h *Host) searchLive(q string) {
	q = strings.TrimSpace(q)
	h.searchSeq++
	seq, ep, ws := h.searchSeq, h.epoch, h.ws
	h.marks.Store(termsOf(q))
	if q == "" {
		h.hitList = nil
		h.hits.Reset(nil)
		h.set("App.hitsTitle", "hits")
		h.showPreview("search", "", "", 0)
		return
	}
	type answer struct {
		hits           []hit
		mode, semantic string
		err            error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "search.query", ws, q, map[string]any{"limit": int64(100)})
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		var out []hit
		for _, x := range asList(m["hits"]) {
			hm := asMap(x)
			start, _ := hm["byte_start"].(int64)
			rel, _ := hm["relevance"].(float64)
			out = append(out, hit{path: str(hm, "path"), breadcrumb: str(hm, "breadcrumb"), byteStart: int(start), relevance: rel})
		}
		return answer{hits: out, mode: str(m, "mode_used"), semantic: str(m, "semantic")}
	}, func(a answer) {
		if seq != h.searchSeq || ep != h.epoch {
			return
		}
		if a.err != nil {
			h.set("App.hitsTitle", "hits · "+wireMessage(a.err))
			return
		}
		h.hitList = a.hits
		rows := make([]rowOf, len(a.hits))
		for i, x := range a.hits {
			rows[i] = rowOf{"key": fmt.Sprintf("%d\t%s", i, x.path), "hit": relevanceText(x.relevance), "path": x.path, "section": x.breadcrumb}
		}
		h.hits.Reset(rows)
		h.set("App.hitsTitle", fmt.Sprintf("hits (%d) · %s · semantic %s", len(a.hits), a.mode, a.semantic))
		if len(a.hits) > 0 {
			h.previewHit(0)
		} else {
			h.showPreview("search", "", "", 0)
		}
	})
}

// termsOf are the query's words as the preview marks them: lower case, the prefix star and the
// quotes gone.
func termsOf(q string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		w = strings.Trim(w, `"*`)
		if w != "" {
			out = append(out, w)
		}
	}
	return out
}

// openSearch opens the search picker, the last query still in it.
func (h *Host) openSearch() { h.open("searchPicker") }

// previewHit shows the note of hit i, at its section.
func (h *Host) previewHit(i int) {
	if i < 0 || i >= len(h.hitList) {
		return
	}
	x := h.hitList[i]
	h.preview("search", x.path, x.byteStart)
}

// openHit opens the note of hit i at its section, and closes the picker.
func (h *Host) openHit(i int) {
	if i < 0 || i >= len(h.hitList) {
		return
	}
	x := h.hitList[i]
	h.closeDialog("searchPicker")
	h.openAt = x.byteStart
	h.openPath(x.path)
}

// preview shows note p of the workspace in the named picker's preview, at byte at: read through
// the daemon, the latest asked for winning.
func (h *Host) preview(picker, p string, at int) {
	h.previewSeq++
	seq, ep, ws := h.previewSeq, h.epoch, h.ws
	do(h, func(ctx context.Context) (answer struct {
		text string
		err  error
	}) {
		res, err := h.call(ctx, "doc.read", ws, p)
		if err != nil {
			answer.err = err
			return
		}
		b, _ := asMap(res)["content"].([]byte)
		answer.text = string(b)
		return
	}, func(a struct {
		text string
		err  error
	}) {
		if seq != h.previewSeq || ep != h.epoch {
			return
		}
		if a.err != nil {
			h.showPreview(picker, p+" · "+wireMessage(a.err), "", 0)
			return
		}
		h.showPreview(picker, p, a.text, at)
	})
}

// showPreview puts text in a picker's preview, the cursor at byte at: the position is moved away
// first, so the same number on another note still moves it.
func (h *Host) showPreview(picker, title, text string, at int) {
	at = min(max(at, 0), len(text))
	h.set("App."+picker+"PreviewTitle", title)
	h.set("App."+picker+"PreviewAt", -1)
	h.set("App."+picker+"PreviewText", text)
	h.set("App."+picker+"PreviewAt", cursorAt(text, at))
}

// cursorAt is byte at of text as the editor's cursorPosition counts it: characters (grapheme
// clusters) before it, a line break (\r\n, \r or \n) one, as the editor splits them. A byte
// inside a character is that character's start. A count of runes lands late after a combining
// mark or a joined emoji.
func cursorAt(text string, at int) int {
	n, end := 0, 0
	for c := range tuicore.Graphemes(text) {
		if end += len(c); end > at {
			break
		}
		n++
	}
	return n
}

// The open picker: a filter over the workspace's notes.

// openPicker opens File › Open over the workspace's notes.
func (h *Host) openPicker() {
	h.pickerFilter("")
	h.open("noteOpen")
}

// pickerFilter keeps the notes whose path holds text (case aside), and previews the first.
func (h *Host) pickerFilter(text string) {
	h.pickerRows = filterNotes(h.notesAll, text)
	rows := make([]rowOf, len(h.pickerRows))
	for i, p := range h.pickerRows {
		rows[i] = rowOf{"key": p, "path": p}
	}
	h.picker.Reset(rows)
	h.set("App.pickerStatus", fmt.Sprintf("notes (%d of %d)", len(rows), len(h.notesAll)))
	if len(rows) > 0 {
		h.preview("open", h.pickerRows[0], 0)
	} else {
		h.showPreview("open", "", "", 0)
	}
}

func filterNotes(all []string, text string) []string {
	text = strings.ToLower(strings.TrimSpace(text))
	var out []string
	for _, p := range all {
		if strings.Contains(strings.ToLower(p), text) {
			out = append(out, p)
		}
	}
	return out
}

// previewPick shows the open picker's row i.
func (h *Host) previewPick(i int) {
	if i >= 0 && i < len(h.pickerRows) {
		h.preview("open", h.pickerRows[i], 0)
	}
}

// pickerSelect opens the open picker's row i.
func (h *Host) pickerSelect(i int) {
	if i < 0 || i >= len(h.pickerRows) {
		return
	}
	p := h.pickerRows[i]
	h.closeDialog("noteOpen")
	h.openPath(p)
}

// The new-note picker: a path, over the workspace's notes to take a folder from.

// newNoteFilter lists the notes under what the path holds so far, and previews the first.
func (h *Host) newNoteFilter(text string) {
	h.newRows = filterNotes(h.notesAll, text)
	rows := make([]rowOf, len(h.newRows))
	for i, p := range h.newRows {
		rows[i] = rowOf{"key": p, "path": p}
	}
	h.newList.Reset(rows)
	if len(rows) > 0 {
		h.preview("new", h.newRows[0], 0)
	} else {
		h.showPreview("new", "", "", 0)
	}
}

// previewNew shows the new-note picker's row i.
func (h *Host) previewNew(i int) {
	if i >= 0 && i < len(h.newRows) {
		h.preview("new", h.newRows[i], 0)
	}
}

// newNoteFolder writes row i's folder into the path, for a name to follow.
func (h *Host) newNoteFolder(i int) {
	if i < 0 || i >= len(h.newRows) {
		return
	}
	dir := path.Dir(h.newRows[i])
	if dir == "." {
		dir = ""
	} else {
		dir += "/"
	}
	h.set("App.newNotePath", "\x00") // moved away first, so the same folder twice still writes it
	h.set("App.newNotePath", dir)
}

// marks is the search's words, as the preview's highlighter marks them.
type marks struct{ terms atomic.Pointer[[]string] }

func (m *marks) Store(terms []string) { m.terms.Store(&terms) }

func (m *marks) load() []string {
	if p := m.terms.Load(); p != nil {
		return *p
	}
	return nil
}

// searchHighlighter is Markdown's, with the search's words marked over it (the Alert style).
func (h *Host) searchHighlighter() highlight.Highlighter {
	md := markdown.Highlighter()
	return highlight.HighlighterFunc(func(line string, prev highlight.State) ([]highlight.Span, highlight.State) {
		spans, next := md.HighlightBlock(line, prev)
		terms := h.marks.load()
		if len(terms) == 0 || line == "" {
			return spans, next
		}
		styles := make([]highlight.Style, len(line))
		for _, s := range spans {
			for i := s.Start; i < s.End && i < len(styles); i++ {
				styles[i] = s.Style
			}
		}
		marked := false
		for i := 0; i < len(line); {
			for _, t := range terms {
				if end := foldAt(line, i, t); end > i {
					for j := i; j < end; j++ {
						styles[j] = highlight.Alert
					}
					marked = true
				}
			}
			_, n := utf8.DecodeRuneInString(line[i:])
			i += n
		}
		if !marked {
			return spans, next
		}
		var out []highlight.Span
		for i := 0; i < len(styles); {
			j := i
			for j < len(styles) && styles[j] == styles[i] {
				j++
			}
			if styles[i] != highlight.Normal {
				out = append(out, highlight.Span{Start: i, End: j, Style: styles[i]})
			}
			i = j
		}
		return out, next
	})
}

// foldAt is where term t (lower case) ends when it matches s from byte i, case aside; -1 when it
// does not. The offsets are s's own: lower-casing can change a rune's length, so a match found in
// strings.ToLower(s) would mark the wrong bytes of s.
func foldAt(s string, i int, t string) int {
	for _, tr := range t {
		if i >= len(s) {
			return -1
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		if unicode.ToLower(r) != tr {
			return -1
		}
		i += n
	}
	return i
}
