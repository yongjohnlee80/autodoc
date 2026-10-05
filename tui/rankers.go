package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/search/rank"
)

// THE RANKER MODELS — AI models' second tab (ADR 0215): the rankers search's second stage can use,
// the one in use and the window of top candidates it ranks, each one's usage and recent calls, and
// the form that adds or edits one. A key goes in through the form and is never shown again. A
// build that supplies its own ranker shows it, read-only, in place of the one in use: the stored
// rankers can still be added, edited and removed, but none chosen.

// rankerKind is one of the form's kinds: the store's name, how the form offers it, where it usually
// is, and whether its model is typed (a TEI server serves one, and names it).
type rankerKind struct {
	kind, label, base string
	model             bool
}

// shortModel is a model's name without its owner: bge-reranker-v2-m3 for BAAI/bge-reranker-v2-m3.
func shortModel(m string) string {
	if i := strings.LastIndex(m, "/"); i >= 0 {
		return m[i+1:]
	}
	return m
}

// searchRankedLine is what the search's ranker line says after an answer: the model that ordered
// its hits, or why it could not. Short: the picker's left side is narrow. "" when the answer says nothing of it (no ranker in use, or hits
// it does not rank: a facet's, or a search with no words), so the line keeps what it said.
func searchRankedLine(rk map[string]any) string {
	switch str(rk, "state") {
	case "ready":
		return "re-ranked by " + shortModel(str(rk, "model"))
	case "error":
		return "not re-ranked: " + str(rk, "error")
	}
	return ""
}

// searchRankerLine is what the search's ranker line says when the search opens, from ranker.list:
// the ranker in use, a build's own, or none.
func searchRankerLine(m map[string]any) string {
	if s := str(m, "supplied"); s != "" {
		return "ranker: " + shortModel(s) + " · build's"
	}
	active := str(m, "active")
	if active == "" {
		return "ranker: none"
	}
	line := "ranker: " + active
	for _, x := range asList(m["rankers"]) {
		r := asMap(x)
		if str(r, "name") != active {
			continue
		}
		if model := str(r, "model"); model != "" {
			line += " · " + shortModel(model)
		} else if str(r, "kind") == "tei" {
			line += " · TEI"
		}
	}
	if e := str(m, "error"); e != "" {
		line += " · unavailable: " + e
	}
	return line
}

// loadSearchRanker names the ranker in use on the search's ranker line when the search opens. A
// search answered first says more (which model ordered its hits), so this one then keeps quiet.
func (h *Host) loadSearchRanker() {
	gen := h.session.Gen()
	h.setSearchRanker("")
	type answer struct {
		line string
		err  error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "ranker.list")
		if err != nil {
			return answer{err: err}
		}
		return answer{line: searchRankerLine(asMap(res))}
	}, func(a answer) {
		if gen != h.session.Gen() || !h.searchOpen || h.searchRankerAnswered {
			return
		}
		if a.err != nil {
			h.setSearchRanker("ranker: " + wireMessage(a.err))
			return
		}
		h.setSearchRanker(a.line)
	})
}

// setSearchRanker sets the search's ranker line.
func (h *Host) setSearchRanker(line string) {
	h.searchRanker = line
	h.set("App.searchRanker", line)
}

// defaultTEIURL is where AutoDoc's TEI playbook (shell/tei/tei-rerank.sh) serves a re-ranker, so a
// local one is a step away: Add…, TEI, no key, Save, Use.
const defaultTEIURL = "http://127.0.0.1:18080"

var rankerKinds = []rankerKind{
	{"tei", "TEI", defaultTEIURL, false},                            // text-embeddings-inference, serving a re-ranker
	{"rerank-api", "Rerank API", "https://api.cohere.com/v2", true}, // Cohere-style: POST {base}/rerank
}

func rankerKindIndex(kind string) int {
	for i, k := range rankerKinds {
		if k.kind == kind {
			return i
		}
	}
	return 0
}

func rankerKindChoices() []string {
	labels := make([]string, len(rankerKinds))
	for i, k := range rankerKinds {
		labels[i] = k.label
	}
	return labels
}

// rankerRow is a ranker as the list and the form know it.
type rankerRow struct {
	name, kind, base, model string
	hasKey                  bool
}

// The AI models dialog's tabs: the buttons they share (Add, Edit, Use, Remove) act on the tab that
// is current.
const (
	embeddingTab = 0
	rankerTab    = 1
)

