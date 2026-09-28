package index

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode"

	"github.com/yongjohnlee80/autodoc/core/embed"
)

// fakeProvider embeds a text as the counts of its words hashed into dims buckets (with seed, so two
// models differ): texts sharing words are near. held texts wait for release; failing texts fail.
type fakeProvider struct {
	name, digest string
	seed         string
	dims         int

	mu      sync.Mutex
	calls   [][]string
	held    string // a text holding this word waits for release
	release chan struct{}
	failing string // a text holding this word fails
	refuse  string // a batch holding a text with this word is rejected (the input, not the provider)
}

func newFake(name, seed string) *fakeProvider {
	return &fakeProvider{name: name, digest: "sha256:" + seed, seed: seed, dims: 64, release: make(chan struct{})}
}

func (f *fakeProvider) Name() string { return "fake" }
func (f *fakeProvider) Model() embed.Model {
	return embed.Model{Provider: "fake", Name: f.name, Digest: f.digest, Dims: f.dims}
}

func (f *fakeProvider) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	f.mu.Lock()
	f.calls = append(f.calls, texts)
	held, failing, release, refuse := f.held, f.failing, f.release, f.refuse
	f.mu.Unlock()
	for _, t := range texts {
		if refuse != "" && strings.Contains(t, refuse) {
			return nil, fmt.Errorf("%w: fake: input too long", embed.ErrRejected)
		}
		if failing != "" && strings.Contains(t, failing) {
			return nil, errors.New("fake: the provider is down")
		}
		if held != "" && strings.Contains(t, held) {
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	out := make([][]float32, len(texts))
	for i, t := range texts {
		v := make([]float32, f.dims)
		for _, w := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool { return !unicode.IsLetter(r) }) {
			h := fnv.New32a()
			h.Write([]byte(f.seed + w))
			v[h.Sum32()%uint32(f.dims)]++
		}
		out[i] = v
	}
	return out, nil
}

func (f *fakeProvider) hold(word string) {
	f.mu.Lock()
	f.held, f.release = word, make(chan struct{})
	f.mu.Unlock()
}

func (f *fakeProvider) unhold() {
	f.mu.Lock()
	close(f.release)
	f.held = ""
	f.mu.Unlock()
}

func (f *fakeProvider) fail(word string) {
	f.mu.Lock()
	f.failing = word
	f.mu.Unlock()
}

// texts lists every text the provider was asked to embed.
func (f *fakeProvider) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c...)
	}
	return out
}

// ready waits until every document is semantic-ready under the active model.
func (e *env) ready() {
	e.t.Helper()
	e.eventually("every document semantic-ready", func() bool {
		var unready, docs int
		_ = scanOne(context.Background(), e.store.r, &docs, "SELECT COUNT(*) FROM document")
		_ = scanOne(context.Background(), e.store.r, &unready, "SELECT COUNT(*) FROM document WHERE semantic_ready = 0")
		return docs > 0 && unready == 0
	})
}

func (e *env) query(q string, opts QueryOpts) Result {
	e.t.Helper()
	res, err := e.ix.Search(context.Background(), q, opts)
	if err != nil {
		e.t.Fatalf("search %q: %v", q, err)
	}
	return res
}

func via(r Result) []string {
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.Path+" "+strings.Join(h.Via, "+"))
	}
	return out
}

func (e *env) activeModel() string {
	e.t.Helper()
	var fp string
	_ = scanOne(context.Background(), e.store.r, &fp, "SELECT fp FROM model WHERE active = 1")
	return fp
}

