package pgstore

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// The access corpus: label A in two territories, label B in one, a document with no label, and an
// unready B document. b1.md links to a1.md.
var accessDocs = []searchtest.Doc{
	{Path: "a1.md", Facets: map[string][]string{"label_id": {"A"}, "territory": {"US"}}, Chunks: []string{"common words alpha"}},
	{Path: "a2.md", Facets: map[string][]string{"label_id": {"A"}, "territory": {"CA"}}, Chunks: []string{"common words beta"}},
	{Path: "b1.md", Facets: map[string][]string{"label_id": {"B"}, "territory": {"US"}}, Chunks: []string{"common words gamma"}, Links: []string{"a1.md"}},
	{Path: "n1.md", Chunks: []string{"common words delta"}},
	{Path: "b2.md", Facets: map[string][]string{"label_id": {"B"}}, Chunks: []string{"common words epsilon"}, Unready: true},
}

// answers is what each retrieval path answers in one View: the paths Lexical, Semantic (nearest
// to query) and List return with filter f, at most 100 each, and the paths Signals answers for ids.
type answers struct {
	lexical, semantic, list, signals []string
	inLinks                          map[string]int
	state                            search.State
}

func retrieve(t *testing.T, v *View, f search.Filter, query string, ids map[int64]string) answers {
	t.Helper()
	ctx := context.Background()
	var r answers
	paths := func(cs []search.Candidate[int64], err error) []string {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, c := range cs {
			if !slices.Contains(out, c.Path) {
				out = append(out, c.Path)
			}
		}
		slices.Sort(out)
		return out
	}
	r.lexical = paths(v.Lexical(ctx, []search.Term{{Text: "common"}}, f, 100))
	r.semantic = paths(v.Semantic(ctx, model, searchtest.Embed(query), f, 100))
	r.list = paths(v.List(ctx, f, 100))
	all := make([]int64, 0, len(ids))
	for id := range ids {
		all = append(all, id)
	}
	sig, err := v.Signals(ctx, all)
	if err != nil {
		t.Fatal(err)
	}
	r.inLinks = map[string]int{}
	for id, s := range sig {
		r.signals = append(r.signals, ids[id])
		r.inLinks[ids[id]] = s.InLinks
	}
	slices.Sort(r.signals)
	if r.state, err = v.SemanticState(ctx); err != nil {
		t.Fatal(err)
	}
	return r
}

func join(p []string) string { return strings.Join(p, ",") }

// A View within label A never answers a B document from Lexical, Semantic, List or Signals, even
// when the filter asks for label B or for A and B, and even when the query is nearest to a B
// document by meaning; a B document's link to an A document is no in-link of it; an unready B
// document does not make A's state partial. Values within a field are ORed and fields are ANDed;
// a document with no label is outside every scope. An empty access, a field with no values or no
// name, or a name with '=', is refused before fn runs.
//
// The mutant, named here: access applied as a facet merged into the filter (its values appended to
// Filter.Facets["label_id"]) — a filter asking for label B then ORs with A, and B answers.
func TestAViewWithinNeverAnswersOutsideItsAccess(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", accessDocs)
	ids := map[int64]string{}
	if err := s.View(ctx, func(v *View) error {
		got, err := v.List(ctx, search.Filter{}, 100)
		for _, c := range got {
			ids[c.Doc] = c.Path
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(ids) != len(accessDocs) {
		t.Fatalf("the corpus lists %d documents, want %d", len(ids), len(accessDocs))
	}

	// unscoped, the corpus answers whole: the cells below are not empty by accident
	if err := s.View(ctx, func(v *View) error {
		r := retrieve(t, v, search.Filter{}, "common words gamma", ids)
		if join(r.lexical) != "a1.md,a2.md,b1.md,b2.md,n1.md" || join(r.semantic) != "a1.md,a2.md,b1.md,n1.md" ||
			r.inLinks["a1.md"] != 1 || r.state != search.StatePartial {
			t.Errorf("unscoped: %+v", r)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	onlyA := map[string][]string{"label_id": {"A"}}
	for _, c := range []struct {
		name   string
		filter search.Filter
		want   string
	}{
		{"no filter", search.Filter{}, "a1.md,a2.md"},
		{"a filter for B", search.Filter{Facets: map[string][]string{"label_id": {"B"}}}, ""},
		{"a filter for A or B", search.Filter{Facets: map[string][]string{"label_id": {"A", "B"}}}, "a1.md,a2.md"},
		{"a filter on another field", search.Filter{Facets: map[string][]string{"territory": {"US"}}}, "a1.md"},
	} {
		if err := s.ViewWithin(ctx, onlyA, func(v *View) error {
			// nearest by meaning to B's own text: still never B
			r := retrieve(t, v, c.filter, "common words gamma", ids)
			for path, got := range map[string][]string{"lexical": r.lexical, "semantic": r.semantic, "list": r.list} {
				if join(got) != c.want {
					t.Errorf("within A, %s: %s answered %v, want %q", c.name, path, got, c.want)
				}
			}
			if join(r.signals) != "a1.md,a2.md" {
				t.Errorf("within A, %s: signals for %v, want a1.md and a2.md alone", c.name, r.signals)
			}
			if r.inLinks["a1.md"] != 0 {
				t.Errorf("within A, %s: a1.md counts %d in-links, want none (its one is from B)", c.name, r.inLinks["a1.md"])
			}
			if r.state != search.StateReady {
				t.Errorf("within A, %s: state %q, want ready (the unready document is B's)", c.name, r.state)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	// values ORed within a field, fields ANDed
	for _, c := range []struct {
		access map[string][]string
		want   string
	}{
		{map[string][]string{"label_id": {"A", "B"}}, "a1.md,a2.md,b1.md,b2.md"},
		{map[string][]string{"label_id": {"A", "B"}, "territory": {"US"}}, "a1.md,b1.md"},
		{map[string][]string{"label_id": {"A"}, "territory": {"US"}}, "a1.md"},
		{map[string][]string{"label_id": {"B"}, "territory": {"CA"}}, ""},
	} {
		if err := s.ViewWithin(ctx, c.access, func(v *View) error {
			r := retrieve(t, v, search.Filter{}, "common words alpha", ids)
			if join(r.lexical) != c.want || join(r.list) != c.want || join(r.signals) != c.want {
				t.Errorf("within %v: lexical %v, list %v, signals %v; want %q", c.access, r.lexical, r.list, r.signals, c.want)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	for _, bad := range []map[string][]string{nil, {}, {"label_id": nil}, {"label_id": {}}, {"": {"A"}}, {"label_id=A": {"x"}}} {
		ran := false
		err := s.ViewWithin(ctx, bad, func(*View) error { ran = true; return nil })
		if !errors.Is(err, ErrNoAccess) || ran {
			t.Errorf("access %v: %v (fn ran: %v), want ErrNoAccess before fn", bad, err, ran)
		}
	}
}
