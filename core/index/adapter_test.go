package index

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/searchtest"
	"github.com/yongjohnlee80/golib/search/vector"

	"github.com/yongjohnlee80/autodoc/core/schema"
)

// bodyProvider embeds a section's body alone (the text after its breadcrumb's line) with the
// conformance suite's Embed, so the store's vectors are the ones the suite expects. A text holding
// refuse is rejected, as too long would be: its document is never semantic-ready.
type bodyProvider struct{ refuse string }

func (bodyProvider) Name() string { return "fake" }
func (bodyProvider) Model() embed.Model {
	return embed.Model{Provider: "fake", Name: "body", Digest: "sha256:body", Dims: searchtest.Dims}
}

func (p bodyProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		if p.refuse != "" && strings.Contains(t, p.refuse) {
			return nil, fmt.Errorf("%w: refused", embed.ErrRejected)
		}
		_, body, _ := strings.Cut(t, "\n")
		out[i] = searchtest.Embed(body)
	}
	return out, nil
}

// fileOf is a corpus document as a Markdown file: its tags and facets in the frontmatter, and each
// chunk a section of its own (so chunk i is section i), its links at the end of the last.
func fileOf(d searchtest.Doc) string {
	var b strings.Builder
	b.WriteString("---\n")
	if len(d.Tags) > 0 {
		fmt.Fprintf(&b, "tags: [%s]\n", strings.Join(d.Tags, ", "))
	}
	for _, f := range []string{"kind", "level"} {
		if v := d.Facets[f]; len(v) > 0 {
			fmt.Fprintf(&b, "%s: %s\n", f, v[0])
		}
	}
	b.WriteString("---\n\n")
	for i, c := range d.Chunks {
		fmt.Fprintf(&b, "## s%d\n\n%s", i, c)
		if i == len(d.Chunks)-1 {
			for _, l := range d.Links {
				fmt.Fprintf(&b, " [[%s]]", l)
			}
		}
		b.WriteString("\n\n")
	}
	return b.String()
}

const conformanceSchema = "version: 1\nfrontmatter:\n  kind:\n    type: string\n  level:\n    type: string\n"

// TestTheStorePassesTheSearchConformanceSuite: AutoDoc's index, written through its own indexer,
// answers golib's search port as the engine expects.
func TestTheStorePassesTheSearchConformanceSuite(t *testing.T) {
	searchtest.Run(t, func(tb testing.TB, docs []searchtest.Doc) searchtest.Fixture[int64, *View] {
		sch, err := schema.Parse([]byte(conformanceSchema))
		if err != nil {
			tb.Fatal(err)
		}
		var sv schemaVar
		sv.v.Store(&schemaState{sch, "conformance"})
		e := newEnv(tb, Options{Provider: bodyProvider{refuse: "glimmer"}, Schema: sv.get})
		var later []searchtest.Doc
		for _, d := range docs {
			if d.Unready {
				later = append(later, d)
				continue
			}
			e.put(d.Path, fileOf(d))
		}
		e.ready()
		for _, d := range later {
			e.put(d.Path, fileOf(d)) // its text is refused: it stays not ready
		}
		e.atHead()
		byPath := map[string]searchtest.Doc{}
		for _, d := range docs {
			byPath[d.Path] = d
		}
		change := func(path string, chunks []string) {
			d := byPath[path]
			d.Chunks = chunks
			e.put(path, fileOf(d))
			e.ready2(path)
			e.atHead()
		}
		return searchtest.Fixture[int64, *View]{Store: searchStore{s: e.store, sem: e.ix.sem}, Model: e.activeModel(), Change: change}
	})
}

// queryVec is q embedded by p as a search embeds it.
func queryVec(t *testing.T, p embed.Provider, q string) []float32 {
	t.Helper()
	vecs, err := p.Embed(context.Background(), []string{q})
	if err != nil {
		t.Fatal(err)
	}
	return vector.Normalize(vecs[0])
}

func pathsOf(cs []search.Candidate[int64]) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Path)
	}
	return out
}