// TestSemanticFindsWhatLexicalCannot: every query word must match lexically; the semantic tier finds
// the note that shares some of them, and a hit both find carries both.
func TestSemanticFindsWhatLexicalCannot(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	// the nearer note sorts after the other by path: only the vectors put it first
	e.put("z.md", "zebra giraffe savanna\n", "b.md", "hippo river mud\n")
	e.ready()
	for _, c := range []struct {
		mode, used string
		want       []string
	}{
		{ModeAuto, ModeHybrid, []string{"z.md semantic", "b.md semantic"}},
		{ModeSemantic, ModeSemantic, []string{"z.md semantic", "b.md semantic"}},
		{ModeLexical, ModeLexical, nil},
	} {
		res := e.query("zebra lion", QueryOpts{Mode: c.mode})
		eq(t, c.mode, via(res), c.want)
		if res.ModeUsed != c.used || res.Semantic != SemanticReady {
			t.Errorf("%s: used %q, semantic %q", c.mode, res.ModeUsed, res.Semantic)
		}
	}
	eq(t, "both", via(e.query("zebra", QueryOpts{})), []string{"z.md lexical+semantic", "b.md semantic"})
	if e.activeModel() != p.Model().Fingerprint() {
		t.Errorf("active model %q", e.activeModel())
	}
}

// TestHalfEmbeddedDocumentAnswersLexically: a document with a chunk still waiting for its vector
// is not semantic-ready: it gives no semantic hit, not even from its embedded chunks, and answers
// lexically; the search says "partial". Once the chunk has its vector, it answers semantically.
func TestHalfEmbeddedDocumentAnswersLexically(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	e.put("a.md", "zebra plains\n")
	e.ready()
	p.hold("glacier")
	defer func() {
		p.mu.Lock()
		held := p.held
		p.mu.Unlock()
		if held != "" {
			p.unhold()
		}
	}()
	// c.md's first chunk is a.md's text exactly (title a, the same body), so it has its vector
	// already; its second waits: c.md is half-embedded (one batch holds every text, so a held text
	// holds its whole batch, and two new chunks would both wait)
	e.put("c.md", "---\ntitle: a\n---\nzebra plains\n\n# Two\n\nglacier ice\n")
	e.eventually("c.md's second chunk asked for", func() bool {
		for _, s := range p.texts() {
			if strings.Contains(s, "glacier ice") {
				return true
			}
		}
		return false
	})
	time.Sleep(50 * time.Millisecond)
	var cID int64
	_ = scanOne(context.Background(), e.store.r, &cID, "SELECT id FROM document WHERE path = 'c.md'")
	if _, in := e.ix.sem.snap.Load().docs[cID]; in {
		t.Error("the half-embedded c.md is in the code snapshot")
	}
	for round := range 2 {
		if round == 1 {
			// a snapshot that wrongly holds c.md's embedded chunk: the query's own check still refuses it
			cur := e.ix.sem.snap.Load()
			forged := &codeSnap{fp: cur.fp, watermark: cur.watermark, docs: map[int64][]code{}}
			for d, cs := range cur.docs {
				forged.docs[d] = cs
			}
			codes, err := loadCodes(context.Background(), e.store.r, cur.fp, nil)
			if err != nil {
				t.Fatal(err)
			}
			var chunks []code
			rows, _ := e.store.r.QueryContext(context.Background(), `SELECT c.id, e.bits FROM chunk c JOIN embedding e
				ON e.text_hash = c.text_hash AND e.model_fp = ? WHERE c.doc_id = ?`, cur.fp, cID)
			for rows.Next() {
				var c code
				var b []byte
				_ = rows.Scan(&c.chunk, &b)
				c.bits = bitsOf(b)
				chunks = append(chunks, c)
			}
			rows.Close()
			if len(chunks) != 1 || len(codes[cID]) != 0 {
				t.Fatalf("c.md has %d embedded chunks (%d as ready codes); want 1 and 0", len(chunks), len(codes[cID]))
			}
			forged.docs[cID] = chunks
			e.ix.sem.snap.Store(forged)
		}
		checkPartial(t, e, cID)
	}
	p.unhold()
	e.ready()
	res := e.query("zebra", QueryOpts{Mode: ModeSemantic})
	if res.Semantic != SemanticReady || len(res.Hits) < 2 {
		t.Errorf("after the vector: %q, %q", res.Semantic, via(res))
	}
}

