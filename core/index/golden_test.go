package index

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/schema"

	"github.com/yongjohnlee80/golib/search/vector"
)

// The golden cells pin what the indexer stores and what search answers, so that moving the
// chunkers, the vector math and the search orchestration elsewhere changes neither: no workspace
// re-chunks or re-embeds, and every query answers as before. The goldens were recorded before any
// of that code moved; regenerate them only for an intended change, with -update-goldens.
var updateGoldens = flag.Bool("update-goldens", false, "rewrite testdata/golden/*.json from this build")

// goldenCorpus is every kind of document the chunkers and the metadata reader treat differently.
func goldenCorpus() map[string]string {
	c := map[string]string{
		"guides/architecture.md": `---
title: Architecture Guide
tags: [design, core]
aliases: [arch]
type: adr
status: active
count: 3
---

# Overview

The daemon owns the store; clients search it over a socket. See [[storage-notes]] and the
[reference][r] for the wire format. #inline-tag

## Storage

| table    | holds                      |
|----------|----------------------------|
| document | one row per file           |
| chunk    | one row per section        |
| model    | one row per embedding model |

## Search

` + "```go" + `
func search(q string) []Hit {
	return fuse(lexical(q), semantic(q))
}
` + "```" + `

[r]: https://example.com/wire
`,
		"guides/storage-notes.md": `# Storage Notes

SQLite keeps the store in one file. The [architecture](architecture.md) explains why.
Storage is written by one writer and read by many readers.

## Durability

The write-ahead log makes every commit durable before the writer reports it.
`,
		"notes/draft.md": `---
type: note
status: draft
tags: [draft]
---

# Draft ideas

A draft about storage compaction and vacuum scheduling.
`,
		"notes/schema-bad.md": `---
type: memo
status: unknown
count: many
---

# Off-schema

This note breaks the schema in three fields but still indexes its storage words.
`,
		"notes/broken-frontmatter.md": "---\ntitle: [unclosed\ntags: {\n---\n\n# Broken\n\nThe body still indexes: pelican storage.\n",
		"notes/empty.md":              "",
		"notes/unicode.md":            "# 東京 notes\n\nnaïve café résumé 東京タワー 🚀 " + strings.Repeat("ünïcödé wörds ", 40) + "\n",
		"notes/plain.txt": "Plain text notes about the heron and the egret.\n\nA second paragraph mentions storage once.\n\n" +
			strings.Repeat("A very long plain paragraph about wading birds and marsh water. ", 60) + "\n",
		"config/settings.yaml": `# service settings
database:
  host: db.internal
  port: 5432
  pools: [read, write]
search:
  limit: 20
  modes:
    - lexical
    - semantic
owners:
  - name: storage team
    pager: true
`,
		"config/broken.yaml": "database:\n  host: [unclosed\n\tport: tabs are not yaml\nflamingo storage\n",
		"big/table.md":       "# Big table\n\n" + bigTable(70) + "\nAfter the table, storage again.\n",
		"big/code.md":        "# Big code\n\n```go\n" + bigCode(90) + "```\n",
		"big/list.md":        "# Big list\n\n" + bigList(40),
		// an adversarial fixture for the lexical order: one document, one section, chunks of equal
		// BM25 rank for "kestrel"
		"big/paragraphs.md": "# Repeated\n\n" + strings.Repeat("The kestrel hovers over the field and "+strings.Repeat("waits ", 90)+"\n\n", 12),
		// the windowed semantic path: the keep/ files rank behind every noise/ note by Hamming
		// distance, and a path filter admits only keep/
		"keep/k1.md": "zebra giraffe lion savanna\n",
		"keep/k2.md": "zebra giraffe okapi forest\n",
	}
	for i := range 220 {
		c[fmt.Sprintf("noise/n%03d.md", i)] = "zebra giraffe\n"
	}
	return c
}

func bigTable(rows int) string {
	var b strings.Builder
	b.WriteString("| id | name | description |\n|----|------|-------------|\n")
	for i := range rows {
		fmt.Fprintf(&b, "| %d | row%d | a table row describing item %d with several plain words |\n", i, i, i)
	}
	return b.String()
}

func bigCode(lines int) string {
	var b strings.Builder
	b.WriteString("func main() {\n")
	for i := range lines {
		fmt.Fprintf(&b, "\tvalue%d := compute(%d, \"argument number %d\")\n", i, i, i)
	}
	b.WriteString("}\n")
	return b.String()
}

