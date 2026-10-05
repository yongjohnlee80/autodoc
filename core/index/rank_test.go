package index

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"
)

// scorer is a ranker scoring each text by score, recording the texts it was asked.
type scorer struct {
	score func(text string) float64
	err   error
	mu    sync.Mutex
	asked [][]string
}

func (s *scorer) Rank(_ context.Context, _ string, texts []string) ([]float64, error) {
	s.mu.Lock()
	s.asked = append(s.asked, slices.Clone(texts))
	s.mu.Unlock()
	if s.err != nil {
		return nil, s.err
	}
	out := make([]float64, len(texts))
	for i, x := range texts {
		out[i] = s.score(x)
	}
	return out, nil
}

func (*scorer) Model() rank.Model { return rank.Model{Provider: "test", Name: "scorer", MaxBatch: 100} }

func (s *scorer) texts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var all []string
	for _, a := range s.asked {
		all = append(all, a...)
	}
	return all
}

// rankedEnv is an index whose searches go through the stage, the ranker held in h.
func rankedEnv(t *testing.T, r Rank, ranker rank.Ranker) (*env, *rank.Holder) {
	t.Helper()
	h := &rank.Holder{}
	if ranker != nil {
		h.Set(ranker, rank.DefaultWindow)
	}
	r.Source = h
	e := newEnv(t, Options{Rank: r})
	// lexically, short.md's one word in a short chunk outranks long.md's
	e.put("short.md", "# Short\n\nkestrel\n", "long.md", "# Long\n\nkestrel among many other words in a longer chunk of text\n")
	return e, h
}

func (e *env) ixSearch(q string) (Result, error) {
	return e.ix.Search(context.Background(), q, QueryOpts{})
}

// TestTheRankerOrdersTheHitsByTheirChunks: the ranker reads each hit's chunk, its breadcrumb then
// its body, and the hits come back in its order, scored, found "rank" too, and the result says by
// which model.
func TestTheRankerOrdersTheHitsByTheirChunks(t *testing.T) {
	sc := &scorer{score: func(x string) float64 { return float64(len(x)) }} // the longer chunk first
	e, _ := rankedEnv(t, Rank{}, sc)
	recall, err := e.store.Search(context.Background(), "kestrel", QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "recall order", paths(recall), []string{"short.md", "long.md"})
	res, err := e.ixSearch("kestrel")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "ranked order", paths(res), []string{"long.md", "short.md"})
	if res.Rank != (RankState{State: "ready", Model: "scorer"}) {
		t.Errorf("rank %+v", res.Rank)
	}
	for _, h := range res.Hits {
		if h.RankScore == nil || !slices.Contains(h.Via, "rank") {
			t.Errorf("hit %s: score %v, via %v", h.Path, h.RankScore, h.Via)
		}
	}
	got := sc.texts()
	slices.Sort(got)
	eq(t, "texts read", got, []string{"Long\nkestrel among many other words in a longer chunk of text", "Short\nkestrel"})
}

// TestAStaleHitIsNotRanked: a hit whose document is gone, at another generation, or whose bytes are
// no chunk alive at its generation, is rank.ErrStale: never another chunk's text.
func TestAStaleHitIsNotRanked(t *testing.T) {
	e, _ := rankedEnv(t, Rank{}, nil)
	res, err := e.store.Search(context.Background(), "kestrel", QueryOpts{})
	if err != nil || len(res.Hits) == 0 {
		t.Fatal(res, err)
	}
	h := res.Hits[0]
	hit := search.Hit{Path: h.Path, Generation: h.Generation, ByteStart: h.ByteStart, ByteEnd: h.ByteEnd}
	if texts, err := e.store.chunkTexts(context.Background(), []search.Hit{hit}); err != nil || len(texts) != 1 {
		t.Fatalf("a live hit: %q, %v", texts, err)
	}
	for what, mutate := range map[string]func(*search.Hit){
		"gone":             func(x *search.Hit) { x.Path = "missing.md" },
		"older generation": func(x *search.Hit) { x.Generation-- },
		"newer generation": func(x *search.Hit) { x.Generation++ },
		"other bytes":      func(x *search.Hit) { x.ByteEnd++ },
	} {
		x := hit
		mutate(&x)
		if _, err := e.store.chunkTexts(context.Background(), []search.Hit{hit, x}); !errors.Is(err, rank.ErrStale) {
			t.Errorf("%s: %v, want ErrStale", what, err)
		}
	}
	// a document rewritten after the search found it, its chunk at the same bytes: its generation
	// moved on, and the chunk found then is dead now; the chunk at those bytes now is the one read
	e.put("short.md", "# Short\n\nKestrel\n")
	old := hitAt(t, e, "short.md")
	old.Generation--
	if _, err := e.store.chunkTexts(context.Background(), []search.Hit{old}); !errors.Is(err, rank.ErrStale) {
		t.Errorf("a rewritten document: %v, want ErrStale", err)
	}
	now := hitAt(t, e, "short.md")
	if now.ByteStart != old.ByteStart || now.ByteEnd != old.ByteEnd {
		t.Fatalf("the rewrite moved the chunk: %d-%d, was %d-%d", now.ByteStart, now.ByteEnd, old.ByteStart, old.ByteEnd)
	}
	if texts, err := e.store.chunkTexts(context.Background(), []search.Hit{now}); err != nil || !slices.Equal(texts, []string{"Short\nKestrel"}) {
		t.Errorf("the chunk now at those bytes: %q, %v", texts, err)
	}
	// two live chunks at one range is no one chunk's text
	e.exec(`INSERT INTO chunk (workspace_id, doc_id, hash, text_hash, gen_from, gen_to, ord, breadcrumb, body, title, tags, byte_start, byte_end)
		SELECT workspace_id, doc_id, hash, text_hash, gen_from, gen_to, ord + 1, breadcrumb, 'another', title, tags, byte_start, byte_end
		FROM chunk WHERE body = 'Kestrel' AND gen_to IS NULL`)
	if _, err := e.store.chunkTexts(context.Background(), []search.Hit{now}); !errors.Is(err, rank.ErrStale) {
		t.Errorf("two live chunks at the hit's bytes: %v, want ErrStale", err)
	}
}

