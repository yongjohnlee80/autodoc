package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/kind"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE SEARCH PICKER — Go › Search: the words typed searched by the daemon as they are typed, the
// hits listed, the file under the cursor previewed at its section (SearchPicker.qml, a pick[hit]).

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
	h.openRefTo(fileRef{h.ws, x.path}, true, nil, &landing{at: x.byteStart}) // at its section
	switch x.hold {
	case "stale":
		h.notify(x.path + " changed since this backend's build indexed it: the section may have moved")
	case "unchecked":
		h.notify(x.path + " is held, not yet checked against its file: the section may have moved")
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
