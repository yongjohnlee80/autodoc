package tui

import (
	"context"
	"fmt"
	"path"
	"strings"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/golib/highlight"
	"github.com/yongjohnlee80/golib/parse/markdown"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE PICKERS — search, open and new file: the picker's layout (the fields over the list on the
// left, the file under the cursor on the right, the buttons beneath), each over the workspace's
// files through the daemon.

// hit is one section a search found.
type hit struct {
	path, breadcrumb string
	byteStart        int
	relevance        float64 // 0 to 1: 1 is first in every retriever the search ran
	hold             string  // "" interpreted; current, stale or unchecked: a document the daemon holds
}

// relevanceText is a hit's relevance as the list shows it, a percentage.
func relevanceText(r float64) string { return fmt.Sprintf("%3.0f%%", 100*min(max(r, 0), 1)) }

// searchLive runs the search as it is typed: the latest answers, an older one dropped.
func (h *Host) searchLive(q string) {
	q = strings.TrimSpace(q)
	h.searchQuery = q
	if h.searchCancel != nil {
		h.searchCancel()
		h.searchCancel = nil
	}
	tk, ws := h.ticket(&h.searchSeq, TicketWorkspace), h.ws
	h.marks.Store(termsOf(q))
	h.set("App.searchStatus", "")
	if q == "" {
		h.searchWaiting = false
		if h.searchWaitToast {
			h.notifyDone(toastSearchWait, "search cleared")
			h.searchWaitToast = false
		}
		h.searchPick.show(nil)
		h.set("App.hitsTitle", "hits")
		return
	}
	queryCtx, cancel := context.WithCancel(h.ctx)
	h.searchCancel = cancel
	h.set("App.hitsTitle", "hits · searching "+ws)
	h.searchWaitLine(tk, h.searchSeq)
	if remaining := h.prog.emb.working(); remaining > 0 {
		h.notifyOngoing(toastSearchWait, fmt.Sprintf("search in %s is waiting: %d texts still embedding", ws, remaining))
		h.searchWaitToast = true
	}
	type answer struct {
		hits           []hit
		mode, semantic string
		ranked         string // the title's note of the hits' order (rankedTitle)
		rankedBy       string // the model that ordered them, "" when none did
		err            error
	}
	opts := map[string]any{"limit": int64(100)}
	if stages := h.stageBoxes().stages(); stages != nil {
		opts["stages"] = stages
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(queryCtx, "search.query", ws, q, opts)
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		var out []hit
		for _, x := range asList(m["hits"]) {
			hm := asMap(x)
			start, _ := hm["byte_start"].(int64)
			rel, _ := hm["relevance"].(float64)
			out = append(out, hit{path: str(hm, "path"), breadcrumb: str(hm, "breadcrumb"), byteStart: int(start), relevance: rel,
				hold: str(hm, "hold")})
		}
		rk := asMap(m["rank"])
		a := answer{hits: out, mode: str(m, "mode_used"), semantic: str(m, "semantic"), ranked: rankedTitle(rk)}
		if str(rk, "state") == "ready" {
			a.rankedBy = str(rk, "model")
		}
		return a
	}, func(a answer) {
		if !tk.Current() {
			return
		}
		cancel()
		h.searchCancel = nil
		h.set("App.searchStatus", "")
		if a.err != nil {
			if h.searchWaitToast {
				h.notifyDone(toastSearchWait, "search did not complete")
				h.searchWaitToast = false
			}
			h.searchPick.show(nil)
			h.set("App.hitsTitle", "hits · "+wireMessage(a.err))
			msg := "search in " + ws + " is unavailable: " + wireMessage(a.err)
			if msg != h.lastSearchError {
				h.notify(msg)
				h.lastSearchError = msg
			}
			return
		}
		if a.semantic != "error" {
			h.lastSearchError = ""
		}
		if len(a.hits) == 0 && a.semantic == "partial" && h.prog.emb.working() > 0 {
			if !h.searchWaiting {
				h.notify(fmt.Sprintf("no hits yet in %s: %d texts still embedding; search will refresh", ws, h.prog.emb.working()))
				h.searchWaiting = true
			}
		} else {
			h.searchWaiting = false
		}
		if a.semantic == "error" {
			msg := "semantic search could not answer in " + ws + "; results are by words only"
			if msg != h.lastSearchError {
				h.notify(msg)
				h.lastSearchError = msg
			}
		}
		if len(a.hits) > 0 && h.searchWaitToast {
			h.notifyDone(toastSearchWait, "search has results in "+ws)
			h.searchWaitToast = false
		}
		// the order first: a narrow pane keeps what is read first
		h.hitsRanked = a.ranked
		h.rankedBy(a.rankedBy)
		h.set("App.hitsTitle", fmt.Sprintf("hits (%d)%s · %s · semantic %s", len(a.hits), a.ranked, a.mode, a.semantic))
		h.searchPick.show(a.hits) // the hits, the first previewed
	})
}