func checkPartial(t *testing.T, e *env, cID int64) {
	t.Helper()
	for _, m := range []string{ModeAuto, ModeSemantic} {
		res := e.query("zebra", QueryOpts{Mode: m})
		if res.Semantic != SemanticPartial {
			t.Errorf("%s: semantic %q, want partial", m, res.Semantic)
		}
		for _, h := range res.Hits {
			if h.Path == "c.md" && strings.Contains(strings.Join(h.Via, "+"), "semantic") {
				t.Errorf("%s: the half-embedded c.md gave a semantic hit: %+v", m, h)
			}
		}
		if m == ModeAuto && !reflect.DeepEqual(via(res), []string{"a.md lexical+semantic", "c.md lexical"}) {
			t.Errorf("auto while partial: %q", via(res))
		}
	}
}

// TestEditClearsReadiness: a new generation with an unembedded chunk makes its document wait again.
func TestEditClearsReadiness(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	e.put("a.md", "zebra\n")
	e.ready()
	p.hold("volcano")
	e.put("a.md", "zebra\n\n# More\n\nvolcano ash\n")
	var ready int
	_ = scanOne(context.Background(), e.store.r, &ready, "SELECT semantic_ready FROM document")
	if ready != 0 {
		t.Error("an edit adding an unembedded chunk left the document ready")
	}
	if got := e.query("zebra", QueryOpts{}).Semantic; got != SemanticPartial {
		t.Errorf("semantic %q, want partial", got)
	}
	p.unhold()
	e.ready()
}

// TestQueryEmbedFailure: a query the provider cannot embed answers lexically in auto mode, with
// SemanticError and its constant message, and fails with ErrEmbedFailed in semantic mode.
func TestQueryEmbedFailure(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	e.put("a.md", "zebra stripes\n")
	e.ready()
	p.fail("stripes")
	res := e.query("zebra stripes", QueryOpts{})
	if res.Semantic != SemanticError || res.SemanticError != ErrEmbedFailed.Error() || res.ModeUsed != ModeLexical ||
		!reflect.DeepEqual(via(res), []string{"a.md lexical"}) {
		t.Errorf("auto: %+v", res)
	}
	if strings.Contains(res.SemanticError, "down") {
		t.Errorf("the provider's own error reached the client: %q", res.SemanticError)
	}
	if _, err := e.ix.Search(context.Background(), "zebra stripes", QueryOpts{Mode: ModeSemantic}); !errors.Is(err, ErrEmbedFailed) {
		t.Errorf("semantic: %v, want ErrEmbedFailed", err)
	}
	// the workers' own failure shows in the status: a new text the provider cannot embed
	e.put("b.md", "stripes of a tiger\n")
	e.eventually("the failure in the status", func() bool {
		es := e.embStatus()
		return strings.Contains(es.LastErr, "down") && es.Pending == 1
	})
}

// TestIdenticalTextIsEmbeddedOnce: vectors are keyed by the embedded text, so two chunks with the
// same text share one, and a document that moves its sections is not embedded again.
func TestIdenticalTextIsEmbeddedOnce(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	e.put("a.md", "# S\n\nshared words here\n", "b.md", "# S\n\nshared words here\n")
	e.ready()
	count := func() int {
		n := 0
		for _, s := range p.texts() {
			if strings.Contains(s, "shared words here") {
				n++
			}
		}
		return n
	}
	// a.md and b.md are both titled S (their first heading): the same breadcrumb and body, one text
	if n := count(); n != 1 {
		t.Errorf("embedded %d times, want 1", n)
	}
	// c.md's twin sections are one text too ("T > S" and the body), another than a.md's
	e.put("c.md", "# T\n\n## S\n\nshared words here\n\n## S\n\nshared words here\n")
	e.ready()
	if n := count(); n != 2 {
		t.Errorf("embedded %d times after c.md's twins, want 2", n)
	}
	e.put("c.md", "# T\n\n## S\n\nshared words here\n\n## New\n\nother\n\n## S\n\nshared words here\n")
	e.ready()
	if n := count(); n != 2 {
		t.Errorf("embedded %d times after an unrelated edit, want 2", n)
	}
}

