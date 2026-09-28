package index

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/errs"
)

func (e *env) search(q string, opts QueryOpts) Result {
	e.t.Helper()
	res, err := e.store.Search(context.Background(), q, opts)
	if err != nil {
		e.t.Fatalf("search %q: %v", q, err)
	}
	return res
}

// paths lists the hits' paths, in order.
func paths(r Result) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.Path)
	}
	return out
}

func TestSearchRanksTitleOverBody(t *testing.T) {
	e := newEnv(t, Options{})
	// body.md says it three times in a short body; title.md once, in its title (and so its breadcrumb)
	e.put("body.md", "# Notes\n\nkestrel kestrel kestrel\n", "title.md", "# Kestrel\n\nunrelated words that make the chunk longer than the other\n")
	res := e.search("kestrel", QueryOpts{})
	eq(t, "ranking", paths(res), []string{"title.md", "body.md"})
	if res.ModeUsed != ModeLexical || res.Semantic != SemanticOff {
		t.Errorf("mode %q, semantic %q; want lexical, off", res.ModeUsed, res.Semantic)
	}
	h := res.Hits[1]
	if !strings.Contains(h.Snippet, HighlightStart+"kestrel"+HighlightEnd+" ") || h.Breadcrumb != "Notes" || h.Generation != 1 ||
		!reflect.DeepEqual(h.Via, []string{ModeLexical}) {
		t.Errorf("hit %+v", h)
	}
}

// TestSearchQuotesOperators: FTS5's syntax in a query is words, never syntax, and never an error.
// Each word is a phrase of the tokens it holds, so "title:alpha" is the phrase "title alpha": as a
// column filter it would match no note (no title is alpha).
func TestSearchQuotesOperators(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a.md", "alpha beta\n", "b.md", "alpha or beta, near alpha, title alpha\n", "c.md", "gamma\n")
	for q, want := range map[string][]string{
		"alpha OR gamma":  nil,              // OR is a word: no note has alpha, or and gamma
		"alpha or beta":   {"b.md"},         // the word "or"
		"alpha AND":       nil,              // no note has "and"
		"NOT gamma":       nil,              // no note has "not"
		"title:alpha":     {"b.md"},         // the phrase "title alpha", not a column filter
		"NEAR(alpha beta": {"b.md"},         // the phrase "near alpha", and beta; unquoted, a syntax error
		`"alpha`:          {"a.md", "b.md"}, // a stray quote
		"-alpha ^beta":    {"a.md", "b.md"}, // no exclusion, no initial-token anchor
		"alpha (beta)":    {"a.md", "b.md"}, // no grouping
		"* ** ()":         nil,              // only punctuation: no query
		"":                nil,
	} {
		res, err := e.store.Search(context.Background(), q, QueryOpts{})
		if err != nil {
			t.Errorf("%q: %v", q, err)
			continue
		}
		got := paths(res)
		if len(got) > 0 || len(want) > 0 {
			eq(t, fmt.Sprintf("%q", q), sortedCopy(got), want)
		}
	}
}