func bigList(items int) string {
	var b strings.Builder
	for i := range items {
		fmt.Fprintf(&b, "- item %d about list handling and nested content\n\n  a continuation paragraph for item %d with more words\n\n", i, i)
	}
	return b.String()
}

func goldenMatch(p string) bool {
	switch filepath.Ext(p) {
	case ".md", ".txt", ".yaml", ".yml":
		return true
	}
	return false
}

// goldenEnv indexes the corpus with a deterministic provider and the facet schema, and waits until
// every document is semantic-ready and the snapshot is at the head.
func goldenEnv(t testing.TB) (*env, *fakeProvider) {
	t.Helper()
	p := newFake("m", "a")
	sch, err := schema.Parse([]byte(facetSchema))
	if err != nil {
		t.Fatal(err)
	}
	var sv schemaVar
	sv.v.Store(&schemaState{sch, "fp1"})
	e := newEnv(t, Options{Provider: p, Match: goldenMatch, Schema: sv.get, BatchDelay: time.Millisecond})
	// one document at a time, in path order, so documents and chunks get the same ids every run:
	// the semantic scan breaks equal Hamming distances by chunk id, and touched paths reach the
	// indexer's queue in no set order
	corpus := goldenCorpus()
	for _, k := range slices.Sorted(maps.Keys(corpus)) {
		e.put(k, corpus[k])
	}
	e.ready()
	e.atHead()
	return e, p
}

// goldenDoc is what the indexer stored for one document.
type goldenDoc struct {
	Path, Indexer, Title, FrontmatterJSON, FrontmatterError string
	Tags, Aliases, Facets, Diagnostics, Links               []string
	Chunks                                                  []goldenChunk
}

type goldenChunk struct {
	Ord                int
	Breadcrumb, Title  string
	Tags               string
	ByteStart, ByteEnd int
	BodyBytes          int
	Hash, TextHash     string
	Embed              string `json:",omitempty"` // the built-ins' is "", so their goldens never name it
}

func (e *env) goldenDocs() []goldenDoc {
	e.t.Helper()
	ctx := context.Background()
	rows, err := e.raw.QueryContext(ctx, `SELECT id, path, indexer, COALESCE(title, ''), COALESCE(frontmatter_json, ''),
		COALESCE(frontmatter_error, '') FROM document ORDER BY path`)
	if err != nil {
		e.t.Fatal(err)
	}
	type idDoc struct {
		id  int64
		doc goldenDoc
	}
	var docs []idDoc
	for rows.Next() {
		var d idDoc
		if err := rows.Scan(&d.id, &d.doc.Path, &d.doc.Indexer, &d.doc.Title, &d.doc.FrontmatterJSON, &d.doc.FrontmatterError); err != nil {
			e.t.Fatal(err)
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		e.t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		e.t.Fatal(err)
	}
	strs := func(q string, id int64) []string {
		rows, err := e.raw.QueryContext(ctx, q, id)
		if err != nil {
			e.t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				e.t.Fatal(err)
			}
			out = append(out, s)
		}
		if err := rows.Err(); err != nil {
			e.t.Fatal(err)
		}
		return out
	}
	out := make([]goldenDoc, 0, len(docs))
	for _, d := range docs {
		d.doc.Tags = strs("SELECT tag FROM doc_tag WHERE doc_id = ? ORDER BY tag", d.id)
		d.doc.Aliases = strs("SELECT alias FROM doc_alias WHERE doc_id = ? ORDER BY alias", d.id)
		d.doc.Facets = strs("SELECT field || '=' || value FROM doc_facet WHERE doc_id = ? ORDER BY field, value", d.id)
		d.doc.Diagnostics = strs("SELECT ord || ' ' || field || ' ' || line || ' ' || rule || ': ' || message FROM doc_diagnostic WHERE doc_id = ? ORDER BY ord", d.id)
		d.doc.Links = strs(`SELECT l.kind || ' ' || l.raw || ' -> ' || l.name || COALESCE('#' || l.anchor, '') || ' = ' || COALESCE(dd.path, '(none)')
			FROM link l LEFT JOIN document dd ON dd.id = l.dst_doc WHERE l.src_doc = ? AND l.gen_to IS NULL ORDER BY l.id`, d.id)
		rows, err := e.raw.QueryContext(ctx, `SELECT ord, breadcrumb, title, tags, byte_start, byte_end, length(CAST(body AS BLOB)), hash, text_hash, embed
			FROM chunk WHERE doc_id = ? AND gen_to IS NULL ORDER BY ord`, d.id)
		if err != nil {
			e.t.Fatal(err)
		}
		for rows.Next() {
			var c goldenChunk
			var h, th []byte
			if err := rows.Scan(&c.Ord, &c.Breadcrumb, &c.Title, &c.Tags, &c.ByteStart, &c.ByteEnd, &c.BodyBytes, &h, &th, &c.Embed); err != nil {
				e.t.Fatal(err)
			}
			c.Hash, c.TextHash = hex.EncodeToString(h), hex.EncodeToString(th)
			d.doc.Chunks = append(d.doc.Chunks, c)
		}
		if err := rows.Err(); err != nil {
			e.t.Fatal(err)
		}
		if err := rows.Close(); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, d.doc)
	}
	return out
}