// TestDeadChunkNeverASemanticHit: a chunk edited away is no hit, even when a snapshot still holds
// its code at the current watermark: candidates are validated in the query's transaction.
func TestDeadChunkNeverASemanticHit(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p, noGC: true})
	e.put("a.md", "# A\n\nplover marsh\n")
	e.ready()
	old := e.ix.sem.snap.Load()
	e.put("a.md", "# A\n\nsandpiper coast\n")
	e.ready()
	cur := e.ix.sem.snap.Load()
	forged := &codeSnap{fp: cur.fp, watermark: cur.watermark, docs: map[int64][]code{}}
	for d, cs := range cur.docs {
		forged.docs[d] = append(append([]code(nil), cs...), old.docs[d]...)
	}
	e.ix.sem.snap.Store(forged)
	scans := e.ix.sem.snapshotScans.Load()
	for _, h := range e.query("plover marsh", QueryOpts{Mode: ModeSemantic}).Hits {
		if strings.Contains(h.Snippet, "plover") {
			t.Errorf("the dead chunk was a hit: %+v", h)
		}
	}
	if e.ix.sem.snapshotScans.Load() != scans+1 {
		t.Fatal("the query did not scan the forged snapshot: this test would show nothing")
	}
}

// TestSnapshotAndFallbackAgree: every commit publishes a snapshot at its commit_seq; a query that
// finds none matching its transaction scans SQL instead, with the same answer.
func TestSnapshotAndFallbackAgree(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	e.put("a.md", "zebra giraffe\n", "b.md", "zebra hippo\n", "c.md", "lion\n")
	e.ready()
	e.put("c.md", "lion zebra\n")
	e.remove("b.md")
	e.ready()
	e.eventually("the snapshot at the head", func() bool {
		var seq int64
		_ = scanOne(context.Background(), e.store.r, &seq, "SELECT CAST(v AS INTEGER) FROM meta WHERE k = 'commit_seq'")
		s := e.ix.sem.snap.Load()
		return s != nil && s.watermark == seq
	})
	snaps, falls := e.ix.sem.snapshotScans.Load(), e.ix.sem.fallbackScans.Load()
	withSnap := e.query("zebra giraffe", QueryOpts{Mode: ModeSemantic})
	if e.ix.sem.snapshotScans.Load() != snaps+1 {
		t.Fatal("the query at the head did not use the snapshot")
	}
	cur := e.ix.sem.snap.Load()
	e.ix.sem.snap.Store(&codeSnap{fp: cur.fp, watermark: cur.watermark - 1, docs: cur.docs})
	withSQL := e.query("zebra giraffe", QueryOpts{Mode: ModeSemantic})
	if e.ix.sem.fallbackScans.Load() != falls+1 {
		t.Fatal("a stale snapshot was used")
	}
	e.ix.sem.snap.Store(&codeSnap{fp: cur.fp, watermark: cur.watermark + 1, docs: map[int64][]code{}})
	withNewer := e.query("zebra giraffe", QueryOpts{Mode: ModeSemantic})
	if e.ix.sem.fallbackScans.Load() != falls+2 {
		t.Fatal("a newer snapshot was used")
	}
	e.ix.sem.snap.Store(&codeSnap{fp: "another|model||64", watermark: cur.watermark, docs: map[int64][]code{}})
	withOther := e.query("zebra giraffe", QueryOpts{Mode: ModeSemantic})
	if e.ix.sem.fallbackScans.Load() != falls+3 {
		t.Fatal("another model's snapshot was used")
	}
	eq(t, "snapshot", via(withSnap), []string{"a.md semantic", "c.md semantic"})
	if !reflect.DeepEqual(withSnap, withSQL) || !reflect.DeepEqual(withSnap, withNewer) || !reflect.DeepEqual(withSnap, withOther) {
		t.Errorf("answers differ:\n%+v\n%+v\n%+v\n%+v", withSnap, withSQL, withNewer, withOther)
	}
}