// searchWaits are what the search's waiting line says, one at a time, while a search has not
// answered: the provider is embedding the query, or the files, or loading its model.
var searchWaits = []string{
	"warming up the engine",
	"prepping the vectors",
	"crates are being fetched",
	"asking the GPU nicely",
	"herding the tokens",
	"polishing the cosines",
	"consulting the embeddings",
	"untangling the meanings",
}

// searchWaitDelay is how long a search may take before its waiting line turns: an answer sooner
// shows nothing at all.
const searchWaitDelay = 300 * time.Millisecond

// searchWaitFrames is how many spinner frames one of searchWaits stays before the next.
const searchWaitFrames = 16

// searchWaitLine turns the line under the search's field while search n (its ticket tk) has not
// answered: a spinner and one of searchWaits, changing every few seconds. It starts after
// searchWaitDelay and stops when the search answers, is replaced, cleared or closed (each blanks
// the line).
func (h *Host) searchWaitLine(tk Ticket, n uint64) {
	frame := int(n) * searchWaitFrames // a different message first, search to search
	waiting := func() bool { return tk.Current() && h.searchCancel != nil }
	var turn func()
	turn = func() {
		if !waiting() {
			return
		}
		msg := searchWaits[(frame/searchWaitFrames)%len(searchWaits)]
		h.set("App.searchStatus", spinFrames[frame%len(spinFrames)]+" "+msg+"…")
		frame++
		h.after(spinEvery, turn)
	}
	h.after(searchWaitDelay, func() {
		if waiting() {
			turn()
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
func (h *Host) openSearch() {
	h.searchOpen = true
	h.open("searchPicker")
	h.loadStageRanker(false)
	h.refreshSearch()
}

func (h *Host) searchClosed() {
	h.searchOpen = false
	h.searchSeq++
	h.set("App.searchStatus", "")
	if h.searchCancel != nil {
		h.searchCancel()
		h.searchCancel = nil
	}
	if h.searchWaitToast {
		h.notifyDone(toastSearchWait, "search closed")
		h.searchWaitToast = false
	}
}

// clearSearch empties the search: its words, in the picker's field too, and its hits, so the next
// search starts afresh. why is said when there was a search to clear.
func (h *Host) clearSearch(why string) {
	had := h.searchQuery != ""
	h.searchLive("")
	h.set("App.searchText", "\x00") // moved away first, so an empty field twice still clears it
	h.set("App.searchText", "")
	if had {
		h.notify("search cleared: " + why)
	}
}

// refreshSearch replaces results produced under an earlier workspace or model.
func (h *Host) refreshSearch() {
	if h.searchOpen && h.searchQuery != "" {
		h.searchLive(h.searchQuery)
	}
}

// newSearchPick is the search's picker: its hits are what the daemon found for the words typed.
func (h *Host) newSearchPick() *pick[hit] {
	return &pick[hit]{c: core{h}, dialog: "searchPicker", name: "search",
		list:  newRows(tuidecl.NewListModel("key", "hit", "path", "section"), hitRowOf),
		query: h.searchLive,
		preview: func(x hit) func(context.Context) answerOf[pane] {
			return h.readPreview(x.path, x.byteStart, x.hold != "")
		},
		// a hit read as Markdown offers its Rendered view
		shown:  func(x hit, a answerOf[pane]) { h.previewRendered(x.path, a.err == nil) },
		scope:  TicketWorkspace,
		choose: h.openHit,
		cancel: h.searchClosed}
}

// openHit opens the file of hit x at its section: the picker has closed.
func (h *Host) openHit(x hit) {
	h.searchClosed()
	h.openAt = x.byteStart
	h.openPath(x.path)
	switch x.hold {
	case "stale":
		h.notify(x.path + " changed since this backend's build indexed it: the section may have moved")
	case "unchecked":
		h.notify(x.path + " is held, not yet checked against its file: the section may have moved")
	}
}

// readPreview is the work that reads file p of the workspace for a picker's preview, the cursor at
// byte at: through the daemon, off the loop. held is a hit of a document another backend's build
// indexed, which says so when this one cannot read it.
func (h *Host) readPreview(p string, at int, held bool) func(context.Context) answerOf[pane] {
	ws := h.ws
	return func(ctx context.Context) answerOf[pane] {
		res, err := h.call(ctx, "doc.read", ws, p)
		if err != nil {
			why := wireMessage(err)
			if held {
				// a held hit this backend cannot read (a PDF under a community build): say why
				why = "held: this backend's build does not read this file; it was indexed by another (" + why + ")"
			}
			return answerOf[pane]{v: pane{title: p + " · " + why}, err: err}
		}
		b, _ := asMap(res)["content"].([]byte)
		return answerOf[pane]{v: pane{title: p, text: string(b), at: at}}
	}
}

// previewRendered gives the search's preview its Rendered view for a hit read as Markdown, as
// setRendered does the editor's: a note or a derived document; code, YAML and plain text stay
// Raw. read is false when the hit could not be read, which shows no text to render.
// The preview binds App.searchPreviewRendered to its renderedEnabled: an id inside the picker's
// own file is not the host's to look up.
func (h *Host) previewRendered(p string, read bool) {
	h.set("App.searchPreviewRendered", read && h.resolver().Of(p).Traits().ReadsAs == kind.Markdown)
}

// The open picker: a filter over the workspace's files.

// newOpenPick is File › Open's picker over the workspace's files.
func (h *Host) newOpenPick() *pick[string] {
	return &pick[string]{c: core{h}, dialog: "fileOpen", name: "open",
		list:   newRows(tuidecl.NewListModel("key", "path"), pathRowOf),
		status: "App.pickerStatus", noun: "files",
		match:   func(p string) string { return p },
		preview: func(p string) func(context.Context) answerOf[pane] { return h.readPreview(p, 0, false) },
		scope:   TicketWorkspace,
		choose:  h.openPath}
}

// openPicker opens File › Open over the workspace's files.
func (h *Host) openPicker() {
	h.openPick.setAll(h.files.Paths)
	h.openPick.open()
}

// pathRowOf is a file's row in a list of paths.
func pathRowOf(_ int, p string) rowOf { return rowOf{"key": p, "path": p} }

// The new-file picker: a path, over the workspace's files to take a folder from.

// newNewPick is the draft's name: the path typed, over the workspace's files under it. Enter on a
// file takes its folder; accept creates the file at the path.
func (h *Host) newNewPick() *pick[string] {
	return &pick[string]{c: core{h}, dialog: "fileName", name: "new",
		list:    newRows(tuidecl.NewListModel("key", "path"), pathRowOf),
		match:   func(p string) string { return p },
		preview: func(p string) func(context.Context) answerOf[pane] { return h.readPreview(p, 0, false) },
		scope:   TicketWorkspace,
		enter:   h.newFileFolder,
		submit:  h.createFile}
}

// newFileFolder writes file p's folder into the path, for a name to follow.
func (h *Host) newFileFolder(p string) {
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	} else {
		dir += "/"
	}
	h.set("App.newFilePath", "\x00") // moved away first, so the same folder twice still writes it
	h.set("App.newFilePath", dir)
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
func (h *Host) searchHighlighter() highlight.Highlighter { return markedHighlighter(&h.marks) }

func (h *Host) searchDefinition() highlight.Definition {
	d := markdown.Definition()
	d.Name = "Markdown (search)"
	d.Extensions = nil
	d.Aliases = nil
	factory := d.SourceFactory
	d.SourceFactory = func(catalog *highlight.Catalog) highlight.Source {
		source := factory(catalog)
		source.Highlighter = markedHighlighterWith(&h.marks, source.Highlighter)
		return source
	}
	return d
}

// markedHighlighter is Markdown's, with the words in m marked over it (the Alert style), read
// afresh on every line: the preview's for the search's words, the page's for the find's.
func markedHighlighter(m *marks) highlight.Highlighter {
	return markedHighlighterWith(m, markdown.Highlighter())
}

func markedHighlighterWith(m *marks, base highlight.Highlighter) highlight.Highlighter {
	return highlight.HighlighterFunc(func(line string, prev highlight.State) ([]highlight.Span, highlight.State) {
		spans, next := base.HighlightBlock(line, prev)
		return markedSpans(m, line, spans), next
	})
}

func markedSpans(m *marks, line string, spans []highlight.Span) []highlight.Span {
	terms := m.load()
	if len(terms) == 0 || line == "" {
		return spans
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
		return spans
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
	return out
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

// hitRows are the hit list's rows.
func hitRows(hits []hit) []rowOf {
	rows := make([]rowOf, len(hits))
	for i, x := range hits {
		rows[i] = hitRowOf(i, x)
	}
	return rows
}

// hitRowOf is hit i's row: a held document's hit says so after its section.
func hitRowOf(i int, x hit) rowOf {
	section := x.breadcrumb
	if l := holdLabel(x.hold); l != "" {
		section += " · " + l
	}
	return rowOf{"key": fmt.Sprintf("%d\t%s", i, x.path), "hit": relevanceText(x.relevance), "path": x.path, "section": section}
}