// golden compares got, as indented JSON, with testdata/golden/name, or rewrites it with
// -update-goldens.
func golden(t *testing.T, name string, got any) {
	t.Helper()
	b, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, '\n')
	path := filepath.Join("testdata", "golden", name)
	if *updateGoldens {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (record it with -update-goldens)", err)
	}
	if !bytes.Equal(b, want) {
		gotPath := filepath.Join(os.TempDir(), "autodoc-golden-"+name) // outlives the test, to diff
		_ = os.WriteFile(gotPath, b, 0o644)
		t.Errorf("%s differs from the golden; this build's is %s (diff them)", path, gotPath)
	}
}

// TestGoldenStoredDocuments: every document's metadata, links, facets, diagnostics and live chunks
// (positions, breadcrumbs, hashes, text hashes) and its indexer version are as recorded.
func TestGoldenStoredDocuments(t *testing.T) {
	e, _ := goldenEnv(t)
	docs := e.goldenDocs()
	// the fixture's own preconditions: each kind it exists for is there and did what it is for
	byPath := map[string]goldenDoc{}
	for _, d := range docs {
		byPath[d.Path] = d
	}
	for p, want := range map[string]func(goldenDoc) bool{
		"big/table.md":                func(d goldenDoc) bool { return len(d.Chunks) > 2 },
		"big/code.md":                 func(d goldenDoc) bool { return len(d.Chunks) > 2 },
		"big/list.md":                 func(d goldenDoc) bool { return len(d.Chunks) > 2 },
		"big/paragraphs.md":           func(d goldenDoc) bool { return len(d.Chunks) > 3 },
		"notes/plain.txt":             func(d goldenDoc) bool { return len(d.Chunks) > 2 },
		"config/settings.yaml":        func(d goldenDoc) bool { return len(d.Chunks) > 0 },
		"config/broken.yaml":          func(d goldenDoc) bool { return len(d.Chunks) > 0 },
		"notes/broken-frontmatter.md": func(d goldenDoc) bool { return d.FrontmatterError != "" },
		"notes/schema-bad.md":         func(d goldenDoc) bool { return len(d.Diagnostics) > 0 },
		"guides/architecture.md": func(d goldenDoc) bool {
			return len(d.Links) == 1 && len(d.Tags) == 3 && len(d.Aliases) == 1 && len(d.Facets) == 3
		},
		"guides/storage-notes.md": func(d goldenDoc) bool { return len(d.Links) == 1 },
	} {
		if d, ok := byPath[p]; !ok || !want(d) {
			t.Fatalf("the fixture no longer exercises %s: %+v", p, d)
		}
	}
	golden(t, "documents.json", docs)
}

// goldenQuery is one search of the golden: through the indexer (provider and schema), or through
// the store alone (no provider, no schema).
type goldenQuery struct {
	Name  string
	Q     string
	Opts  QueryOpts
	Store bool `json:",omitempty"`
}

type goldenAnswer struct {
	Query  goldenQuery
	Result *Result `json:",omitempty"`
	Err    string  `json:",omitempty"`
}

