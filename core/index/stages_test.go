package index

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"
)

// TestTheStagesAQueryAsksFor: stages name the retrievers and whether the ranker runs; without
// them, a mode says the same, every mode ranking as a ranker in use always did. Neither retriever,
// a name that is no stage, a stage named twice, or stages beside a mode is refused.
func TestTheStagesAQueryAsksFor(t *testing.T) {
	all := []string{StageLexical, StageSemantic, StageRerank}
	for _, c := range []struct {
		name   string
		opts   QueryOpts
		asked  []string
		mode   search.Mode
		rerank bool
	}{
		{"no stages, no mode: auto", QueryOpts{}, all, search.ModeAuto, true},
		{"mode auto", QueryOpts{Mode: ModeAuto}, all, search.ModeAuto, true},
		{"mode lexical", QueryOpts{Mode: ModeLexical}, []string{StageLexical, StageRerank}, search.ModeLexical, true},
		{"mode semantic", QueryOpts{Mode: ModeSemantic}, []string{StageSemantic, StageRerank}, search.ModeSemantic, true},
		{"every stage, in any order", QueryOpts{Stages: []string{StageRerank, StageSemantic, StageLexical}}, all, search.ModeAuto, true},
		{"both retrievers", QueryOpts{Stages: []string{StageSemantic, StageLexical}}, []string{StageLexical, StageSemantic}, search.ModeAuto, false},
		{"words", QueryOpts{Stages: []string{StageLexical}}, []string{StageLexical}, search.ModeLexical, false},
		{"words, ranked", QueryOpts{Stages: []string{StageLexical, StageRerank}}, []string{StageLexical, StageRerank}, search.ModeLexical, true},
		{"meaning", QueryOpts{Stages: []string{StageSemantic}}, []string{StageSemantic}, search.ModeSemantic, false},
		{"meaning, ranked", QueryOpts{Stages: []string{StageSemantic, StageRerank}}, []string{StageSemantic, StageRerank}, search.ModeSemantic, true},
	} {
		p, err := planOf(c.opts)
		if err != nil || !reflect.DeepEqual(p.requested, c.asked) || p.mode != c.mode || p.rerank != c.rerank {
			t.Errorf("%s: %+v, %v; want %v in %s, rerank %v", c.name, p, err, c.asked, c.mode, c.rerank)
		}
	}
	for _, c := range []struct {
		name string
		opts QueryOpts
		want error
	}{
		{"no stage", QueryOpts{Stages: []string{}}, ErrNoRetriever},
		{"the ranker alone", QueryOpts{Stages: []string{StageRerank}}, ErrNoRetriever},
		{"a name that is no stage", QueryOpts{Stages: []string{StageLexical, "fuzzy"}}, ErrUnknownStage},
		{"a stage twice", QueryOpts{Stages: []string{StageLexical, StageLexical}}, ErrRepeatedStage},
		{"stages and a mode", QueryOpts{Stages: []string{StageLexical}, Mode: ModeLexical}, ErrStagesAndMode},
		{"a mode that is none", QueryOpts{Mode: "fuzzy"}, ErrUnknownMode},
	} {
		if _, err := planOf(c.opts); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
	}
}

// counter is a ranker that counts its calls, scoring the longer text higher.
type counter struct{ scorer }

func newCounter() *counter {
	return &counter{scorer{score: func(x string) float64 { return float64(len(x)) }}}
}

func (c *counter) calls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.asked)
}

// stagedEnv is an index with an embedding model and a ranker in use, two files embedded.
func stagedEnv(t *testing.T, r Rank) (*env, *fakeProvider, *counter, *rank.Holder) {
	t.Helper()
	p := newFake("m", "a")
	c := newCounter()
	h := &rank.Holder{}
	h.Set(c, rank.DefaultWindow)
	r.Source = h
	e := newEnv(t, Options{Provider: p, Rank: r})
	e.put("short.md", "# Short\n\nkestrel\n", "long.md", "# Long\n\nkestrel among many other words in a longer chunk of text\n")
	e.ready()
	return e, p, c, h
}

func stagesOf(stages ...string) QueryOpts { return QueryOpts{Stages: stages} }