// aiTabMoved is the dialog's tab changed: its own buttons shown, the other's hidden, and the
// rankers listed when their tab shows. The buttons change once the tab's change is done: a button
// hidden while the view is still changing tabs would cancel a gesture outside an event.
func (h *Host) aiTabMoved(tab int) {
	h.aiTab = tab
	h.p.Post(func() {
		if h.aiTab != tab {
			return
		}
		h.set("App.aiEmbeddingTab", tab == embeddingTab)
		h.set("App.aiRankerTab", tab == rankerTab)
		h.syncRankerButtons()
		if tab == rankerTab {
			h.loadRankers()
		}
	})
}

// syncRankerButtons enables Use, Don't use and Window… only on the ranker tab, and only when the
// build does not supply its ranker.
func (h *Host) syncRankerButtons() {
	choice := h.aiTab == rankerTab && h.rankerSupplied == ""
	h.set("App.aiRankerChoice", choice)
	h.set("App.aiUseEnabled", h.aiTab == embeddingTab || choice)
}

func (h *Host) aiAdd() {
	if h.aiTab == rankerTab {
		h.startAddRanker()
		return
	}
	h.startAddProvider()
}

func (h *Host) aiEdit(provider int) {
	if h.aiTab == rankerTab {
		h.startEditRanker(h.rankerCursor)
		return
	}
	h.startEditProvider(provider)
}

func (h *Host) aiUse(provider int) {
	if h.aiTab == rankerTab {
		h.useRanker(h.rankerCursor)
		return
	}
	h.useProvider(provider)
}

func (h *Host) aiRemove(provider int) {
	if h.aiTab == rankerTab {
		h.startRemoveRanker(h.rankerCursor)
		return
	}
	h.startRemoveProvider(provider)
}

// loadRankers lists the rankers, the one in use and its window, why the one chosen is not in use if
// it is not, and the build's own ranker when it supplies one.
func (h *Host) loadRankers() {
	gen := h.session.Gen()
	type answer struct {
		rows                    []rankerRow
		active, error, supplied string
		window                  int64
		err                     error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "ranker.list")
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		var rows []rankerRow
		for _, x := range asList(m["rankers"]) {
			r := asMap(x)
			has, _ := r["has_key"].(bool)
			rows = append(rows, rankerRow{name: str(r, "name"), kind: str(r, "kind"), base: str(r, "base_url"), model: str(r, "model"), hasKey: has})
		}
		return answer{rows: rows, active: str(m, "active"), error: str(m, "error"), supplied: str(m, "supplied"), window: num(m, "window")}
	}, func(a answer) {
		if gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			h.set("App.rankersStatus", "rankers: "+wireMessage(a.err))
			return
		}
		h.rankerList, h.activeRanker, h.rankerSupplied = a.rows, a.active, a.supplied
		h.syncRankerButtons()
		rows := make([]rowOf, len(a.rows))
		for i, r := range a.rows {
			use := ""
			if r.name == a.active {
				use = "●"
			}
			key := "—"
			if r.hasKey {
				key = "sealed"
			}
			model := r.model
			if model == "" {
				model = "—" // a TEI server's own, named by Check
			}
			rows[i] = rowOf{"key": r.name, "use": use, "name": r.name, "kind": rankerKinds[rankerKindIndex(r.kind)].label, "model": model, "apiKey": key}
		}
		h.rankers.Reset(rows)
		h.set("App.rankersStatus", rankersStatus(a.active, a.supplied, a.error, a.window))
		if len(a.rows) > 0 {
			h.rankerDetail(min(max(h.rankerCursor, 0), len(a.rows)-1))
		} else {
			h.set("App.rankerDetail", "")
		}
	})
}

// rankersStatus is the line under the list: the ranker in use and its window, or the build's own.
func rankersStatus(active, supplied, why string, window int64) string {
	var s string
	switch {
	case supplied != "":
		s = fmt.Sprintf("re-ranking with %s, supplied by the build, over the top %d: the one in use cannot be changed here", supplied, window)
		if why != "" {
			s += " · it did not answer its probe: " + why
		}
		return s
	case active != "":
		s = fmt.Sprintf("re-ranking with %s over the top %d candidates", active, window)
	default:
		s = "not re-ranking: Add… a ranker, then Use it"
	}
	if why != "" {
		s += " · the chosen ranker did not start: " + why
	}
	return s
}

func (h *Host) rankerAt(i int) (rankerRow, bool) {
	if i < 0 || i >= len(h.rankerList) {
		return rankerRow{}, false
	}
	return h.rankerList[i], true
}

