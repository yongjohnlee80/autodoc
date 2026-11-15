//go:build server

package index

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// The index on a store on PostgreSQL: this test binary links the index's declarations, which a
// store's own cells never do, so opening the store here is what proves the declarations render on
// the engine (they once panicked at the open). Search there matches the tsvector column with
// tsquery, ranks with ts_rank_cd by class, and marks its snippet with ts_headline: the same hits
// and marks as SQLite's, scores the engine's own, and no ellipsis at a cut.

// newEnvOnPostgres is newEnv on a scratch PostgreSQL database (skipped without one).
func newEnvOnPostgres(t testing.TB, opts options) *env {
	t.Helper()
	e := &env{t: t, fsys: memfs.New(), dir: t.TempDir()}
	e.loc = &store.Location{Engine: store.EngineOfPostgres, DSN: pgtest.ScratchURL(t), Schema: "autodoc_store",
		KeyFile: filepath.Join(e.dir, "store.key")}
	e.fault = &faultFS{FS: e.fsys, fail: map[string]error{}, opens: map[string]int{}}
	e.open(opts)
	return e
}

func TestSearchOnPostgresRanksTitleOverBody(t *testing.T) {
	e := newEnvOnPostgres(t, options{})
	e.put("body.md", "# Notes\n\nkestrel kestrel kestrel\n", "title.md", "# Kestrel\n\nunrelated words that make the chunk longer than the other\n")
	res := e.search("kestrel", QueryOpts{})
	eq(t, "ranking", hitPaths(res), []string{"title.md", "body.md"})
	if res.ModeUsed != ModeLexical {
		t.Errorf("mode %q, want lexical", res.ModeUsed)
	}
	h := res.Hits[1]
	if !strings.Contains(h.Snippet, HighlightStart+"kestrel"+HighlightEnd) || h.Breadcrumb != "Notes" || h.Generation != 1 ||
		!reflect.DeepEqual(h.Via, []string{ModeLexical}) {
		t.Errorf("hit %+v", h)
	}
}

// A query's text is words on PostgreSQL too, never tsquery syntax, and never an error: each case
// finds what its words find. Stemming is autodoc_english's, so "runs" finds "running".
func TestSearchOnPostgresQuotesOperators(t *testing.T) {
	e := newEnvOnPostgres(t, options{})
	e.put("a.md", "alpha beta\n", "b.md", "alpha or beta, near alpha, title alpha\n", "c.md", "gamma\n", "d.md", "she was running\n")
	for q, want := range map[string][]string{
		"alpha | gamma":  {"a.md", "b.md", "c.md"}, // no file holds all three words: any one is enough
		"alpha & !beta":  {"a.md", "b.md"},         // & and ! are no operators: alpha and beta
		"title:alpha":    {"b.md"},
		"alpha <-> beta": {"a.md", "b.md"},
		"'alpha":         {"a.md", "b.md"},
		"runs":           {"d.md"},
		"* ** ()":        nil,
		"":               nil,
	} {
		res, err := e.store.Search(context.Background(), q, QueryOpts{})
		if err != nil {
			t.Errorf("%q: %v", q, err)
			continue
		}
		if got := hitPaths(res); len(got) > 0 || len(want) > 0 {
			eq(t, q, sortedCopy(got), want)
		}
	}
}

// The snippet binds the query the match does: it marks the stemmed term in a long body, in one
// fragment of at most 16 words with no ellipsis; the filters still apply before the limit.
func TestSearchOnPostgresMarksTheSnippet(t *testing.T) {
	e := newEnvOnPostgres(t, options{})
	long := strings.Repeat("filler words go here and there ", 10)
	e.put("notes/long.md", "# Long\n\n"+long+"the kestrel was hovering over the field "+long+"\n", "other/k.md", "# K\n\nkestrel\n")
	res := e.search("kestrels hover", QueryOpts{Paths: []string{"notes"}})
	if got := hitPaths(res); !reflect.DeepEqual(got, []string{"notes/long.md"}) {
		t.Fatalf("hits %v, want notes/long.md alone", got)
	}
	s := res.Hits[0].Snippet
	if !strings.Contains(s, HighlightStart+"kestrel"+HighlightEnd) || !strings.Contains(s, HighlightStart+"hovering"+HighlightEnd) {
		t.Errorf("the snippet %q: want kestrel and hovering marked", s)
	}
	if n := len(strings.Fields(s)); n > 16 || strings.Contains(s, "…") {
		t.Errorf("the snippet %q: %d words, want at most 16 and no ellipsis", s, n)
	}
}