func sortedCopy(ss []string) []string {
	out := append([]string(nil), ss...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// TestSearchPrefixOnTheLastWord: a '*' ending the last word is a prefix; anywhere else it is
// punctuation. (The tokenizer stems, so the words here are ones stemming leaves whole.)
func TestSearchPrefixOnTheLastWord(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a.md", "zebrafish meeting\n", "b.md", "a zebra\n")
	eq(t, "zebra*", sortedCopy(paths(e.search("zebra*", QueryOpts{}))), []string{"a.md", "b.md"})
	eq(t, "zebra", paths(e.search("zebra", QueryOpts{})), []string{"b.md"})
	eq(t, "meeting zebraf*", paths(e.search("meeting zebraf*", QueryOpts{})), []string{"a.md"})
	if got := paths(e.search("zebraf* meeting", QueryOpts{})); len(got) != 0 {
		t.Errorf("zebraf* meeting matched %v: an earlier * was a prefix", got)
	}
}

// TestSearchCollapsesPerDocument: at most three hits come from one document, then the limit.
func TestSearchCollapsesPerDocument(t *testing.T) {
	e := newEnv(t, Options{})
	var b strings.Builder
	for i := range 6 {
		fmt.Fprintf(&b, "# S%d\n\nosprey number %d\n\n", i, i)
	}
	e.put("many.md", b.String(), "one.md", "# One\n\nosprey\n")
	res := e.search("osprey", QueryOpts{})
	n := 0
	for _, h := range res.Hits {
		if h.Path == "many.md" {
			n++
		}
	}
	if n != 3 || len(res.Hits) != 4 {
		t.Errorf("%d hits from many.md, %d in all; want 3 and 4: %v", n, len(res.Hits), paths(res))
	}
	if got := e.search("osprey", QueryOpts{Limit: 2}); len(got.Hits) != 2 {
		t.Errorf("limit 2 gave %d hits", len(got.Hits))
	}
}

// TestSearchBoostsAfterFusion: of two notes with the same text, the one linked to ranks first, and
// a query word equal to a tag lifts its note; each factor is exactly the ADR's.
func TestSearchBoostsAfterFusion(t *testing.T) {
	e := newEnv(t, Options{})
	// a.md links to itself, which is no in-link, from a section of its own: the heron chunks match
	e.put("a.md", "# Same\n\nheron text\n\n## See\n\n[[a]]\n", "b.md", "# Same\n\nheron text\n\n## See\n\nnothing\n")
	base := e.search("heron", QueryOpts{})
	eq(t, "equal text: by path", paths(base), []string{"a.md", "b.md"})
	// RRF over one retriever, unboosted: 1/(60 + rank), ranks from 1
	if base.Hits[0].Score != 1.0/61 || base.Hits[1].Score != 1.0/62 {
		t.Errorf("base scores %v, %v; want 1/61, 1/62", base.Hits[0].Score, base.Hits[1].Score)
	}
	e.put("x.md", "[[b]]", "y.md", "[[b]] [[b]]", "z.md", "[[b]]") // three documents, four links
	linked := e.search("heron", QueryOpts{})
	eq(t, "linked first", paths(linked), []string{"b.md", "a.md"})
	want := base.Hits[1].Score * (1 + 0.1*math.Log(4))
	if got := linked.Hits[0].Score; math.Abs(got-want) > 1e-12 {
		t.Errorf("linked score %v, want %v (three linking documents)", got, want)
	}
	e.put("a.md", "---\ntags: [heron]\n---\n# Same\n\nheron text\n\n## See\n\n[[a]]\n")
	// the tag is on each of a.md's chunks, so its See section matches too: two hits, then b.md
	tagged := e.search("heron", QueryOpts{})
	eq(t, "tag outranks three in-links", paths(tagged), []string{"a.md", "a.md", "b.md"})
	for i, want := range []float64{1.2 / 61, 1.2 / 62, (1 + 0.1*math.Log(4)) / 63} {
		if got := tagged.Hits[i].Score; math.Abs(got-want) > 1e-12 {
			t.Errorf("hit %d score %v, want %v", i, got, want)
		}
	}
}

func TestSearchFilters(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a/x.md", "---\ntags: [red, blue]\n---\nwren\n", "a/y.md", "---\ntags: [red]\n---\nwren\n",
		"ab/z.md", "---\ntags: [red, blue]\n---\nwren\n", "b.md", "wren #Blue\n")
	for _, c := range []struct {
		opts QueryOpts
		want []string
	}{
		{QueryOpts{Tags: []string{"red"}}, []string{"a/x.md", "a/y.md", "ab/z.md"}},
		{QueryOpts{Tags: []string{"red", "#Blue"}}, []string{"a/x.md", "ab/z.md"}},
		{QueryOpts{Tags: []string{"blue"}}, []string{"a/x.md", "ab/z.md", "b.md"}},
		{QueryOpts{Paths: []string{"a"}}, []string{"a/x.md", "a/y.md"}},         // not ab/
		{QueryOpts{Paths: []string{"a/"}}, []string{"a/x.md", "a/y.md"}},        // a trailing slash
		{QueryOpts{Paths: []string{"b.md", "ab"}}, []string{"ab/z.md", "b.md"}}, // a file, a directory
		{QueryOpts{Paths: []string{"."}}, []string{"a/x.md", "a/y.md", "ab/z.md", "b.md"}},
		{QueryOpts{Paths: []string{"a"}, Tags: []string{"blue"}}, []string{"a/x.md"}},
	} {
		eq(t, fmt.Sprintf("%+v", c.opts), sortedCopy(paths(e.search("wren", c.opts))), c.want)
	}
}

func TestSearchModes(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a.md", "finch\n")
	if _, err := e.store.Search(context.Background(), "finch", QueryOpts{Mode: ModeSemantic}); !errors.Is(err, errs.ErrUnsupported) {
		t.Errorf("semantic with no provider: %v, want ErrUnsupported", err)
	}
	if _, err := e.store.Search(context.Background(), "finch", QueryOpts{Mode: "fuzzy"}); err == nil {
		t.Error("an unknown mode is accepted")
	}
	for _, m := range []string{"", ModeAuto, ModeLexical} {
		if got := paths(e.search("finch", QueryOpts{Mode: m})); len(got) != 1 {
			t.Errorf("mode %q: %v", m, got)
		}
	}
}

// TestSearchNeverFindsADeadChunk: text edited away is no hit, though its FTS row is still there.
func TestSearchNeverFindsADeadChunk(t *testing.T) {
	e := newEnv(t, Options{noGC: true})
	e.put("a.md", "# A\n\nplover\n")
	e.put("a.md", "# A\n\nsandpiper\n")
	if got := paths(e.search("plover", QueryOpts{})); len(got) != 0 {
		t.Errorf("the dead chunk was a hit: %v", got)
	}
	var entries int
	_ = scanOne(context.Background(), e.store.r, &entries, "SELECT COUNT(*) FROM chunk_fts WHERE chunk_fts MATCH 'plover'")
	if entries != 1 {
		t.Fatalf("%d FTS entries for the dead text, want 1: this test would show nothing", entries)
	}
	eq(t, "the new text", paths(e.search("sandpiper", QueryOpts{})), []string{"a.md"})
}

// TestSearchAcrossCommitsAgrees: searches running while a note flips between two texts get hits
// whose text and generation agree: odd generations hold one text, even ones the other.
func TestSearchAcrossCommitsAgrees(t *testing.T) {
	e := newEnv(t, Options{BatchDelay: 2 * time.Millisecond})
	texts := [2]string{"# T\n\ncurlew even\n", "# T\n\ncurlew odd\n"}
	e.put("a.md", texts[1]) // generation 1
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 2; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			e.write("a.md", texts[i%2])
			e.ix.Touch("a.md")
			e.indexedAt("a.md")
		}
	}()
	checked := 0
	for range 300 {
		res, err := e.store.Search(context.Background(), "curlew", QueryOpts{})
		if err != nil {
			t.Fatal(err)
		}
		for _, h := range res.Hits {
			word := "even"
			if h.Generation%2 == 1 {
				word = "odd"
			}
			if !strings.Contains(h.Snippet, word) {
				t.Fatalf("generation %d with snippet %q", h.Generation, h.Snippet)
			}
			checked++
		}
	}
	close(stop)
	wg.Wait()
	var gen int64
	_ = scanOne(context.Background(), e.store.r, &gen, "SELECT active_gen FROM document")
	t.Logf("%d hits checked across %d generations", checked, gen)
	if gen < 5 {
		t.Errorf("only %d generations: the searches did not run across commits", gen)
	}
}