// TestModelSwitchKeepsTheOldModel: a new target model fills in the background while the old one
// answers; the switch happens once the new model covers every chunk, and queries racing it are
// never empty.
func TestModelSwitchKeepsTheOldModel(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra giraffe\n", "y.md", "hippo river\n", "z.md", "glacier ice\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	e.stop()
	b := newFake("m2", "b")
	b.hold("glacier")
	e.stop = nil
	e.open(Options{Provider: b, ProviderFor: func(m embed.Model) (embed.Provider, error) {
		if m.Fingerprint() == fpA {
			return a, nil
		}
		return nil, fmt.Errorf("no provider for %s", m.Fingerprint())
	}})
	e.eventually("b filling", func() bool { return len(b.texts()) > 0 })
	time.Sleep(50 * time.Millisecond)
	// asking the writer whether b covers every chunk, while it covers none, flips nothing
	if err := e.ix.handVectors(context.Background(), vecBatch{fp: b.Model().Fingerprint()}); err != nil {
		t.Fatal(err)
	}
	if got := e.activeModel(); got != fpA {
		t.Fatalf("active %q while b fills, want a", got)
	}
	if es := e.embStatus(); es.Model != fpA || es.Target != b.Model().Fingerprint() {
		t.Errorf("status while b fills %+v", es)
	}
	before := e.query("zebra", QueryOpts{Mode: ModeSemantic})
	if len(before.Hits) == 0 || before.Semantic != SemanticReady {
		t.Fatalf("the old model does not answer while the new one fills: %+v", before)
	}
	// an edit during the switch gets the old model's vector, so it answers now, not after the flip
	e.put("w.md", "zebra foal\n")
	e.ready2("w.md")
	if got := via(e.query("foal", QueryOpts{Mode: ModeSemantic})); len(got) == 0 || got[0] != "w.md semantic" {
		t.Errorf("a note written during the switch: %q", got)
	}
	// queries race the flip: none is empty
	stop := make(chan struct{})
	var wg sync.WaitGroup
	var empty, done int
	var mu sync.Mutex
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				res, err := e.ix.Search(context.Background(), "zebra", QueryOpts{Mode: ModeSemantic})
				mu.Lock()
				done++
				if err != nil || len(res.Hits) == 0 {
					empty++
				}
				mu.Unlock()
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	b.unhold()
	fpB := b.Model().Fingerprint()
	e.eventually("the flip to b", func() bool { return e.activeModel() == fpB })
	time.Sleep(50 * time.Millisecond)
	close(stop)
	wg.Wait()
	t.Logf("%d queries across the flip", done)
	if empty != 0 {
		t.Errorf("%d of %d queries across the flip were empty or failed", empty, done)
	}
	e.ready()
	if es := e.embStatus(); es.Model != fpB || es.Target != "" || es.Pending != 0 || es.Semantic != SemanticReady {
		t.Errorf("status after the flip %+v", es)
	}
	after := e.query("zebra", QueryOpts{Mode: ModeSemantic})
	if len(after.Hits) == 0 || e.ix.sem.snap.Load().fp != fpB {
		t.Errorf("after the flip: %+v, snapshot %q", after, e.ix.sem.snap.Load().fp)
	}
	// the old model's vectors stay until purged; the active one cannot be purged
	var vecs int
	_ = scanOne(context.Background(), e.store.r, &vecs, "SELECT COUNT(*) FROM embedding WHERE model_fp = ?", fpA)
	if vecs == 0 {
		t.Error("the old model's vectors went implicitly")
	}
	if err := e.ix.PurgeModel(context.Background(), fpB); err == nil {
		t.Error("the active model was purged")
	}
	if err := e.ix.PurgeModel(context.Background(), fpA); err != nil {
		t.Fatal(err)
	}
	_ = scanOne(context.Background(), e.store.r, &vecs, "SELECT COUNT(*) FROM embedding WHERE model_fp = ?", fpA)
	var rows int
	_ = scanOne(context.Background(), e.store.r, &rows, "SELECT COUNT(*) FROM model WHERE fp = ?", fpA)
	if vecs != 0 || rows != 0 {
		t.Errorf("after the purge: %d vectors, %d rows", vecs, rows)
	}
}

