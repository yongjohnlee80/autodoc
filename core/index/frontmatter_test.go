package index

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/schema"
)

const facetSchema = `version: 1
frontmatter:
  type:
    type: string
    enum: [note, adr]
    required: true
  status:
    type: string
    enum: [draft, active]
    default: active
  count:
    type: integer
`

// schemaVar is a swappable schema, as the daemon holds one per workspace.
type schemaVar struct{ v atomic.Pointer[schemaState] }

type schemaState struct {
	s  *schema.Schema
	fp string
}

func (sv *schemaVar) set(t *testing.T, src, fp string) {
	t.Helper()
	s, err := schema.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	sv.v.Store(&schemaState{s, fp})
}

func (sv *schemaVar) get() (*schema.Schema, string) {
	st := sv.v.Load()
	if st == nil {
		return nil, ""
	}
	return st.s, st.fp
}

func (e *env) facets(p string) []string {
	e.t.Helper()
	rows, err := e.raw.QueryContext(context.Background(), `SELECT f.field || '=' || f.value FROM doc_facet f
		JOIN document d ON d.id = f.doc_id WHERE d.path = ? ORDER BY f.field, f.value`, p)
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
	return out
}

func hitPaths(r Result) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.Path)
	}
	return out
}

func TestSchemaFacetsAndDiagnostics(t *testing.T) {
	var sv schemaVar
	sv.set(t, facetSchema, "s1")
	e := newEnv(t, Options{Schema: sv.get})
	e.write("adr.md", "---\ntype: adr\ncount: 3\n---\nzebra crossing\n")
	e.write("memo.md", "---\ntype: memo\nstatus: draft\n---\nzebra stripes\n")
	e.write("bare.md", "zebra plain\n")
	for _, p := range []string{"adr.md", "memo.md", "bare.md"} {
		e.ix.Touch(p)
		e.indexedAt(p)
	}
	if got, want := e.facets("adr.md"), []string{"count=3", "status=active", "type=adr"}; !reflect.DeepEqual(got, want) {
		t.Errorf("adr.md facets = %v, want %v (status from its default)", got, want)
	}
	if got, want := e.facets("memo.md"), []string{"status=draft"}; !reflect.DeepEqual(got, want) {
		t.Errorf("memo.md facets = %v, want %v (an invalid type is not admitted)", got, want)
	}
	ctx := context.Background()
	d, err := e.store.Diagnostics(ctx, "memo.md")
	if err != nil {
		t.Fatal(err)
	}
	if len(d) != 1 || d[0].Field != "type" || d[0].Rule != schema.RuleEnum || d[0].Line != 2 {
		t.Errorf("memo.md diagnostics = %+v", d)
	}
	if d, _ := e.store.Diagnostics(ctx, "bare.md"); len(d) != 1 || d[0].Rule != schema.RuleRequired {
		t.Errorf("bare.md diagnostics = %+v", d)
	}
	st, _ := e.store.Status(ctx)
	if st.Diagnosed != 2 {
		t.Errorf("Status().Diagnosed = %d, want 2", st.Diagnosed)
	}
	// invalid frontmatter never takes the body out of search
	r, err := e.ix.Search(ctx, "zebra", QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitPaths(r); len(got) != 3 {
		t.Errorf("zebra = %v, want all three", got)
	}
	for q, want := range map[string][]string{
		"zebra type:adr":     {"adr.md"},
		"zebra status:draft": {"memo.md"},
		"zebra count:0x3":    {"adr.md"}, // read as its field's type
		"zebra type:note":    nil,
		"type:adr":           {"adr.md"}, // filters alone list the notes
		"zebra other:thing":  nil,        // an undeclared field is a word, which no note has
	} {
		r, err := e.ix.Search(ctx, q, QueryOpts{})
		if err != nil {
			t.Errorf("%q: %v", q, err)
			continue
		}
		if got := hitPaths(r); !reflect.DeepEqual(got, want) {
			t.Errorf("%q = %v, want %v", q, got, want)
		}
	}
	r, err = e.ix.Search(ctx, "zebra", QueryOpts{Facets: map[string][]string{"status": {"active", "draft"}, "type": {"adr", "note"}}})
	if err != nil || !reflect.DeepEqual(hitPaths(r), []string{"adr.md"}) {
		t.Errorf("opts facets = %v, %v; want adr.md (values OR, fields AND)", hitPaths(r), err)
	}
	if _, err := e.ix.Search(ctx, "zebra", QueryOpts{Facets: map[string][]string{"owner": {"me"}}}); !errors.Is(err, ErrUnknownFacet) {
		t.Errorf("undeclared facet option: %v, want ErrUnknownFacet", err)
	}
	if _, err := e.ix.Search(ctx, "zebra count:many", QueryOpts{}); !errors.Is(err, ErrFacetValue) {
		t.Errorf("count:many: %v, want ErrFacetValue", err)
	}
}

// A facet filter narrows every retriever before its top-N: a matching note ranked far below the
// retriever's limit is still found.
func TestFacetFilterAppliesBeforeTopN(t *testing.T) {
	var sv schemaVar
	sv.set(t, facetSchema, "s1")
	e := newEnv(t, Options{Schema: sv.get})
	for i := range retrieverTop + 10 {
		p := fmt.Sprintf("n%03d.md", i)
		e.write(p, fmt.Sprintf("---\ntype: note\ntitle: common common\n---\n# common\n\ncommon common %d\n", i))
		e.ix.Touch(p)
	}
	e.write("z-adr.md", "---\ntype: adr\n---\n"+strings.Repeat("filler words here. ", 200)+"common\n")
	e.ix.Touch("z-adr.md")
	e.indexedAt("z-adr.md")
	e.eventually("all indexed", func() bool { return e.pendingJobs() == 0 })
	ctx := context.Background()
	r, err := e.ix.Search(ctx, "common", QueryOpts{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range r.Hits {
		if h.Path == "z-adr.md" {
			t.Fatalf("precondition: z-adr.md is within the unfiltered top %d; the test proves nothing", retrieverTop)
		}
	}
	r, err = e.ix.Search(ctx, "common type:adr", QueryOpts{})
	if err != nil {
		t.Fatal(err)
	}
	if got := hitPaths(r); !reflect.DeepEqual(got, []string{"z-adr.md"}) {
		t.Fatalf("filtered = %v, want z-adr.md", got)
	}
}

// A schema change rebuilds exactly the Markdown notes, keeping every unchanged chunk (and so its
// vector), and its facets follow the new schema.
func TestSchemaChangeRevalidatesMarkdownOnly(t *testing.T) {
	var sv schemaVar
	sv.set(t, facetSchema, "s1")
	e := newEnv(t, Options{Schema: sv.get, Match: func(p string) bool { return testMatch(p) || strings.HasSuffix(p, ".txt") }})
	e.write("a.md", "---\ntype: draft\n---\nalpha\n")
	e.write("t.txt", "type: adr in plain text\n")
	for _, p := range []string{"a.md", "t.txt"} {
		e.ix.Touch(p)
		e.indexedAt(p)
	}
	if got := e.facets("a.md"); !reflect.DeepEqual(got, []string{"status=active"}) {
		t.Fatalf("before: %v", got)
	}
	_, before := e.alive("a.md")
	parses := e.ix.Parses()
	sv.set(t, strings.Replace(facetSchema, "enum: [note, adr]", "enum: [note, adr, draft]", 1), "s2")
	if err := e.ix.Revalidate(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.eventually("a.md revalidated", func() bool { return reflect.DeepEqual(e.facets("a.md"), []string{"status=active", "type=draft"}) })
	e.eventually("no jobs", func() bool { return e.pendingJobs() == 0 })
	if got := e.ix.Parses() - parses; got != 1 {
		t.Errorf("%d parses after the schema change, want 1 (a.md; t.txt has no frontmatter)", got)
	}
	_, after := e.alive("a.md")
	if len(after) != len(before) || after[0].genFrom != before[0].genFrom {
		t.Errorf("chunks before %+v, after %+v: an unchanged chunk must be reused", before, after)
	}
	if got := e.facets("t.txt"); len(got) != 0 {
		t.Errorf("t.txt facets = %v: plain text has no frontmatter", got)
	}
}

// A schema edited while the daemon was down is applied at the next start.
func TestSchemaChangedWhileStoppedRevalidatesOnStart(t *testing.T) {
	var sv schemaVar
	sv.set(t, facetSchema, "s1")
	e := newEnv(t, Options{Schema: sv.get})
	e.write("a.md", "---\ntype: draft\n---\nalpha\n")
	e.ix.Touch("a.md")
	e.indexedAt("a.md")
	e.stop()
	e.stop = nil
	sv.set(t, strings.Replace(facetSchema, "enum: [note, adr]", "enum: [note, adr, draft]", 1), "s2")
	e.open(Options{Schema: sv.get})
	e.eventually("a.md revalidated at start", func() bool {
		return reflect.DeepEqual(e.facets("a.md"), []string{"status=active", "type=draft"})
	})
}

// Without a schema, malformed frontmatter is still a diagnostic; nothing is faceted.
func TestNoSchemaStillDiagnosesMalformedFrontmatter(t *testing.T) {
	e := newEnv(t, Options{})
	e.write("bad.md", "---\ntitle: [unclosed\n---\nbody\n")
	e.write("ok.md", "---\ntype: adr\n---\nbody\n")
	for _, p := range []string{"bad.md", "ok.md"} {
		e.ix.Touch(p)
		e.indexedAt(p)
	}
	d, err := e.store.Diagnostics(context.Background(), "bad.md")
	if err != nil || len(d) != 1 || d[0].Rule != schema.RuleYAML {
		t.Fatalf("bad.md diagnostics = %+v, %v", d, err)
	}
	if got := e.facets("ok.md"); len(got) != 0 {
		t.Fatalf("ok.md facets without a schema = %v", got)
	}
	if _, err := e.ix.Search(context.Background(), "body", QueryOpts{Facets: map[string][]string{"type": {"adr"}}}); !errors.Is(err, ErrUnknownFacet) {
		t.Fatalf("facets without a schema: %v, want ErrUnknownFacet", err)
	}
}