// TestFuse: reciprocal rank fusion (ADR 0204 §5.5). A chunk found by both retrievers outranks one
// found by one at the same ranks; equal scores order by path, then position, every time.
func TestFuse(t *testing.T) {
	c := func(path string, doc int64, ord int, via string) candidate {
		return candidate{hit: Hit{Path: path, Via: []string{via}}, docID: doc, ord: ord}
	}
	lex := []candidate{c("b.md", 2, 0, "lexical"), c("a.md", 1, 1, "lexical"), c("a.md", 1, 0, "lexical")}
	sem := []candidate{c("a.md", 1, 1, "semantic"), c("b.md", 2, 0, "semantic"), c("c.md", 3, 0, "semantic")}
	got := fuse(lex, sem)
	sort.Slice(got, func(i, j int) bool { return got[i].before(got[j]) })
	var order []string
	for _, g := range got {
		order = append(order, fmt.Sprintf("%s#%d %v %.6f", g.hit.Path, g.ord, g.hit.Via, g.hit.Score))
	}
	both := 1.0/61 + 1.0/62 // b.md#0 and a.md#1: ranks 1 and 2 each way, the same sum
	eq(t, "fused", order, []string{
		fmt.Sprintf("a.md#1 [lexical semantic] %.6f", both), // tied with b.md#0: the path decides
		fmt.Sprintf("b.md#0 [lexical semantic] %.6f", both),
		fmt.Sprintf("a.md#0 [lexical] %.6f", 1.0/63),  // third in one list …
		fmt.Sprintf("c.md#0 [semantic] %.6f", 1.0/63), // … and third in the other: the path decides
	})
	if got[0].hit.Score != got[1].hit.Score {
		t.Errorf("the tie is not exact: %v, %v", got[0].hit.Score, got[1].hit.Score)
	}
	a0 := []candidate{c("x.md", 9, 0, "lexical")}
	if s := fuse(a0, a0)[0].hit.Score; s <= fuse(a0)[0].hit.Score {
		t.Errorf("found by both scores %v, by one %v", s, fuse(a0)[0].hit.Score)
	}
}