// hitAt is path's hit for kestrel, by recall.
func hitAt(t *testing.T, e *env, path string) search.Hit {
	t.Helper()
	res, err := e.store.Search(context.Background(), "kestrel", QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range res.Hits {
		if h.Path == path {
			return search.Hit{Path: h.Path, Generation: h.Generation, ByteStart: h.ByteStart, ByteEnd: h.ByteEnd}
		}
	}
	t.Fatalf("no hit for %s", path)
	return search.Hit{}
}

// TestABuildsTextsReachTheRanker: a build's texts are what the ranker reads, exactly; when they
// fail the hits are in recall order, unscored, the state error.
func TestABuildsTextsReachTheRanker(t *testing.T) {
	var fail error
	texts := func(_ context.Context, hits []search.Hit) ([]string, error) {
		if fail != nil {
			return nil, fail
		}
		out := make([]string, len(hits))
		for i, h := range hits {
			out[i] = "T:" + h.Path
		}
		return out, nil
	}
	sc := &scorer{score: func(x string) float64 { return float64(len(x)) }}
	e, _ := rankedEnv(t, Rank{Texts: texts}, sc)
	res, err := e.ixSearch("kestrel")
	if err != nil {
		t.Fatal(err)
	}
	got := sc.texts()
	slices.Sort(got)
	eq(t, "texts read", got, []string{"T:long.md", "T:short.md"})
	eq(t, "ranked order", paths(res), []string{"short.md", "long.md"}) // "T:short.md" is the longer
	fail = errors.New("labels: the catalogue is down")
	res, err = e.ixSearch("kestrel")
	if err != nil {
		t.Fatal(err)
	}
	eq(t, "recall order", paths(res), []string{"short.md", "long.md"})
	if res.Rank.State != "error" || res.Hits[0].RankScore != nil {
		t.Errorf("rank %+v, score %v", res.Rank, res.Hits[0].RankScore)
	}
}

// TestRequiredRefusesWhatItCannotRank: ranked or nothing: no ranker in use, a ranker that fails, or
// texts that fail, and a worded search is refused; a search the stage passes (no words) is
// answered. Without Required, no ranker in use is the state off.
func TestRequiredRefusesWhatItCannotRank(t *testing.T) {
	sc := &scorer{score: func(string) float64 { return 0 }}
	e, h := rankedEnv(t, Rank{Required: true}, sc)
	if res, err := e.ixSearch("kestrel"); err != nil || res.Rank.State != "ready" {
		t.Fatalf("ranked: %+v, %v", res.Rank, err)
	}
	sc.err = rank.ErrUnreachable
	if _, err := e.ixSearch("kestrel"); !errors.Is(err, rank.ErrUnavailable) {
		t.Errorf("a ranker that fails: %v, want ErrUnavailable", err)
	}
	h.Set(nil, 0)
	if _, err := e.ixSearch("kestrel"); !errors.Is(err, rank.ErrUnavailable) {
		t.Errorf("no ranker in use: %v, want ErrUnavailable", err)
	}

	e2, _ := rankedEnv(t, Rank{}, nil)
	if res, err := e2.ixSearch("kestrel"); err != nil || res.Rank.State != "off" || len(res.Hits) != 2 {
		t.Errorf("no ranker in use, not required: %+v, %d hits, %v", res.Rank, len(res.Hits), err)
	}
}
