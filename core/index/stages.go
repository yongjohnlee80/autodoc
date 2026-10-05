package index

import (
	"fmt"
	"slices"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/query"
)

// The search's stages (QueryOpts.Stages, Result.Stages): the two retrievers, by words and by
// meaning, and the ranker that re-orders what they found.
const (
	StageLexical  = "lexical"
	StageSemantic = "semantic"
	StageRerank   = "rerank"
)

// allStages are the stages in the order a Stages lists them; auto asks for every one.
var allStages = []string{StageLexical, StageSemantic, StageRerank}

// The refusals of QueryOpts.Stages.
var (
	// ErrNoRetriever is stages with neither retriever: empty, or rerank alone. A ranker only
	// re-orders what a retriever found.
	ErrNoRetriever = errs.Sentinel(errs.ErrInvalidArgument, "index: search stages must include lexical or semantic")
	// ErrUnknownStage is a stage that is none of StageLexical, StageSemantic and StageRerank.
	ErrUnknownStage = errs.Sentinel(errs.ErrInvalidArgument, "index: unknown search stage")
	// ErrRepeatedStage is a stage named twice.
	ErrRepeatedStage = errs.Sentinel(errs.ErrInvalidArgument, "index: a search stage is named twice")
	// ErrStagesAndMode is stages and a mode in one query: one says what the other does.
	ErrStagesAndMode = errs.Sentinel(errs.ErrInvalidArgument, "index: search stages and a search mode together")
)

// Why a stage that was asked for did not run (Stages.Skipped): constant, and read by people.
const (
	SkipNoWords     = "the query has no words"
	SkipNoModel     = "no embedding model is in use"
	SkipPaused      = "the workspace's embedding policy pauses semantic search"
	SkipSwitching   = "a new embedding model is filling"
	SkipEmbedFailed = "the query could not be embedded"
	SkipNoRanker    = "no ranker is in use"
	SkipRankFailed  = "the ranker could not rank this search"
)

// Stages is how a search's answer was made: the stages asked for (auto: every one), those that
// shaped the hits, and why each other one asked for did not run.
type Stages struct {
	Requested, Performed []string
	Skipped              map[string]string
}

// plan is a query's stages as the engine runs them: the mode its retrievers search in, and whether
// the ranker re-orders their hits.
type plan struct {
	requested []string
	mode      search.Mode
	rerank    bool
}

// planOf reads the stages of opts, or, without them, its mode: auto is every stage, and lexical and
// semantic each rank what they find, as a ranker in use always did.
func planOf(opts QueryOpts) (plan, error) {
	if opts.Stages == nil {
		switch opts.Mode {
		case "", ModeAuto:
			return plan{requested: allStages, mode: search.ModeAuto, rerank: true}, nil
		case ModeLexical:
			return plan{requested: []string{StageLexical, StageRerank}, mode: search.ModeLexical, rerank: true}, nil
		case ModeSemantic:
			return plan{requested: []string{StageSemantic, StageRerank}, mode: search.ModeSemantic, rerank: true}, nil
		}
		return plan{}, fmt.Errorf("%w: %q", ErrUnknownMode, opts.Mode)
	}
	if opts.Mode != "" {
		return plan{}, ErrStagesAndMode
	}
	asked := map[string]bool{}
	for _, s := range opts.Stages {
		if !slices.Contains(allStages, s) {
			return plan{}, ErrUnknownStage
		}
		if asked[s] {
			return plan{}, ErrRepeatedStage
		}
		asked[s] = true
	}
	p := plan{rerank: asked[StageRerank]}
	for _, s := range allStages {
		if asked[s] {
			p.requested = append(p.requested, s)
		}
	}
	switch {
	case asked[StageLexical] && asked[StageSemantic]:
		p.mode = search.ModeAuto // by meaning too when it can answer, else by words
	case asked[StageLexical]:
		p.mode = search.ModeLexical
	case asked[StageSemantic]:
		p.mode = search.ModeSemantic // fails when meaning cannot answer: never by words instead
	default:
		return plan{}, ErrNoRetriever
	}
	return p, nil
}

func (p plan) asked(stage string) bool { return slices.Contains(p.requested, stage) }

// report is how res was made. A query with no words ran no stage but, with filters, the scan that
// listed their files, which is the lexical one's. paused is semantic search held off by the
// workspace's embedding policy.
func (p plan) report(res Result, wordless, paused bool) Stages {
	st := Stages{Requested: p.requested, Performed: []string{}, Skipped: map[string]string{}}
	skip := func(stage, why string) {
		if p.asked(stage) {
			st.Skipped[stage] = why
		}
	}
	if wordless {
		if res.ModeUsed == ModeFacet {
			st.Performed = append(st.Performed, StageLexical)
		}
		for _, s := range p.requested {
			if !slices.Contains(st.Performed, s) {
				st.Skipped[s] = SkipNoWords
			}
		}
		return st
	}
	if p.asked(StageLexical) {
		st.Performed = append(st.Performed, StageLexical)
	}
	switch {
	case res.ModeUsed == ModeHybrid || res.ModeUsed == ModeSemantic:
		st.Performed = append(st.Performed, StageSemantic)
	case paused:
		skip(StageSemantic, SkipPaused)
	case res.Semantic == SemanticSwitching:
		skip(StageSemantic, SkipSwitching)
	case res.Semantic == SemanticError:
		skip(StageSemantic, SkipEmbedFailed)
	default:
		skip(StageSemantic, SkipNoModel)
	}
	switch res.Rank.State {
	case string(search.RankReady):
		st.Performed = append(st.Performed, StageRerank)
	case string(search.RankError):
		skip(StageRerank, SkipRankFailed)
	default:
		skip(StageRerank, SkipNoRanker)
	}
	return st
}

// wordless reports whether q has no words to search by once its field filters are taken out: then
// nothing is embedded or ranked, and filters alone list their files.
func wordless(q string, opts QueryOpts, fields search.Fields) bool {
	text, _, err := query.Facets(q, opts.Facets, fields)
	if err != nil {
		return false
	}
	terms, _ := query.Terms(text)
	return len(terms) == 0
}

// searchers are one engine's two faces: plain, its retrievers' order, and ranked, re-ordered by
// the ranker in use (nil when the indexer has no rank stage).
type searchers struct {
	plain, ranked search.Searcher
}

// of is the face a query's plan runs: ranked only when it asks for the ranker.
func (s searchers) of(rerank bool) search.Searcher {
	if rerank && s.ranked != nil {
		return s.ranked
	}
	return s.plain
}