// rankerDetail shows ranker i's usage over the last week and its latest calls, and keeps it as the
// row the shared buttons act on.
func (h *Host) rankerDetail(i int) {
	r, ok := h.rankerAt(i)
	if !ok {
		return
	}
	h.rankerCursor = i
	h.rankerSeq++
	seq, gen := h.rankerSeq, h.session.Gen()
	type answer struct {
		text string
		err  error
	}
	do(h, func(ctx context.Context) answer {
		us, err := h.call(ctx, "ranker.usage", r.name, int64(7))
		if err != nil {
			return answer{err: err}
		}
		log, err := h.call(ctx, "ranker.log", r.name, int64(30))
		if err != nil {
			return answer{err: err}
		}
		return answer{text: usageText(r.name, asList(us), asList(log))}
	}, func(a answer) {
		if seq != h.rankerSeq || gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			h.set("App.rankerDetail", r.name+": "+wireMessage(a.err))
			return
		}
		h.set("App.rankerDetail", a.text)
	})
}

// useRanker makes ranker i the one search's second stage uses; one that does not set up is
// refused, and the one in use stays.
func (h *Host) useRanker(i int) {
	r, ok := h.rankerAt(i)
	if !ok || h.rankerSupplied != "" {
		return
	}
	h.set("App.rankersStatus", "setting up "+r.name+"…")
	h.rankerChoice(r.name, func(err error) string { return r.name + " not used: " + wireMessage(err) })
}

// stopRanking uses no ranker: the hits stay in recall order.
func (h *Host) stopRanking() {
	if h.rankerSupplied != "" {
		return
	}
	h.rankerChoice("", func(err error) string { return "still re-ranking: " + wireMessage(err) })
}

func (h *Host) rankerChoice(name string, refused func(error) string) {
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "ranker.use", name)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.rankersStatus", refused(err))
			return
		}
		if name == "" {
			h.notify("search is not re-ranked: the hits are in recall order")
		} else {
			h.notify("search is re-ranked with " + name)
		}
		h.loadRankers()
		h.refreshSearch()
	})
}

// startRankerWindow opens the window's field on the one in use.
func (h *Host) startRankerWindow() {
	if h.rankerSupplied != "" {
		return
	}
	h.set("App.rankerWindowError", fmt.Sprintf("%d to %d: more ranks deeper and takes longer; a CPU ranker answers 10–20 in a few seconds", rank.MinWindow, rank.MaxWindow))
	h.setField("App.rankerWindow", "")
	h.open("rankerWindow")
	do(h, func(ctx context.Context) int64 {
		res, err := h.call(ctx, "ranker.list")
		if err != nil {
			return rank.DefaultWindow
		}
		return num(asMap(res), "window")
	}, func(n int64) { h.setField("App.rankerWindow", strconv.FormatInt(n, 10)) })
}

// saveRankerWindow sets the window; one outside the bounds opens the field again, saying why.
func (h *Host) saveRankerWindow(text string) {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || n < rank.MinWindow || n > rank.MaxWindow {
		h.set("App.rankerWindowError", fmt.Sprintf("not saved: the window is %d to %d candidates, not %q", rank.MinWindow, rank.MaxWindow, strings.TrimSpace(text)))
		h.open("rankerWindow")
		return
	}
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "ranker.window", n)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.rankerWindowError", "not saved: "+wireMessage(err))
			h.open("rankerWindow")
			return
		}
		h.notify(fmt.Sprintf("the ranker ranks the top %d candidates", n))
		h.loadRankers()
		h.refreshSearch()
	})
}

// startRemoveRanker asks before removing ranker i, with its usage and log.
func (h *Host) startRemoveRanker(i int) {
	r, ok := h.rankerAt(i)
	if !ok {
		return
	}
	h.removingRanker = r.name
	h.set("App.rankerRemoveQuestion", "Remove the ranker "+r.name+"? Its key, its usage and its log go; "+
		"if it is in use, search is no longer re-ranked.")
	h.open("rankerRemove")
}

func (h *Host) removeRankerConfirmed() {
	name := h.removingRanker
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "ranker.remove", name)
		return err
	}, func(err error) {
		if err != nil {
			h.failed("remove "+name, err)
			return
		}
		h.notify("removed the ranker " + name)
		h.loadRankers()
		h.refreshSearch()
	})
}

// The form.

// startAddRanker opens the form for a new ranker: a TEI server, to begin with.
func (h *Host) startAddRanker() {
	h.editingRanker = ""
	h.fillRankerForm("add a ranker", rankerRow{kind: "tei", base: rankerKinds[0].base})
}

// startEditRanker opens the form on ranker i; its key is kept unless one is typed.
func (h *Host) startEditRanker(i int) {
	r, ok := h.rankerAt(i)
	if !ok {
		return
	}
	h.editingRanker = r.name
	h.fillRankerForm("edit "+r.name, r)
}

