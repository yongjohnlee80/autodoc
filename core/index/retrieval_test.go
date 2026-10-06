package index

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

func on() *bool  { b := true; return &b }
func off() *bool { b := false; return &b }

// setRetrieval stores the workspace's retrieval settings, revalidates, and waits until every
// Markdown document carries the identity they give.
func (e *env) setRetrieval(abstract, demote bool) {
	e.t.Helper()
	ctx := context.Background()
	if err := e.store.db.Configure(ctx, e.store.sc.ID(), store.Changes{AbstractChunk: &abstract, DemoteSuperseded: &demote}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.ix.Revalidate(ctx); err != nil {
		e.t.Fatal(err)
	}
	e.eventually("every Markdown document rebuilt", func() bool {
		var stale int
		mark := "%.r1"
		if abstract {
			mark = "%.r1.a1"
		}
		_ = scanOne(ctx, e.raw, &stale, "SELECT COUNT(*) FROM document WHERE path LIKE '%.md' AND indexer NOT LIKE ?", mark)
		return stale == 0 && e.pendingJobs() == 0
	})
}

// chunkRows lists a document's live chunks as "ord kind breadcrumb".
func (e *env) chunkRows(p string) []string {
	e.t.Helper()
	rows, err := e.raw.QueryContext(context.Background(),
		`SELECT c.ord, c.kind, c.breadcrumb FROM chunk c JOIN document d ON d.id = c.doc_id
		 WHERE d.path = ? AND c.gen_to IS NULL ORDER BY c.ord`, p)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var ord int
		var kind, crumb string
		if err := rows.Scan(&ord, &kind, &crumb); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, fmt.Sprintf("%d %s %s", ord, kind, crumb))
	}
	return out
}

const withAbstract = "---\ntitle: Storage\nabstract: Where the index keeps its rows.\n---\n# Storage\n\nThe body.\n"

// TestTheAbstractChunk: with the setting on, a document with an abstract gains one chunk, after its
// sections, of kind abstract; its sections keep their ords and rows. Off again, it goes.
func TestTheAbstractChunk(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("s.md", withAbstract, "plain.md", "# Plain\n\nno abstract\n")
	before := e.chunkRows("s.md")
	if !reflect.DeepEqual(before, []string{"0 section Storage"}) {
		t.Fatalf("before: %q", before)
	}
	e.setRetrieval(true, false)
	if got := e.chunkRows("s.md"); !reflect.DeepEqual(got, []string{"0 section Storage", "1 abstract Storage > abstract"}) {
		t.Errorf("abstract on: %q", got)
	}
	if got := e.chunkRows("plain.md"); len(got) != 1 {
		t.Errorf("a document with no abstract gained a chunk: %q", got)
	}
	e.setRetrieval(false, false)
	if got := e.chunkRows("s.md"); !reflect.DeepEqual(got, before) {
		t.Errorf("abstract off again: %q, want %q", got, before)
	}
}

// TestTheAbstractIsLeftOutInsideTheRetriever: on an index built with abstracts, a search with the
// abstract chunk off answers as an index built without them, even where the abstracts would take
// the retriever's whole top N.
func TestTheAbstractIsLeftOutInsideTheRetriever(t *testing.T) {
	build := func(abstracts bool) *env {
		e := newEnv(t, Options{})
		var pc []string
		// the abstracts say "heron" often, so each outranks every body that says it once
		for i := 0; i < retrieverTop+5; i++ {
			pc = append(pc, fmt.Sprintf("a%02d.md", i), fmt.Sprintf("---\nabstract: heron heron heron heron %d\n---\n# A%d\n\nnothing here\n", i, i))
		}
		for i := 0; i < 3; i++ {
			pc = append(pc, fmt.Sprintf("b%d.md", i), fmt.Sprintf("# B%d\n\na heron in the body\n", i))
		}
		e.put(pc...)
		if abstracts {
			e.setRetrieval(true, false)
		}
		return e
	}
	with, without := build(true), build(false)
	opts := QueryOpts{Limit: 10, Mode: ModeLexical}
	plain := pathsOfResult(without.search("heron", opts))
	if strings.Join(plain, " ") != "b0.md b1.md b2.md" {
		t.Fatalf("built without abstracts: %v", plain)
	}
	opts.Retrieval = &Retrieval{AbstractChunk: off()}
	if got := pathsOfResult(with.search("heron", opts)); !reflect.DeepEqual(got, plain) {
		t.Errorf("abstracts left out: %v, want the index without them's %v", got, plain)
	}
	opts.Retrieval = &Retrieval{AbstractChunk: on()}
	if got := pathsOfResult(with.search("heron", opts)); len(got) == 0 || !strings.HasPrefix(got[0], "a") {
		t.Errorf("abstracts searched: %v, want an abstract first", got)
	}
}