// TestAHeldPublishTakesTheCompleteFallback: a query that sees a commit whose code index is not yet
// published (a seam holds the publish) scans the stored codes, and answers completely: the document
// that commit made ready is found, and the answer is the one the published index gives after.
func TestAHeldPublishTakesTheCompleteFallback(t *testing.T) {
	var hold atomic.Bool
	release := make(chan struct{})
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p, beforePublish: func() {
		if hold.CompareAndSwap(true, false) {
			<-release
		}
	}})
	e.put("a.md", "zebra plains\n", "b.md", "hippo river\n")
	e.ready()
	e.atHead()
	p.hold("quokka")
	e.put("c.md", "zebra quokka\n") // indexed, its vector held
	hold.Store(true)
	p.unhold() // the vector commits; its publish waits on the seam
	e.eventually("c.md ready", func() bool {
		var ready int
		_ = scanOne(context.Background(), e.raw, &ready, "SELECT semantic_ready FROM document WHERE path = 'c.md'")
		return ready == 1
	})
	falls, snaps := e.ix.sem.fallbackScans.Load(), e.ix.sem.snapshotScans.Load()
	held := e.query("zebra quokka", QueryOpts{Mode: ModeSemantic})
	if e.ix.sem.fallbackScans.Load() != falls+1 || e.ix.sem.snapshotScans.Load() != snaps {
		t.Fatal("a query during a held publish did not take the fallback")
	}
	if got := via(held); len(got) == 0 || got[0] != "c.md semantic" {
		t.Errorf("the fallback's answer %q lacks the document just made ready", got)
	}
	close(release)
	e.atHead()
	after := e.query("zebra quokka", QueryOpts{Mode: ModeSemantic})
	if e.ix.sem.snapshotScans.Load() != snaps+1 {
		t.Error("the query after the publish did not use the index")
	}
	if !reflect.DeepEqual(held, after) {
		t.Errorf("the fallback and the published index answer differently:\n%+v\n%+v", held, after)
	}
}

