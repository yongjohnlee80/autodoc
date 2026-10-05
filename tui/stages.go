package tui

import (
	"context"
	"slices"
	"strings"
)

// THE SEARCH'S STAGES — the row of checkboxes along the search picker's foot: Lexical, Semantic and
// Rerank, each the stage search.query runs. Semantic shows only while an embedding model is in
// use, and Rerank only while a ranker is, named by its model; a box that does not show is off. All
// start checked, which is auto: the search runs every stage the daemon can, whatever this TUI has
// yet to learn of. A change searches again at once, and is kept as a preference, so a box that
// comes back comes back as it was left. One of Lexical and Semantic always stays checked.

// The stages, by the names search.query takes.
const (
	stageLexical  = "lexical"
	stageSemantic = "semantic"
	stageRerank   = "rerank"
)

// prefSearchStages keeps the boxes checked, by stage, comma-separated.
const prefSearchStages = "tui.search.stages"

// stageChoice is the boxes as the user left them, shown or not.
type stageChoice struct{ lexical, semantic, rerank bool }

func allStagesChecked() stageChoice { return stageChoice{true, true, true} }

// stageChoiceOf reads the preference: the stages named are checked.
func stageChoiceOf(s string) stageChoice {
	names := strings.Split(s, ",")
	return stageChoice{slices.Contains(names, stageLexical), slices.Contains(names, stageSemantic), slices.Contains(names, stageRerank)}
}

// String is the choice as the preference keeps it.
func (c stageChoice) String() string {
	var names []string
	for _, s := range []struct {
		on   bool
		name string
	}{{c.lexical, stageLexical}, {c.semantic, stageSemantic}, {c.rerank, stageRerank}} {
		if s.on {
			names = append(names, s.name)
		}
	}
	return strings.Join(names, ",")
}

// stageBoxes are the boxes as they show: which can, and which are checked; auto when every box is
// left checked, shown or not.
type stageBoxes struct {
	semanticShown, rerankShown bool
	on                         stageChoice
	auto                       bool
}

// boxesOf is choice over what is available: an embedding model in use (semantic), a ranker in use
// (rerank). A box that does not show is off; Lexical, always shown, is checked whenever Semantic is
// not, so a search always has a retriever.
func boxesOf(choice stageChoice, semantic, rerank bool) stageBoxes {
	b := stageBoxes{semanticShown: semantic, rerankShown: rerank, auto: choice == allStagesChecked()}
	b.on.semantic = semantic && choice.semantic
	b.on.rerank = rerank && choice.rerank
	b.on.lexical = choice.lexical || !b.on.semantic
	return b
}

// stages are the stages the boxes ask search.query for: nil for auto.
func (b stageBoxes) stages() []any {
	if b.auto {
		return nil
	}
	out := []any{}
	for _, s := range []struct {
		on   bool
		name string
	}{{b.on.lexical, stageLexical}, {b.on.semantic, stageSemantic}, {b.on.rerank, stageRerank}} {
		if s.on {
			out = append(out, s.name)
		}
	}
	return out
}

// toggle is the boxes' choice with stage flipped, or false when it is refused: a box that does not
// show, or the last retriever checked.
func (b stageBoxes) toggle(choice stageChoice, stage string) (stageChoice, bool) {
	switch stage {
	case stageLexical:
		if b.on.lexical && !b.on.semantic {
			return choice, false
		}
		choice.lexical = !b.on.lexical
	case stageSemantic:
		if !b.semanticShown || (b.on.semantic && !b.on.lexical) {
			return choice, false
		}
		choice.semantic = !b.on.semantic
	case stageRerank:
		if !b.rerankShown {
			return choice, false
		}
		choice.rerank = !b.on.rerank
	default:
		return choice, false
	}
	return choice, true
}

// setBox sets a box's checked, moved away first: a CheckBox flips itself when clicked, so a refused
// click leaves it showing the other state while the value the host holds has not changed, and
// only a change reaches it.
func (h *Host) setBox(name string, on bool) {
	h.set(name, !on)
	h.set(name, on)
}

// stageBoxes are the search's boxes now.
func (h *Host) stageBoxes() stageBoxes {
	return boxesOf(h.prefs.searchStages, h.prog.emb.on, h.stageRanker != "")
}

// syncStages shows the boxes as they are, and, when refresh is set and the stages a search asks
// for changed, searches again with them.
func (h *Host) syncStages(refresh bool) {
	b := h.stageBoxes()
	h.setBox("App.stageLexical", b.on.lexical)
	h.setBox("App.stageSemantic", b.on.semantic)
	h.set("App.stageSemanticShown", b.semanticShown)
	h.setBox("App.stageRerank", b.on.rerank)
	h.set("App.stageRerankText", "Rerank ("+h.stageRanker+")")
	h.set("App.stageRerankShown", b.rerankShown)
	sent := strings.Join(strs(b.stages()), ",") // "" is auto
	changed := sent != h.stagesSent
	h.stagesSent = sent
	if refresh && changed {
		h.refreshSearch()
	}
}

// toggleSearchStage is a box's click: the stage on or off, kept, and searched with at once. The
// last retriever checked stays checked, and the status line says why.
func (h *Host) toggleSearchStage(stage string) {
	choice, ok := h.stageBoxes().toggle(h.prefs.searchStages, stage)
	if !ok {
		if stage == stageLexical || stage == stageSemantic {
			h.say("search needs Lexical or Semantic: one stays checked")
		}
		h.syncStages(false) // a clicked box flipped itself: it shows the stages as they are again
		return
	}
	h.say("")
	h.setPref(prefSearchStages, choice.String(), func(p *prefs) { p.searchStages = choice })
}

// loadStageRanker learns the ranker in use, for the Rerank box: its model, or its name while the
// model is not known (a TEI server names its own, which a ranked answer then says). refresh
// searches again whatever changed; otherwise only a change of the stages asked for does.
func (h *Host) loadStageRanker(refresh bool) {
	gen := h.session.Gen()
	type answer struct {
		id, label string
		err       error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "ranker.list")
		if err != nil {
			return answer{err: err}
		}
		id, label := rankerInUse(asMap(res))
		return answer{id: id, label: label}
	}, func(a answer) {
		if gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			a.id, a.label = "", "" // a daemon that cannot say ranks nothing this TUI can name
		}
		if a.id != h.stageRankerID || a.label != "" {
			h.stageRanker = a.label
		}
		if a.id != "" && h.stageRanker == "" {
			h.stageRanker = a.id
		}
		h.stageRankerID = a.id
		h.syncStages(!refresh)
		if refresh {
			h.refreshSearch()
		}
	})
}

// rankerInUse is ranker.list's ranker in use: who it is ("" for none), and its model's short name
// when the list knows it.
func rankerInUse(m map[string]any) (id, model string) {
	if s := str(m, "supplied"); s != "" {
		return s, shortModel(s)
	}
	active := str(m, "active")
	if active == "" {
		return "", ""
	}
	for _, x := range asList(m["rankers"]) {
		if r := asMap(x); str(r, "name") == active && str(r, "model") != "" {
			return active, shortModel(str(r, "model"))
		}
	}
	return active, ""
}

// rankedBy names the Rerank box by the model an answer says ranked its hits.
func (h *Host) rankedBy(model string) {
	if model = shortModel(model); model != "" && model != h.stageRanker && h.stageRankerID != "" {
		h.stageRanker = model
		h.syncStages(false)
	}
}

func strs(l []any) []string {
	out := make([]string, len(l))
	for i, v := range l {
		out[i], _ = v.(string)
	}
	return out
}