// TestSwitchWithoutTheOldProvider: while a new model fills and nothing can embed with the active
// one, a query reports SemanticError (auto) or fails with ErrEmbedFailed (semantic).
func TestSwitchWithoutTheOldProvider(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n", "y.md", "glacier\n")
	e.ready()
	e.stop()
	b := newFake("m2", "b")
	b.hold("glacier")
	defer b.unhold()
	e.stop = nil
	e.open(Options{Provider: b})
	e.eventually("b filling", func() bool { return len(b.texts()) > 0 })
	res := e.query("zebra", QueryOpts{})
	if res.Semantic != SemanticError || !reflect.DeepEqual(via(res), []string{"x.md lexical"}) {
		t.Errorf("auto: %+v", res)
	}
	if _, err := e.ix.Search(context.Background(), "zebra", QueryOpts{Mode: ModeSemantic}); !errors.Is(err, ErrEmbedFailed) {
		t.Errorf("semantic: %v", err)
	}
}

// TestRejectedTextDoesNotBlockOthers: a text the provider rejects is narrowed down and set aside:
// the texts batched with it are embedded, its document answers lexically, and it is not asked for
// again and again.
func TestRejectedTextDoesNotBlockOthers(t *testing.T) {
	p := newFake("m", "a")
	p.refuse = "oversized"
	e := newEnv(t, Options{Provider: p})
	e.put("a.md", "zebra\n", "b.md", "giraffe\n", "bad.md", "# Bad\n\noversized list\n\n# Fine\n\nhippo\n", "c.md", "lion\n")
	e.eventually("the others ready", func() bool {
		var ready int
		_ = scanOne(context.Background(), e.store.r, &ready, "SELECT COUNT(*) FROM document WHERE semantic_ready = 1 AND path != 'bad.md'")
		return ready == 3
	})
	var badReady int
	_ = scanOne(context.Background(), e.store.r, &badReady, "SELECT semantic_ready FROM document WHERE path = 'bad.md'")
	if badReady != 0 {
		t.Error("the document with a rejected chunk is ready")
	}
	// its other chunk has its vector: only the rejected text was set aside
	var vecs int
	_ = scanOne(context.Background(), e.store.r, &vecs, "SELECT COUNT(*) FROM embedding")
	if vecs != 4 {
		t.Errorf("%d vectors, want 4 (every text but the rejected one)", vecs)
	}
	asked := func() int {
		n := 0
		for _, s := range p.texts() {
			if strings.Contains(s, "oversized") {
				n++
			}
		}
		return n
	}
	n := asked()
	e.put("d.md", "tiger\n") // wakes the worker
	e.ready2("d.md")
	time.Sleep(100 * time.Millisecond)
	if again := asked() - n; again != 0 {
		t.Errorf("the rejected text was asked for %d more times", again)
	}
	// (a query holding the rejected word is itself rejected, and answers lexically with "error")
	if st, err := e.ix.Status(context.Background()); err != nil || st.Embeddings == nil {
		t.Fatalf("status: %+v, %v", st, err)
	} else if es := st.Embeddings; es.Refused != 1 || es.Pending != 1 ||
		es.Semantic != SemanticPartial || es.Provider != "fake" || es.Model != p.Model().Fingerprint() || es.Target != "" {
		t.Errorf("status %+v", es)
	}
	res := e.query("list", QueryOpts{})
	var bad []string
	for _, h := range res.Hits {
		if h.Path == "bad.md" {
			bad = append(bad, strings.Join(h.Via, "+"))
		}
	}
	if res.Semantic != SemanticPartial || !reflect.DeepEqual(bad, []string{ModeLexical}) {
		t.Errorf("%q: bad.md's hits came by %q, want one, lexical: %q", res.Semantic, bad, via(res))
	}
}

// ready2 waits until the document at path is semantic-ready.
func (e *env) ready2(path string) {
	e.t.Helper()
	e.eventually(path+" semantic-ready", func() bool {
		var ready int
		_ = scanOne(context.Background(), e.store.r, &ready, "SELECT semantic_ready FROM document WHERE path = ?", path)
		return ready == 1
	})
}

// TestSemanticRanksByCosine: vectors are normalized, so a short note about the query outranks a
// long one that repeats it among much else.
func TestSemanticRanksByCosine(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	e.put("a.md", "zebra zebra hippo lion tiger bear wolf fox owl crow hawk eagle\n", "b.md", "zebra\n")
	e.ready()
	eq(t, "order", via(e.query("zebra", QueryOpts{Mode: ModeSemantic})), []string{"b.md semantic", "a.md semantic"})
}

