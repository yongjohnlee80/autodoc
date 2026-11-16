//go:build server

package index

import (
	"context"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao/postgres"
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

// The workspace scope on PostgreSQL: two workspaces of one store, a file of the same name holding
// the same word in each. A search in either finds its own chunk alone. SQLite's FTS table carries
// the workspace column; on PostgreSQL the scope rests on the workspace filter in the same WHERE as
// the match, which this cell holds.
func TestSearchOnPostgresKeepsToItsWorkspace(t *testing.T) {
	e := newEnvOnPostgres(t, options{})
	ctx := context.Background()
	w, err := e.db.AddWorkspace(ctx, "other", "/other", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	fsB := memfs.New()
	other := NewStore(e.db, w.ID)
	ixB := newIndexer(other, fsB, options{BatchDelay: 20 * time.Millisecond, Match: testMatch})
	runB, stopB := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- ixB.Run(runB) }()
	t.Cleanup(func() { stopB(); <-done }) // before the env's own cleanup closes the store

	e.put("same.md", "# Alpha\n\nkestrel in the first workspace\n")
	if _, err := fsB.WriteFile(ctx, "same.md", strings.NewReader("# Beta\n\nkestrel in the second workspace\n")); err != nil {
		t.Fatal(err)
	}
	ixB.Touch("same.md")
	deadline := time.Now().Add(20 * time.Second)
	for {
		_, ok, err := other.Version(ctx, "same.md")
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the second workspace's file was never indexed")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if res := e.search("kestrel", QueryOpts{}); len(res.Hits) != 1 || res.Hits[0].Breadcrumb != "Alpha" {
		t.Errorf("the first workspace's search: %+v, want its own chunk alone (Alpha)", res.Hits)
	}
	res, err := other.Search(ctx, "kestrel", QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Hits) != 1 || res.Hits[0].Breadcrumb != "Beta" {
		t.Errorf("the second workspace's search: %+v, want its own chunk alone (Beta)", res.Hits)
	}
}

// ChunkTSV against the catalog: the generated column's expression gives each of the declaration's
// columns its class, under its configuration, compared column by column by name (a tsvector's
// weights follow the classes, not the order the expression lists the columns in). A script that
// reclassifies a column, or a declaration out of step with the scripts, would mis-weight the rank
// silently; this cell fails instead.
func TestChunkTSVIsTheCatalogs(t *testing.T) {
	e := newEnvOnPostgres(t, options{})
	ctx := context.Background()
	conn, err := postgres.Open(ctx, e.loc.DSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `SELECT pg_get_expr(d.adbin, d.adrelid) FROM pg_attrdef d
		JOIN pg_attribute a ON a.attrelid = d.adrelid AND a.attnum = d.adnum
		WHERE d.adrelid = to_regclass($1) AND a.attname = 'tsv'`, e.loc.Schema+".chunk")
	if err != nil {
		t.Fatal(err)
	}
	var expr string
	if !rows.Next() {
		_ = rows.Close()
		t.Fatalf("no generated tsv on %s.chunk", e.loc.Schema)
	}
	if err := rows.Scan(&expr); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	parts := regexp.MustCompile(`setweight\(to_tsvector\('([a-z_.]+)'::regconfig, \(?([a-z_]+)[^']*'([A-D])'`).FindAllStringSubmatch(expr, -1)
	if len(parts) != len(ChunkTSV.Columns) {
		t.Fatalf("the catalog's tsv has %d weighted columns, the declaration %d: %s", len(parts), len(ChunkTSV.Columns), expr)
	}
	class := map[string]string{}
	for _, p := range parts {
		// the store makes its configuration in its own schema; a session without that schema on its
		// search path reads it qualified
		if p[1] != ChunkTSV.Config && p[1] != e.loc.Schema+"."+ChunkTSV.Config {
			t.Errorf("the catalog parses %s with %q, the declaration with %q", p[2], p[1], ChunkTSV.Config)
		}
		class[p[2]] = p[3]
	}
	for i, col := range ChunkTSV.Columns {
		if got, want := class[col], string(ChunkTSV.Classes[i]); got != want {
			t.Errorf("column %s: class %q in the catalog, %q in the declaration (%s)", col, got, want, expr)
		}
	}
}