func (e *env) ask(q goldenQuery) goldenAnswer {
	var res Result
	var err error
	if q.Store {
		res, err = e.store.Search(context.Background(), q.Q, q.Opts)
	} else {
		res, err = e.ix.Search(context.Background(), q.Q, q.Opts)
	}
	if err != nil {
		return goldenAnswer{Query: q, Err: err.Error()}
	}
	return goldenAnswer{Query: q, Result: &res}
}

// readyQueries run while every document is semantic-ready.
var readyQueries = []goldenQuery{
	{Name: "lexical word", Q: "storage", Opts: QueryOpts{Mode: ModeLexical}},
	{Name: "auto word", Q: "storage"},
	{Name: "semantic word", Q: "storage", Opts: QueryOpts{Mode: ModeSemantic}},
	{Name: "two words", Q: "sqlite storage"},
	{Name: "prefix", Q: "stor*", Opts: QueryOpts{Mode: ModeLexical}},
	{Name: "punctuation", Q: "storage, !!! (sqlite)"},
	{Name: "fts syntax is words", Q: `storage OR "quoted" NEAR(x) -y ^z col:val`, Opts: QueryOpts{Mode: ModeLexical}},
	{Name: "tag filter", Q: "storage", Opts: QueryOpts{Tags: []string{"#Design"}}},
	{Name: "two tags", Q: "storage", Opts: QueryOpts{Tags: []string{"design", "core"}}},
	{Name: "no document has the tags", Q: "storage", Opts: QueryOpts{Tags: []string{"design", "draft"}}},
	{Name: "directory filter", Q: "storage", Opts: QueryOpts{Paths: []string{"guides/"}}},
	{Name: "file filter", Q: "storage", Opts: QueryOpts{Paths: []string{"guides/architecture.md"}}},
	{Name: "root path is no filter", Q: "storage", Opts: QueryOpts{Paths: []string{"keep", "."}}},
	{Name: "facet word", Q: "type:adr storage"},
	{Name: "facet option", Q: "storage", Opts: QueryOpts{Facets: map[string][]string{"status": {"active", "draft"}}}},
	{Name: "facet word and option", Q: "type:note storage", Opts: QueryOpts{Facets: map[string][]string{"status": {"draft"}}}},
	{Name: "facets alone", Q: "type:adr"},
	{Name: "facet option alone", Q: "", Opts: QueryOpts{Facets: map[string][]string{"status": {"draft"}}}},
	{Name: "undeclared field is a word", Q: "re:storage storage"},
	{Name: "bad facet value", Q: "count:many storage"},
	{Name: "unknown facet option", Q: "storage", Opts: QueryOpts{Facets: map[string][]string{"nope": {"x"}}}},
	{Name: "tag boost", Q: "design"},
	{Name: "link boost", Q: "writer"},
	{Name: "big table", Q: "table row", Opts: QueryOpts{Limit: 50}},
	{Name: "big code", Q: "compute argument"},
	{Name: "big list", Q: "continuation paragraph"},
	{Name: "unicode", Q: "東京タワー"},
	{Name: "accents", Q: "naïve café"},
	{Name: "yaml", Q: "database host"},
	{Name: "broken yaml", Q: "flamingo"},
	{Name: "plain text", Q: "heron egret"},
	{Name: "broken frontmatter body", Q: "pelican"},
	{Name: "equal rank, one document", Q: "kestrel", Opts: QueryOpts{Mode: ModeLexical, Limit: 50}},
	{Name: "windowed semantic", Q: "zebra giraffe", Opts: QueryOpts{Mode: ModeSemantic, Paths: []string{"keep"}}},
	{Name: "windowed hybrid", Q: "zebra giraffe", Opts: QueryOpts{Paths: []string{"keep"}}},
	{Name: "limit", Q: "zebra", Opts: QueryOpts{Limit: 5}},
	{Name: "limit clamped", Q: "zebra", Opts: QueryOpts{Limit: 500}},
	{Name: "nothing matches", Q: "xylophone"},
	{Name: "empty query", Q: "   "},
	{Name: "unknown mode", Q: "storage", Opts: QueryOpts{Mode: "fuzzy"}},
	{Name: "store: auto", Q: "storage", Store: true},
	{Name: "store: semantic", Q: "storage", Opts: QueryOpts{Mode: ModeSemantic}, Store: true},
	{Name: "store: facet word is a word", Q: "type:adr storage", Store: true},
	{Name: "store: facet option", Q: "storage", Opts: QueryOpts{Facets: map[string][]string{"status": {"active"}}}, Store: true},
}