func (h *Host) fillRankerForm(title string, r rankerRow) {
	h.rankerFormKind, h.rankerFormHasKey = rankerKindIndex(r.kind), r.hasKey
	h.set("App.rankerFormTitle", title)
	h.set("App.rankerFormError", "")
	h.set("App.rankerKindIndex", h.rankerFormKind)
	h.setField("App.rankerName", r.name)
	h.setField("App.rankerBase", r.base)
	h.setField("App.rankerModel", r.model)
	h.setField("App.rankerKey", "")
	h.showRankerFields()
	h.set("App.rankerModelsStatus", "Check asks the ranker what it serves")
	h.open("rankerEdit")
}

// showRankerFields shows the model field for a kind whose model is typed, and says what leaving
// the key empty does.
func (h *Host) showRankerFields() {
	k := rankerKinds[h.rankerFormKind]
	h.set("App.rankerModelShown", k.model)
	switch {
	case h.editingRanker != "" && h.rankerFormHasKey:
		h.set("App.rankerKeyLabel", "API key (sealed; leave empty to keep it)")
	case k.model:
		h.set("App.rankerKeyLabel", "API key")
	default:
		h.set("App.rankerKeyLabel", "API key (if the server takes one)")
	}
}

// rankerKindChosen is the form's kind chooser: its base URL filled in when the one there is another
// kind's default, and the model field shown where the kind types one.
func (h *Host) rankerKindChosen(i int, base string) {
	if i < 0 || i >= len(rankerKinds) {
		return
	}
	h.rankerFormKind = i
	for _, k := range rankerKinds {
		if strings.TrimSpace(base) == "" || base == k.base {
			h.setField("App.rankerBase", rankerKinds[i].base)
			break
		}
	}
	h.showRankerFields()
}

// checkRanker asks the ranker the form describes what it serves: a TEI server names its model; a
// rerank API names none, its model typed. It is the form's kind and base URL that are asked, an
// edited ranker lending its stored key when none is typed.
func (h *Host) checkRanker(base, key string) {
	spec := map[string]any{"kind": rankerKinds[h.rankerFormKind].kind, "base_url": strings.TrimSpace(base)}
	args := []any{spec}
	if key != "" {
		spec["key"] = key
	} else if h.editingRanker != "" {
		args = []any{h.editingRanker, spec} // its stored key, opened by the daemon
	}
	h.set("App.rankerModelsStatus", "asking the ranker…")
	gen := h.session.Gen()
	type answer struct {
		models []string
		err    error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "ranker.models", args...)
		if err != nil {
			return answer{err: err}
		}
		var out []string
		for _, m := range asList(res) {
			if s, ok := m.(string); ok {
				out = append(out, s)
			}
		}
		return answer{models: out}
	}, func(a answer) {
		if gen != h.session.Gen() {
			return
		}
		switch {
		case a.err != nil:
			h.set("App.rankerModelsStatus", "not a ranker: "+wireMessage(a.err))
		case len(a.models) == 0:
			h.set("App.rankerModelsStatus", "an endpoint that names no models: type the model it ranks with")
		default:
			h.set("App.rankerModelsStatus", "it ranks with "+strings.Join(a.models, ", "))
		}
	})
}

// saveRanker adds the ranker the form describes, or rewrites the one it edits. A refusal opens the
// form again, saying why: the one in use is set up and probed with the edit before it is saved.
func (h *Host) saveRanker(name, base, model, key string) {
	k := rankerKinds[h.rankerFormKind]
	// a TEI ranker's model is its server's: the store keeps none, whatever the hidden field holds
	spec := map[string]any{"name": strings.TrimSpace(name), "kind": k.kind, "base_url": strings.TrimSpace(base), "model": strings.TrimSpace(model)}
	if key != "" {
		spec["key"] = key
	}
	editing := h.editingRanker
	do(h, func(ctx context.Context) error {
		var err error
		if editing == "" {
			_, err = h.call(ctx, "ranker.add", spec)
		} else {
			_, err = h.call(ctx, "ranker.update", editing, spec)
		}
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.rankerFormError", "not saved: "+wireMessage(err))
			h.open("rankerEdit")
			return
		}
		h.notify("saved the ranker " + spec["name"].(string))
		h.loadRankers()
		h.refreshSearch()
	})
}

// rankedTitle is what the hits' title says of their order: the ranker's, or why it is recall's.
func rankedTitle(rk map[string]any) string {
	switch str(rk, "state") {
	case "ready":
		return " · re-ranked by " + shortModel(str(rk, "model"))
	case "error":
		return " · not re-ranked: " + str(rk, "error")
	}
	return ""
}
