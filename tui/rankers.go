package tui

import (
	"context"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/search/rank"
)

// THE RANKER MODELS — AI models' second tab
// (docs/decisions/search.md#re-ranking-with-one-global-ranker): the rankers search's second stage
// can use, the one in use and the window of top candidates it ranks, each one's usage and recent
// calls, and the form that adds or edits one (endpoints.go). A key goes in through the form and is
// never shown again. A build that supplies its own ranker shows it, read-only, in place of the one
// in use: the stored rankers can still be added, edited and removed, but none chosen.

// shortModel is a model's name without its owner: bge-reranker-v2-m3 for BAAI/bge-reranker-v2-m3.
func shortModel(m string) string {
	if i := strings.LastIndex(m, "/"); i >= 0 {
		return m[i+1:]
	}
	return m
}

// defaultTEIURL is where AutoDoc's TEI playbook (shell/tei/tei-rerank.sh) serves a re-ranker, so a
// local one is a step away: Add…, TEI, no key, Save, Use.
const defaultTEIURL = "http://127.0.0.1:18080"

// rankerKinds are the form's kinds; a TEI server serves one model and names it, so its model is
// not typed.
var rankerKinds = []endpointKind{
	{kind: "tei", label: "TEI", base: defaultTEIURL, keyLabel: "API key (if the server takes one)"},                // text-embeddings-inference, serving a re-ranker
	{kind: "rerank-api", label: "Rerank API", base: "https://api.cohere.com/v2", keyLabel: "API key", model: true}, // Cohere-style: POST {base}/rerank
}

// rankers is the rankers' list and form, the build's own ranker when it supplies one, and the
// window the one in use ranks.
type rankers struct {
	*endpoints
	supplied string // the build's own ranker: shown, read-only, in place of the one in use
	listed   func() // the dialog's buttons follow the list (aimodels.go)
}

func newRankers(c Core, listed func()) *rankers {
	r := &rankers{listed: listed}
	r.endpoints = newEndpoints(c, endpointSpec{
		noun: "ranker", verb: "ranker", list: "ranker.list", ranker: true,
		kinds:     rankerKinds,
		blank:     endpointRow{kind: "tei", base: defaultTEIURL},
		addTitle:  "add a ranker",
		checkHint: "Check asks the ranker what it serves",
		columns:   []string{"use", "name", "kind", "model", "apiKey"},
		listed:    r.status,
		removal:   r.removal, formOpened: r.formOpened, kindShown: r.kindShown, checked: r.checked,
	})
	return r
}

func (r *rankers) State() map[string]any {
	s := r.endpoints.State()
	s["App.rankerModelShown"] = false
	s["App.rankerWindow"], s["App.rankerWindowError"] = "", ""
	return s
}

func (r *rankers) Commands() map[string]Command {
	return map[string]Command{
		"App.showRanker":        oneNumber("a row", r.detail),
		"App.startEditRanker":   oneNumber("a row", r.startEdit),
		"App.stopRanker":        none(r.stop),
		"App.startRankerWindow": none(r.startWindow),
		"App.saveRankerWindow":  oneString("a number of candidates", r.saveWindow),
		"App.rankerKindChosen":  numberAndString("a row and the base URL", r.kindChosen),
		"App.checkRanker":       twoStrings("the base URL and the key", r.check),
		"App.saveRanker": fourStrings("a name, the base URL, the model and the key", func(name, base, model, key string) {
			r.save(name, base, model, key, "")
		}),
	}
}

// status reads the build's own ranker from the list, the dialog's buttons following it, and is the
// line under the list.
func (r *rankers) status(m map[string]any, active, why string) string {
	r.supplied = str(m, "supplied")
	r.listed()
	return rankersStatus(r.c, active, r.supplied, why, num(m, "window"))
}

// rankersStatus is the line under the list: the ranker in use and its window, or the build's own.
func rankersStatus(t wording, active, supplied, why string, window int64) string {
	switch {
	case supplied != "" && why != "":
		return t.Trf("autodoc.text.rankers.re_ranking_supplied_by_the_build_no_probe_answer", supplied, window, why)
	case supplied != "":
		return t.Trf("autodoc.text.rankers.re_ranking_supplied_by_the_build", supplied, window)
	case active != "" && why != "":
		return t.Trf("autodoc.text.rankers.re_ranking_with_the_chosen_ranker_did_not_start", active, window, why)
	case active != "":
		return t.Trf("autodoc.text.rankers.re_ranking_with_over_the_top_candidates", active, window)
	case why != "":
		return t.Trf("autodoc.text.rankers.not_re_ranking_the_chosen_ranker_did_not_start", why)
	}
	return t.Tr("autodoc.text.rankers.not_re_ranking_add_a_ranker_then_use_it")
}

// use makes ranker i the one search's second stage uses; one that does not set up is refused, and
// the one in use stays.
func (r *rankers) use(i int) {
	row, ok := r.at(i)
	if !ok || r.supplied != "" {
		return
	}
	r.c.Set("App.rankersStatus", r.c.Trf("autodoc.text.rankers.setting_up", row.name))
	r.choose(row.name, func(err error) {
		r.c.Set("App.rankersStatus", r.c.Trf("autodoc.text.rankers.not_used", row.name, wireMessage(err)))
	},
		func() { r.c.Notify(r.c.Trf("autodoc.notice.rankers.search_is_re_ranked_with", row.name)) })
}