// TestGoldenSearch: every query answers as recorded, in each semantic state the corpus can reach
// (off, ready, error, partial), and each semantic answer is the same from the snapshot and from
// the SQL fallback.
func TestGoldenSearch(t *testing.T) {
	e, p := goldenEnv(t)
	e.windowedPrecondition(t, p)
	var answers []goldenAnswer
	for _, q := range readyQueries {
		a := e.ask(q)
		if q.Opts.Mode != ModeLexical && !q.Store && a.Err == "" {
			// the same answer from the SQL fallback: a snapshot one commit behind is never used
			cur := e.atHead()
			e.ix.sem.snap.Store(cur.Next(cur.Watermark()-1, nil, nil))
			falls := e.ix.sem.fallbackScans.Load()
			b := e.ask(q)
			switch {
			case b.Result == nil:
				t.Errorf("%s: the fallback failed: %s", q.Name, b.Err)
			case a.Result.Semantic == SemanticReady && e.ix.sem.fallbackScans.Load() == falls && b.Result.ModeUsed != ModeFacet:
				t.Errorf("%s: the fallback did not run", q.Name)
			case !reflect.DeepEqual(a, b):
				t.Errorf("%s: the snapshot and the fallback answer differently:\n%+v\n%+v", q.Name, a, b)
			}
			e.ix.sem.snap.Store(cur)
		}
		answers = append(answers, a)
	}
	// the error state: the query cannot be embedded
	p.fail("failword")
	answers = append(answers,
		e.ask(goldenQuery{Name: "embedding fails: auto", Q: "storage failword"}),
		e.ask(goldenQuery{Name: "embedding fails: semantic", Q: "storage failword", Opts: QueryOpts{Mode: ModeSemantic}}),
		e.ask(goldenQuery{Name: "embedding fails: lexical", Q: "storage failword", Opts: QueryOpts{Mode: ModeLexical}}))
	p.fail("")
	// the partial state: one document's vectors are held back
	p.hold("quokka")
	e.put("late/quokka.md", "quokka storage durability\n")
	answers = append(answers,
		e.ask(goldenQuery{Name: "partial: auto", Q: "storage durability"}),
		e.ask(goldenQuery{Name: "partial: semantic", Q: "storage durability", Opts: QueryOpts{Mode: ModeSemantic}}),
		e.ask(goldenQuery{Name: "partial: lexical", Q: "storage durability", Opts: QueryOpts{Mode: ModeLexical}}))
	p.unhold()
	for _, a := range answers[len(answers)-3:] {
		if a.Result == nil || a.Result.Semantic != SemanticPartial {
			t.Fatalf("the fixture no longer reaches the partial state: %+v", a)
		}
	}
	golden(t, "search.json", answers)
}

// windowedPrecondition: more than one window (hammingTop) of codes is nearer the windowed query
// than any keep/ code, so its answer comes only after the first window is filtered away.
func (e *env) windowedPrecondition(t *testing.T, p *fakeProvider) {
	t.Helper()
	vecs, err := p.Embed(context.Background(), []string{"zebra giraffe"})
	if err != nil {
		t.Fatal(err)
	}
	q := vector.SignBits(vector.Normalize(vecs[0]))
	codes, err := e.codes(e.activeModel())
	if err != nil {
		t.Fatal(err)
	}
	keep := map[int64]bool{}
	for _, path := range []string{"keep/k1.md", "keep/k2.md"} {
		var id int64
		if err := scanOne(context.Background(), e.raw, &id, "SELECT id FROM document WHERE path = ?", path); err != nil {
			t.Fatal(err)
		}
		keep[id] = true
	}
	dist := func(c code) int { return vector.Hamming(c.Bits, q) }
	best := 1 << 30
	for doc, cs := range codes {
		if keep[doc] {
			for _, c := range cs {
				best = min(best, dist(c))
			}
		}
	}
	ahead := 0
	for doc, cs := range codes {
		if !keep[doc] {
			for _, c := range cs {
				if dist(c) < best {
					ahead++
				}
			}
		}
	}
	if ahead <= hammingTop {
		t.Fatalf("the windowed fixture has %d codes ahead of keep/, want more than %d", ahead, hammingTop)
	}
}