// TestEachCombinationOfStages: what each combination runs, and says it ran. The ranker is asked
// only when rerank is: without it the hits are the retrievers' order, and the ranker is never
// called, though one is in use.
func TestEachCombinationOfStages(t *testing.T) {
	e, _, c, _ := stagedEnv(t, Rank{})
	for _, k := range []struct {
		name      string
		opts      QueryOpts
		used      string
		performed []string
		ranked    bool
	}{
		{"auto", QueryOpts{}, ModeHybrid, []string{StageLexical, StageSemantic, StageRerank}, true},
		{"mode lexical", QueryOpts{Mode: ModeLexical}, ModeLexical, []string{StageLexical, StageRerank}, true},
		{"mode semantic", QueryOpts{Mode: ModeSemantic}, ModeSemantic, []string{StageSemantic, StageRerank}, true},
		{"lexical+semantic", stagesOf(StageLexical, StageSemantic), ModeHybrid, []string{StageLexical, StageSemantic}, false},
		{"lexical", stagesOf(StageLexical), ModeLexical, []string{StageLexical}, false},
		{"semantic", stagesOf(StageSemantic), ModeSemantic, []string{StageSemantic}, false},
		{"lexical+rerank", stagesOf(StageLexical, StageRerank), ModeLexical, []string{StageLexical, StageRerank}, true},
		{"semantic+rerank", stagesOf(StageSemantic, StageRerank), ModeSemantic, []string{StageSemantic, StageRerank}, true},
		{"every stage", stagesOf(StageLexical, StageSemantic, StageRerank), ModeHybrid, []string{StageLexical, StageSemantic, StageRerank}, true},
	} {
		before := c.calls()
		res := e.query("kestrel", k.opts)
		calls := c.calls() - before
		if res.ModeUsed != k.used || !reflect.DeepEqual(res.Stages.Performed, k.performed) || len(res.Stages.Skipped) != 0 {
			t.Errorf("%s: used %s, stages %+v; want %s, %v performed, none skipped", k.name, res.ModeUsed, res.Stages, k.used, k.performed)
		}
		switch {
		case k.ranked && (calls != 1 || res.Rank.State != "ready" || res.Hits[0].Path != "long.md"):
			t.Errorf("%s: %d ranker calls, rank %+v, first %s; want one call, ready, long.md first", k.name, calls, res.Rank, res.Hits[0].Path)
		case !k.ranked && (calls != 0 || res.Rank.State != "" || res.Hits[0].RankScore != nil):
			t.Errorf("%s: %d ranker calls, rank %+v; want the ranker never asked", k.name, calls, res.Rank)
		}
	}
}

// TestSemanticAloneNeverFallsBackToWords: asked for meaning alone, a search that meaning cannot
// answer fails; asked for both, it answers by words and says why meaning was skipped.
func TestSemanticAloneNeverFallsBackToWords(t *testing.T) {
	e, p, _, _ := stagedEnv(t, Rank{})
	p.fail("kestrel")
	if res, err := e.ix.Search(context.Background(), "kestrel", stagesOf(StageSemantic)); !errors.Is(err, ErrEmbedFailed) {
		t.Errorf("semantic alone, the query not embedded: %v hits, %v; want ErrEmbedFailed", paths(res), err)
	}
	res := e.query("kestrel", stagesOf(StageLexical, StageSemantic))
	if res.ModeUsed != ModeLexical || len(res.Hits) != 2 || !reflect.DeepEqual(res.Stages.Performed, []string{StageLexical}) ||
		!reflect.DeepEqual(res.Stages.Skipped, map[string]string{StageSemantic: SkipEmbedFailed}) {
		t.Errorf("both, the query not embedded: %s, %d hits, %+v", res.ModeUsed, len(res.Hits), res.Stages)
	}

	none := newEnv(t, Options{})
	none.put("a.md", "kestrel\n")
	if _, err := none.ix.Search(context.Background(), "kestrel", stagesOf(StageSemantic)); !errors.Is(err, ErrNoProvider) {
		t.Errorf("semantic alone, no model: %v, want ErrNoProvider", err)
	}
	res = none.query("kestrel", QueryOpts{})
	want := Stages{Requested: []string{StageLexical, StageSemantic, StageRerank}, Performed: []string{StageLexical},
		Skipped: map[string]string{StageSemantic: SkipNoModel, StageRerank: SkipNoRanker}}
	if !reflect.DeepEqual(res.Stages, want) {
		t.Errorf("auto, no model and no ranker: %+v, want %+v", res.Stages, want)
	}
	if res, err := none.store.Search(context.Background(), "kestrel", QueryOpts{}); err != nil || !reflect.DeepEqual(res.Stages, want) {
		t.Errorf("the store alone: %+v, %v; want %+v", res.Stages, err, want)
	}
}