// stop uses no ranker: the hits stay in recall order.
func (r *rankers) stop() {
	if r.supplied != "" {
		return
	}
	r.choose("", func(err error) {
		r.c.Set("App.rankersStatus", r.c.Trf("autodoc.text.rankers.still_re_ranking", wireMessage(err)))
	},
		func() { r.c.Notify(r.c.Tr("autodoc.notice.rankers.search_is_not_re_ranked_the_hits_are_in_recall_o")) })
}

// startWindow opens the window's field on the one in use.
func (r *rankers) startWindow() {
	if r.supplied != "" {
		return
	}
	r.c.Set("App.rankerWindowError", r.c.Trf("autodoc.text.rankers.to_more_ranks_deeper_and_takes_longer_a_cpu_rank", rank.MinWindow, rank.MaxWindow))
	setField(r.c, "App.rankerWindow", "")
	r.c.Open("rankerWindow")
	r.c.Go(func(ctx context.Context) any {
		res, err := r.c.Call(ctx, "ranker.list")
		if err != nil {
			return int64(rank.DefaultWindow)
		}
		return num(asMap(res), "window")
	}, func(v any) { setField(r.c, "App.rankerWindow", strconv.FormatInt(v.(int64), 10)) })
}

// saveWindow sets the window; one outside the bounds opens the field again, saying why.
func (r *rankers) saveWindow(text string) {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || n < rank.MinWindow || n > rank.MaxWindow {
		r.c.Set("App.rankerWindowError", r.c.Trf("autodoc.text.rankers.window_not_saved", rank.MinWindow, rank.MaxWindow, strconv.Quote(strings.TrimSpace(text))))
		r.c.Open("rankerWindow")
		return
	}
	r.c.Go(func(ctx context.Context) any {
		_, err := r.c.Call(ctx, "ranker.window", n)
		return err
	}, func(v any) {
		if err, _ := v.(error); err != nil {
			r.c.Set("App.rankerWindowError", r.c.Trf("autodoc.text.rankers.not_saved", wireMessage(err)))
			r.c.Open("rankerWindow")
			return
		}
		r.c.Notify(r.c.Trf("autodoc.notice.rankers.the_ranker_ranks_the_top_candidates", n))
		r.load()
		r.c.SearchModelsChanged(true)
	})
}

// removal asks before removing ranker row.
func (r *rankers) removal(row endpointRow, remove func()) {
	r.c.Ask(Question{Title: r.c.Tr("autodoc.question.rankers.remove_the_ranker"), Text: r.c.Trf("autodoc.question.rankers.remove_the_ranker_its_key_its_usage_and_its_log", row.name), OnYes: remove})
}

// formOpened is the form's title on Add… and what Check does, in the UI's language: the spec's
// are English, set as the form opens (endpoints.fill).
func (r *rankers) formOpened(endpointRow) {
	if r.editing == "" {
		r.c.Set("App.rankerFormTitle", r.c.Tr("autodoc.text.rankers.add_a_ranker"))
	}
	r.spec.checkHint = r.c.Tr("autodoc.text.rankers.check_asks_the_ranker_what_it_serves")
}

// kindShown shows the model field for a kind whose model is typed, and the key field's label in
// the UI's language (one sealed is endpoints.showKind's).
func (r *rankers) kindShown(k endpointKind) {
	r.c.Set("App.rankerModelShown", k.model)
	if k.keyLabel != "" && (r.editing == "" || !r.hasKey) {
		label := r.c.Tr("autodoc.text.rankers.api_key")
		if k.kind == "tei" {
			label = r.c.Tr("autodoc.text.rankers.api_key_if_the_server_takes_one")
		}
		r.c.Set("App.rankerKeyLabel", label)
	}
}

// checked says what the ranker serves: a TEI server names its model; a rerank API names none, its
// model typed.
func (r *rankers) checked(models []string, err error) {
	switch {
	case err != nil:
		r.c.Set("App.rankerModelsStatus", r.c.Trf("autodoc.text.rankers.not_a_ranker", wireMessage(err)))
	case len(models) == 0:
		r.c.Set("App.rankerModelsStatus", r.c.Tr("autodoc.text.rankers.an_endpoint_that_names_no_models_type_the_model"))
	default:
		r.c.Set("App.rankerModelsStatus", r.c.Trf("autodoc.text.rankers.it_ranks_with", strings.Join(models, ", ")))
	}
}

// rankedTitle is what the hits' title says of their order: the ranker's, or why it is recall's.
func rankedTitle(t wording, rk map[string]any) string {
	switch str(rk, "state") {
	case "ready":
		return " · " + t.Trf("autodoc.text.rankers.re_ranked_by", shortModel(str(rk, "model")))
	case "error":
		return " · " + t.Trf("autodoc.text.rankers.not_re_ranked", str(rk, "error"))
	}
	return ""
}
