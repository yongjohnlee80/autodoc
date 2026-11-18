package ingest

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// declared is the facet declaration of these cells: two keywords, a number and a date.
var declared = map[string]string{"status": pgstore.FacetKeyword, "territory": pgstore.FacetKeyword, "bpm": pgstore.FacetNumber, "released": pgstore.FacetDate}

// A row's declared facets become its document's: a keyword's values beside the access facets, as
// given (case kept), a list value as several; a number's and a date's as typed values (a date
// alone is midnight UTC). A missing key, a null, an empty list or an empty string is no value. A
// value that does not parse is the row's error, naming the facet. The typed values are in the
// Version, so a changed value writes the document again; a row with no declared value hashes as
// it did before facets were declared, so an upgrade writes nothing again. The mutant, named here:
// contentHash without the values (a changed bpm keeps its Version).
func TestARowsDeclaredFacetsBecomeItsDocuments(t *testing.T) {
	path := writeView(t, trackView)
	s, err := LoadDeclared(path, []string{"label_id"}, declared)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := Load(path, []string{"label_id"})
	if err != nil {
		t.Fatal(err)
	}
	doc := func(s *Source, js string) (pgstore.Document, error) {
		t.Helper()
		r := decode([]byte(js))
		p, err := s.Path(r.id)
		if err != nil {
			t.Fatal(err)
		}
		return s.document(r, p)
	}
	base := `"id": 7, "updated_at": "2026-10-12T10:00:00Z", "title": "Blue", "artist": "Ann", "lyric": "la la", "label_id": 42`
	d, err := doc(s, `{`+base+`, "status": "Released", "territory": ["CA", "US"], "bpm": "120", "released": "2024-03-01"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.Facets["status"], []string{"Released"}) || !slices.Equal(d.Facets["territory"], []string{"CA", "US"}) ||
		!slices.Equal(d.Facets["label_id"], []string{"42"}) || len(d.Facets) != 3 {
		t.Errorf("Facets %v", d.Facets)
	}
	if !slices.Equal(d.Values["bpm"], []float64{120}) || !slices.Equal(d.Values["released"], []float64{1709251200}) || len(d.Values) != 2 {
		t.Errorf("Values %v", d.Values)
	}
	list, err := doc(s, `{`+base+`, "bpm": [90, 126.5], "released": "2024-03-01T12:00:00+02:00"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(list.Values["bpm"], []float64{90, 126.5}) || !slices.Equal(list.Values["released"], []float64{1709287200}) {
		t.Errorf("a list and an instant: %v", list.Values)
	}
	none, err := doc(s, `{`+base+`, "status": null, "territory": [], "bpm": "", "released": [null]}`)
	if err != nil {
		t.Fatal(err)
	}
	was, err := doc(plain, `{`+base+`}`)
	if err != nil {
		t.Fatal(err)
	}
	if len(none.Values) != 0 || len(none.Facets) != 1 || none.Version != was.Version {
		t.Errorf("no values: Facets %v, Values %v, Version %q; want the access facet alone, no value, and the undeclared source's Version %q",
			none.Facets, none.Values, none.Version, was.Version)
	}
	faster, err := doc(s, `{`+base+`, "status": "Released", "territory": ["CA", "US"], "bpm": "121", "released": "2024-03-01"}`)
	if err != nil {
		t.Fatal(err)
	}
	if faster.Version == d.Version {
		t.Errorf("a changed bpm keeps the Version %q: a refresh would not write it", d.Version)
	}
	for js, says := range map[string]string{
		`{` + base + `, "bpm": "fast"}`:            "the facet bpm",
		`{` + base + `, "bpm": "NaN"}`:             "the facet bpm",
		`{` + base + `, "bpm": [1, "x"]}`:          "the facet bpm",
		`{` + base + `, "released": "13/01/2024"}`: "the facet released",
		`{` + base + `, "status": {"a": 1}}`:       "the facet status is not a string",
	} {
		if _, err := doc(s, js); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v, want %q", js, err, says)
		}
	}
	for name, typ := range map[string]string{"label_id": pgstore.FacetKeyword, "tags": pgstore.FacetKeyword, "bpm": "integer"} {
		var ve *ViewError
		if _, err := LoadDeclared(path, []string{"label_id"}, map[string]string{name: typ}); !errors.As(err, &ve) || !strings.Contains(err.Error(), name) {
			t.Errorf("declaring %s as %s: %v; want a ViewError naming it", name, typ, err)
		}
	}
}

// A refresh is due while the tenant's last complete refresh wrote another facet declaration than
// the source's: a fact of the store, so it holds whichever server changed the configuration. In the
// sequence of a rolling restart (the policy given the new declaration, then a server still on the
// old one completes its refresh), the next ingester of the new declaration finds it due, and says
// so; after a restart before its refresh completes, still; after its refresh, no longer, nor after
// another restart. The refresh writes every document again with its values, and their texts keep
// their vectors: nothing is pending for the embedding worker, which calls no provider. The
// mutants, named here: the refresh not recording its declaration (due again after it); due read
// from the policy's change rather than the store (the old server's refresh answers it).
func TestARefreshIsDueWhileTheLastWroteAnotherDeclaration(t *testing.T) {
	f := newFixture(t)
	f.exec(`ALTER TABLE track ADD COLUMN bpm int`)
	view := writeView(t, strings.Replace(trackView, "'lyric', t.lyric,", "'lyric', t.lyric, 'bpm', t.bpm,", 1))
	f.track(1, "A", "One", 1)
	f.track(2, "A", "Two", 2)
	f.exec(`UPDATE track SET bpm = 100 + id`)
	before, err := Load(view, []string{"label_id"})
	if err != nil {
		t.Fatal(err)
	}
	after, err := LoadDeclared(view, []string{"label_id"}, map[string]string{"bpm": pgstore.FacetNumber})
	if err != nil {
		t.Fatal(err)
	}
	policy := func(src *Source) {
		t.Helper()
		if err := f.st.SetPolicy(f.ctx, pgstore.Policy{AccessFields: []string{"label_id"}, View: src.Name(), Facets: src.Facets()}); err != nil {
			t.Fatal(err)
		}
	}
	refresh := func(in *Ingester, written int) {
		t.Helper()
		if st, err := in.Refresh(f.ctx); err != nil || !st.Complete || st.Written != written {
			t.Fatalf("the refresh: %+v, %v; want complete, %d written", st, err, written)
		}
	}
	due := func(in *Ingester, want string, when string) {
		t.Helper()
		if got := in.State().RefreshDue; got != want {
			t.Fatalf("%s: refresh due %q; want %q", when, got, want)
		}
	}

	policy(before)
	old := f.ingester(before, Options{}, "")
	due(old, "", "the old declaration, never refreshed")
	refresh(old, 2)
	const model = "test|embed||4"
	if err := old.Store().SetModels(f.ctx, model, ""); err != nil {
		t.Fatal(err)
	}
	texts, _, err := old.Store().Pending(f.ctx, pgstore.PendingQuery{Model: model, Unready: true, Limit: 100})
	if err != nil || len(texts) == 0 {
		t.Fatalf("the first refresh's texts: %d, %v", len(texts), err)
	}
	for _, x := range texts {
		if err := old.Store().Embed(f.ctx, model, []pgstore.Embedding{{TextHash: x.TextHash, Vec: []float32{1, 0, 0, 0}}}); err != nil {
			t.Fatal(err)
		}
	}

	// a server restarts with the new declaration while the old one, holding the lease, refreshes
	policy(after)
	refresh(old, 0) // the old declaration's refresh completes after the change: it writes nothing new
	old.Release()

	in := f.ingester(after, Options{}, "")
	due(in, RefreshFacets, "the new declaration after the old server's refresh")
	in.Release()
	in = f.ingester(after, Options{}, "")
	due(in, RefreshFacets, "a restart before the refresh completed")
	refresh(in, 2)
	due(in, "", "after the refresh")
	if got := f.scalar(f.store, `SELECT string_agg(d.path || ':' || v.field || '=' || v.value::text, ',' ORDER BY d.path)
		FROM rag_document_value v JOIN rag_document d ON d.tenant = v.tenant AND d.id = v.doc_id WHERE v.tenant = $1`, f.tenant); got != "track/1.md:bpm=101,track/2.md:bpm=102" {
		t.Errorf("the values after the refresh: %s", got)
	}
	if texts, _, err := in.Store().Pending(f.ctx, pgstore.PendingQuery{Model: model, Unready: true, Limit: 100}); err != nil || len(texts) != 0 {
		t.Errorf("texts pending after the refresh: %d, %v; want none (no provider call)", len(texts), err)
	}
	in.Release()
	in = f.ingester(after, Options{}, "")
	due(in, "", "a restart after the refresh")
}

// Run refreshes at once while a refresh is due for the declaration, though its schedule says the
// last one is recent. The mutant, named here: the schedule alone deciding (no refresh within the
// wait).
func TestRunRefreshesAtOnceForADeclaration(t *testing.T) {
	f := newFixture(t)
	f.exec(`ALTER TABLE track ADD COLUMN bpm int`)
	view := writeView(t, strings.Replace(trackView, "'lyric', t.lyric,", "'lyric', t.lyric, 'bpm', t.bpm,", 1))
	f.track(1, "A", "One", 1)
	f.exec(`UPDATE track SET bpm = 120`)
	before, err := Load(view, []string{"label_id"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	o := Options{Interval: 20 * time.Millisecond, Refresh: time.Hour, Clock: func() time.Time { return now }}
	old := f.ingester(before, o, "")
	if _, err := old.Refresh(f.ctx); err != nil { // refreshed now: the next is an hour away
		t.Fatal(err)
	}
	old.Release()
	after, err := LoadDeclared(view, []string{"label_id"}, map[string]string{"bpm": pgstore.FacetNumber})
	if err != nil {
		t.Fatal(err)
	}
	in := f.ingester(after, o, "")
	ctx, cancel := context.WithCancel(f.ctx)
	done := make(chan error, 1)
	go func() { done <- in.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, 5*time.Second, "the refresh for the new declaration", func() bool {
		return in.State().RefreshDue == "" && !in.State().LastRefresh.At.IsZero()
	})
}