// TestEveryCommitPublishes: after every writer transaction, the one after a GC and after a delete
// that made no vector included, the code snapshot is the one at commit_seq, so queries do not fall
// back to scanning SQL.
func TestEveryCommitPublishes(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	atHead := func(what string) {
		t.Helper()
		e.eventually("the snapshot at the head "+what, func() bool {
			var seq int64
			_ = scanOne(context.Background(), e.store.r, &seq, "SELECT CAST(v AS INTEGER) FROM meta WHERE k = 'commit_seq'")
			s := e.ix.sem.snap.Load()
			return s != nil && s.watermark == seq
		})
	}
	e.put("a.md", "zebra\n\n# X\n\nold words\n", "b.md", "lion\n")
	e.ready()
	atHead("after indexing")
	// a.md loses a section: its other chunk keeps its vector, so no vector commit follows the GC
	e.put("a.md", "zebra\n")
	e.eventually("the GC", func() bool {
		var dead int
		_ = scanOne(context.Background(), e.store.r, &dead, "SELECT COUNT(*) FROM chunk WHERE gen_to IS NOT NULL")
		return dead == 0
	})
	atHead("after the GC")
	e.remove("b.md")
	atHead("after a delete")
	falls := e.ix.sem.fallbackScans.Load()
	e.query("zebra", QueryOpts{Mode: ModeSemantic})
	if e.ix.sem.fallbackScans.Load() != falls {
		t.Error("a query at rest scanned SQL")
	}
}

func (e *env) embStatus() EmbeddingStatus {
	e.t.Helper()
	st, err := e.ix.Status(context.Background())
	if err != nil || st.Embeddings == nil {
		e.t.Fatalf("status: %+v, %v", st, err)
	}
	return *st.Embeddings
}

// TestLateOldModelBatchKeepsTheFlip: after the target has become active, a batch the old model's
// worker was still embedding is stored, and changes nothing about which model is active, though the
// old model covers every chunk.
func TestLateOldModelBatchKeepsTheFlip(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n", "y.md", "hippo\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	e.stop()
	b := newFake("m2", "b")
	e.stop = nil
	e.open(Options{Provider: b, ProviderFor: func(embed.Model) (embed.Provider, error) { return a, nil }})
	fpB := b.Model().Fingerprint()
	e.eventually("the flip to b", func() bool { return e.activeModel() == fpB })
	for _, vb := range []vecBatch{{fp: fpA}, {fp: fpA, items: []vecItem{{textHash: []byte("late"), vec: make([]float32, 64)}}}} {
		if err := e.ix.handVectors(context.Background(), vb); err != nil {
			t.Fatal(err)
		}
		if got := e.activeModel(); got != fpB || e.ix.sem.active() != fpB || e.ix.sem.snap.Load().fp != fpB {
			t.Fatalf("a late batch of the old model (%d vectors) made %s active (writer %s, snapshot %s)",
				len(vb.items), got, e.ix.sem.active(), e.ix.sem.snap.Load().fp)
		}
	}
	var stored int
	_ = scanOne(context.Background(), e.store.r, &stored, "SELECT COUNT(*) FROM embedding WHERE model_fp = ? AND text_hash = ?", fpA, []byte("late"))
	if stored != 1 {
		t.Error("the late batch's vector was not stored")
	}
}

// TestRefusedTargetTextShowsInStatus: a target text the provider rejects holds the switch back, and
// the status says so, though the active model has nothing pending.
func TestRefusedTargetTextShowsInStatus(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n", "y.md", "oversized hippo\n")
	e.ready()
	e.stop()
	b := newFake("m2", "b")
	b.refuse = "oversized"
	e.stop = nil
	e.open(Options{Provider: b, ProviderFor: func(embed.Model) (embed.Provider, error) { return a, nil }})
	e.eventually("the refusal in the status", func() bool {
		es := e.embStatus()
		return es.TargetRefused == 1 && es.TargetPending == 1
	})
	es := e.embStatus()
	if es.Model != a.Model().Fingerprint() || es.Target != b.Model().Fingerprint() || es.Pending != 0 || es.Refused != 0 {
		t.Errorf("status %+v", es)
	}
}