// TestAViewHeldAcrossADeletionScansItsSnapshot: a view's transaction at commit N while a deletion
// commits and publishes N+1 does not use the newer index (it lacks a chunk still alive at N); it
// scans the stored codes in its own snapshot and answers as before the deletion.
func TestAViewHeldAcrossADeletionScansItsSnapshot(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p, noGC: true})
	e.put("a.md", "zebra alpha\n", "b.md", "zebra beta\n")
	e.ready()
	e.atHead()
	st, model, vec := searchStore{s: e.store, sem: e.ix.sem}, e.activeModel(), queryVec(t, p, "b\nzebra beta")
	err := st.View(context.Background(), func(v *View) error {
		before, err := v.Semantic(context.Background(), model, vec, search.Filter{}, 10)
		if err != nil {
			return err
		}
		e.remove("b.md")
		e.atHead() // the index of the deletion's commit is published
		falls := e.ix.sem.fallbackScans.Load()
		after, err := v.Semantic(context.Background(), model, vec, search.Filter{}, 10)
		if err != nil {
			return err
		}
		if e.ix.sem.fallbackScans.Load() != falls+1 {
			t.Error("the held view used the newer index")
		}
		if !reflect.DeepEqual(before, after) || !strings.Contains(strings.Join(pathsOf(after), " "), "b.md") {
			t.Errorf("the held view's answer changed: %v, then %v", pathsOf(before), pathsOf(after))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestAViewHeldOnTheOldModelScansItsCodes: a view whose snapshot has model A active, held while
// model B's flip commits and B's index is published, rejects B's index and scans A's codes in its
// own snapshot.
func TestAViewHeldOnTheOldModelScansItsCodes(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra giraffe\n", "y.md", "hippo river\n")
	e.ready()
	e.atHead()
	fpA, vecA := a.Model().Fingerprint(), queryVec(t, a, "x\nzebra giraffe")
	e.stop()
	b := newFake("m2", "b")
	b.hold("hippo")
	e.stop = nil
	e.open(Options{Provider: b})
	e.eventually("b filling", func() bool { return len(b.texts()) > 0 })
	st := searchStore{s: e.store, sem: e.ix.sem}
	err := st.View(context.Background(), func(v *View) error {
		before, err := v.Semantic(context.Background(), fpA, vecA, search.Filter{}, 10)
		if err != nil {
			return err
		}
		if st, err := v.SemanticState(context.Background()); err != nil || st != search.StateSwitching {
			t.Errorf("while b fills the view's state is %q, %v; want switching", st, err)
		}
		b.unhold()
		fpB := b.Model().Fingerprint()
		e.eventually("b active and its index published", func() bool {
			idx := e.ix.sem.snap.Load()
			return e.activeModel() == fpB && idx != nil && idx.Model() == fpB
		})
		falls := e.ix.sem.fallbackScans.Load()
		after, err := v.Semantic(context.Background(), fpA, vecA, search.Filter{}, 10)
		if err != nil {
			return err
		}
		if e.ix.sem.fallbackScans.Load() != falls+1 {
			t.Error("the view on model A used model B's index")
		}
		if len(before) == 0 || !reflect.DeepEqual(before, after) {
			t.Errorf("the held view's answer: %v, then %v", pathsOf(before), pathsOf(after))
		}
		// the state is the snapshot's too: A active, B the target, so still switching
		if st, err := v.SemanticState(context.Background()); err != nil || st != search.StateSwitching {
			t.Errorf("after B's flip the held view's state is %q, %v; want its own snapshot's, switching", st, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	err = st.View(context.Background(), func(v *View) error {
		if st, err := v.SemanticState(context.Background()); err != nil || st != search.StateReady {
			t.Errorf("a view after the flip has state %q, %v; want ready", st, err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestAQueryEmbeddedWithAMovedModelIsEmbeddedAgain: a query embedded with a model that is no longer
// active when its view opens gets ErrModelChanged from the view (which scans nothing), and the
// engine embeds it again. AutoDoc's own flips cannot land there (only the target embeds, and a flip
// only ever activates the target), so the seam is the embedder: its first answer names a model
// that has moved.
func TestAQueryEmbeddedWithAMovedModelIsEmbeddedAgain(t *testing.T) {
	var calls atomic.Int32
	var stale sync.Once
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p, NewSearcher: func(st search.Store[int64, *View], emb search.QueryEmbedder) search.Searcher {
		if emb == nil {
			return defaultSearcher(st, nil)
		}
		return defaultSearcher(st, func(ctx context.Context, q string) (string, []float32, error) {
			calls.Add(1)
			fp, vec, err := emb(ctx, q)
			stale.Do(func() { fp = "moved|model||64" })
			return fp, vec, err
		})
	}})
	e.put("a.md", "zebra plains\n", "b.md", "hippo river\n")
	e.ready()
	e.atHead()
	scans := func() int64 { return e.ix.sem.snapshotScans.Load() + e.ix.sem.fallbackScans.Load() }
	before := scans()
	res := e.query("zebra", QueryOpts{Mode: ModeSemantic})
	if calls.Load() != 2 {
		t.Errorf("the query was embedded %d times, want 2", calls.Load())
	}
	if scans() != before+1 {
		t.Errorf("%d scans, want 1: the moved model's attempt scanned", scans()-before)
	}
	if got := via(res); len(got) == 0 || got[0] != "a.md semantic" || res.Semantic != SemanticReady {
		t.Errorf("after the retry: %+v", res)
	}
}

// TestEveryFilterAloneLists: a query with no words and a tag or a path filter lists the documents
// it admits, as one with a facet filter always did (golib's engine lists for every kind of filter);
// a root path is no filter, and lists nothing.
func TestEveryFilterAloneLists(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("guides/a.md", "---\ntags: [design]\n---\nalpha\n", "guides/b.md", "beta\n", "notes/c.md", "---\ntags: [design]\n---\ngamma\n")
	for name, c := range map[string]struct {
		opts QueryOpts
		want []string
	}{
		"a tag":               {QueryOpts{Tags: []string{"design"}}, []string{"guides/a.md", "notes/c.md"}},
		"a path":              {QueryOpts{Paths: []string{"guides"}}, []string{"guides/a.md", "guides/b.md"}},
		"a tag and a path":    {QueryOpts{Tags: []string{"design"}, Paths: []string{"guides"}}, []string{"guides/a.md"}},
		"a root path":         {QueryOpts{Paths: []string{"."}}, nil},
		"the root as /":       {QueryOpts{Paths: []string{"/"}}, nil},
		"the root as ./":      {QueryOpts{Paths: []string{"./"}}, nil},
		"the root and a path": {QueryOpts{Paths: []string{"/", "guides"}}, nil},
		"a path with slashes": {QueryOpts{Paths: []string{"/guides/"}}, []string{"guides/a.md", "guides/b.md"}},
		"no words, no filter": {QueryOpts{}, nil},
	} {
		res, err := e.store.Search(context.Background(), "  ", c.opts)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got := paths(res); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: listed %q, want %q", name, got, c.want)
		}
		if len(c.want) > 0 && res.ModeUsed != ModeFacet {
			t.Errorf("%s: mode %q, want facet", name, res.ModeUsed)
		}
	}
}