// TestTheSkipReasons: a ranker that fails or is not in use, and semantic search paused by the
// workspace's policy, are skipped with their reasons; a query with no words runs nothing but the
// scan of its filters.
func TestTheSkipReasons(t *testing.T) {
	e, _, c, h := stagedEnv(t, Rank{})
	c.err = rank.ErrUnreachable
	res := e.query("kestrel", QueryOpts{})
	if !reflect.DeepEqual(res.Stages.Skipped, map[string]string{StageRerank: SkipRankFailed}) || res.Rank.State != "error" {
		t.Errorf("a ranker that fails: %+v, rank %+v", res.Stages, res.Rank)
	}
	h.Set(nil, 0)
	res = e.query("kestrel", stagesOf(StageLexical, StageRerank))
	if !reflect.DeepEqual(res.Stages.Skipped, map[string]string{StageRerank: SkipNoRanker}) ||
		!reflect.DeepEqual(res.Stages.Performed, []string{StageLexical}) {
		t.Errorf("no ranker in use: %+v", res.Stages)
	}
	e.ix.SetSemanticPaused(true)
	res = e.query("kestrel", stagesOf(StageLexical, StageSemantic))
	if !reflect.DeepEqual(res.Stages.Skipped, map[string]string{StageSemantic: SkipPaused}) {
		t.Errorf("semantic search paused: %+v", res.Stages)
	}
	e.ix.SetSemanticPaused(false)

	e.put("tagged.md", "---\ntags: [birds]\n---\n# Tagged\n\nheron\n")
	listed := e.query("", QueryOpts{Tags: []string{"birds"}})
	if listed.ModeUsed != ModeFacet || len(listed.Hits) != 1 || !reflect.DeepEqual(listed.Stages, Stages{
		Requested: []string{StageLexical, StageSemantic, StageRerank}, Performed: []string{StageLexical},
		Skipped: map[string]string{StageSemantic: SkipNoWords, StageRerank: SkipNoWords}}) {
		t.Errorf("filters alone: %s, %d hits, %+v", listed.ModeUsed, len(listed.Hits), listed.Stages)
	}
	empty := e.query("  ", stagesOf(StageLexical))
	if !reflect.DeepEqual(empty.Stages, Stages{Requested: []string{StageLexical}, Performed: []string{},
		Skipped: map[string]string{StageLexical: SkipNoWords}}) {
		t.Errorf("no words and no filters: %+v", empty.Stages)
	}

	// a model switch answers by words: the reason is the switch's
	switching := plan{requested: []string{StageLexical, StageSemantic}, mode: search.ModeAuto}.
		report(Result{ModeUsed: ModeLexical, Semantic: SemanticSwitching}, false, false)
	if !reflect.DeepEqual(switching.Skipped, map[string]string{StageSemantic: SkipSwitching}) {
		t.Errorf("a model switching: %+v", switching)
	}
}

// TestRequiredHonoursASearchWithoutTheRanker: a build that answers ranked or nothing still answers
// a search that does not ask for the ranker, unranked and without calling it; one that asks for it,
// or auto, is refused when the ranker cannot rank.
func TestRequiredHonoursASearchWithoutTheRanker(t *testing.T) {
	e, _, c, _ := stagedEnv(t, Rank{Required: true})
	c.err = rank.ErrUnreachable
	before := c.calls()
	res, err := e.ix.Search(context.Background(), "kestrel", stagesOf(StageLexical))
	if err != nil || len(res.Hits) != 2 || res.Rank.State != "" || c.calls() != before {
		t.Errorf("required, without rerank: %d hits, rank %+v, %d calls, %v; want two unranked hits, no call", len(res.Hits), res.Rank, c.calls()-before, err)
	}
	for _, opts := range []QueryOpts{{}, stagesOf(StageLexical, StageRerank)} {
		if _, err := e.ix.Search(context.Background(), "kestrel", opts); !errors.Is(err, rank.ErrUnavailable) {
			t.Errorf("required, %v, the ranker failing: %v, want ErrUnavailable", opts.Stages, err)
		}
	}
}