// TestTheDemotionInASearch: with demotion asked for, a superseded document's hit sits below its
// successor's and names it; not asked for, the order is the search's own.
func TestTheDemotionInASearch(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("old.md", "# Old\n\nkestrel kestrel kestrel\n", "new.md", "---\nsupersedes: [old.md]\n---\n# New\n\nkestrel\n")
	opts := QueryOpts{Limit: 5, Mode: ModeLexical}
	if got := pathsOfResult(e.search("kestrel", opts)); !reflect.DeepEqual(got, []string{"old.md", "new.md"}) {
		t.Fatalf("no demotion: %v, want old.md first (it says kestrel three times)", got)
	}
	opts.Retrieval = &Retrieval{DemoteSuperseded: on()}
	res := e.search("kestrel", opts)
	if got := pathsOfResult(res); !reflect.DeepEqual(got, []string{"new.md", "old.md"}) {
		t.Errorf("demoted: %v, want new.md first", got)
	}
	if res.Hits[1].SupersededBy != "new.md" || res.Hits[0].SupersededBy != "" {
		t.Errorf("marks: %q %q", res.Hits[0].SupersededBy, res.Hits[1].SupersededBy)
	}
	// the workspace setting turns it on for every query; a query may turn it off again
	e.setRetrieval(false, true)
	if got := pathsOfResult(e.search("kestrel", QueryOpts{Limit: 5, Mode: ModeLexical})); !reflect.DeepEqual(got, []string{"new.md", "old.md"}) {
		t.Errorf("the workspace's demotion: %v", got)
	}
	opts.Retrieval = &Retrieval{DemoteSuperseded: off()}
	if got := pathsOfResult(e.search("kestrel", opts)); !reflect.DeepEqual(got, []string{"old.md", "new.md"}) {
		t.Errorf("a query's demotion off: %v", got)
	}
}

func pathsOfResult(r Result) []string {
	out := make([]string, len(r.Hits))
	for i, h := range r.Hits {
		out[i] = h.Path
	}
	return out
}

// TestASupersededHitNamesASuccessorWithNoHit: a superseded hit names its successor though the
// successor has no hit, whichever side says so: its own superseded_by, or the successor's supersedes.
func TestASupersededHitNamesASuccessorWithNoHit(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("old.md", "---\nsuperseded_by: [new.md]\n---\n# Old\n\nkestrel\n", "new.md", "# New\n\nnothing here\n",
		"older.md", "# Older\n\nkestrel\n", "newer.md", "---\nsupersedes: [older.md]\n---\n# Newer\n\nnothing here\n")
	res := e.search("kestrel", QueryOpts{Limit: 5, Mode: ModeLexical, Retrieval: &Retrieval{DemoteSuperseded: on()}})
	marks := map[string]string{}
	for _, h := range res.Hits {
		marks[h.Path] = h.SupersededBy
	}
	want := map[string]string{"old.md": "new.md", "older.md": "newer.md"}
	if !reflect.DeepEqual(marks, want) {
		t.Errorf("marks %v, want %v", marks, want)
	}
}

// TestTheDemotionCutsAfterItMoves: with demotion, the search fetches past the limit, so a successor
// ranked beyond it is found, takes its place above the superseded hit, and the cut falls after the
// move: a limit of one answers with the successor alone.
func TestTheDemotionCutsAfterItMoves(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("old.md", "# Old\n\nkestrel kestrel kestrel\n", "new.md", "---\nsupersedes: [old.md]\n---\n# New\n\nkestrel\n")
	opts := QueryOpts{Limit: 1, Mode: ModeLexical}
	if got := pathsOfResult(e.search("kestrel", opts)); !reflect.DeepEqual(got, []string{"old.md"}) {
		t.Fatalf("no demotion: %v, want old.md alone", got)
	}
	opts.Retrieval = &Retrieval{DemoteSuperseded: on()}
	if got := pathsOfResult(e.search("kestrel", opts)); !reflect.DeepEqual(got, []string{"new.md"}) {
		t.Errorf("demoted, limit 1: %v, want new.md alone", got)
	}
}
