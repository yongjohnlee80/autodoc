package search

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	gsearch "github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// The shape of the corpus a labelled catalogue has, as the enterprise server meets it: about 56,000
// documents of two to four short chunks, 1,024-dimension vectors under one model, spread over about
// 200 access labels with a skewed (Zipf-like) share each. All of it is generated, from a fixed seed.
const (
	catalogModel  = "bench|embed||1024"
	catalogDims   = 1024
	catalogTopics = 64
	catalogSeed   = 20261114
)

// catalogEnvInt reads a positive integer from the environment.
func catalogEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// catalogCorpus is the generated corpus: documents' labels and chunk counts, and the generators of their
// text and vectors, every one a pure function of the seed and the document's index, so a query can
// be made from a document without keeping the corpus.
type catalogCorpus struct {
	n      int
	facets bool // AUTODOC_BENCH_FACETS: each document has the declared facets catalogFacets

	labels  []string // by rank: labels[0] is the largest
	label   []int    // document -> rank
	chunks  []int    // document -> chunk count
	byLabel [][]int  // rank -> documents
	vocab   []string
	cum     []float64 // the vocabulary's cumulative Zipf weights
	topics  [][]float32
}

func newLMCorpus(n, nlabels int) *catalogCorpus {
	r := rand.New(rand.NewPCG(catalogSeed, 1))
	c := &catalogCorpus{n: n, label: make([]int, n), chunks: make([]int, n), byLabel: make([][]int, nlabels)}
	for i := range nlabels {
		c.labels = append(c.labels, fmt.Sprintf("L%03d", i))
	}
	w := make([]float64, nlabels)
	var sum float64
	for i := range w {
		w[i] = 1 / float64(i+1)
		sum += w[i]
	}
	for d := range n {
		x := r.Float64() * sum
		k := 0
		for ; k < nlabels-1; k++ {
			if x < w[k] {
				break
			}
			x -= w[k]
		}
		c.label[d] = k
		c.byLabel[k] = append(c.byLabel[k], d)
		c.chunks[d] = 2 + r.IntN(3)
	}
	syl := []string{"ka", "lo", "mi", "ra", "su", "ten", "vo", "dal", "pen", "qui", "bor", "han", "zel", "tu", "nor", "fim"}
	seen := map[string]bool{}
	for len(c.vocab) < 4000 {
		var b strings.Builder
		for range 2 + r.IntN(3) {
			b.WriteString(syl[r.IntN(len(syl))])
		}
		if !seen[b.String()] {
			seen[b.String()] = true
			c.vocab = append(c.vocab, b.String())
		}
	}
	var acc float64
	for i := range c.vocab {
		acc += 1 / float64(i+8)
		c.cum = append(c.cum, acc)
	}
	for range catalogTopics {
		c.topics = append(c.topics, unit(gauss(r, catalogDims)))
	}
	return c
}

func gauss(r *rand.Rand, n int) []float32 {
	v := make([]float32, n)
	for i := range v {
		v[i] = float32(r.NormFloat64())
	}
	return v
}

// unit is v normalised to length one.
func unit(v []float32) []float32 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	s = math.Sqrt(s)
	for i := range v {
		v[i] = float32(float64(v[i]) / s)
	}
	return v
}

func (c *catalogCorpus) word(r *rand.Rand) string {
	x := r.Float64() * c.cum[len(c.cum)-1]
	i, _ := slices.BinarySearch(c.cum, x)
	return c.vocab[min(i, len(c.vocab)-1)]
}

// text is chunk k of document d: a dozen to twenty words, ending in a token no other chunk has.
func (c *catalogCorpus) text(d, k int) string {
	r := rand.New(rand.NewPCG(catalogSeed+1, uint64(d)<<4|uint64(k)))
	var b strings.Builder
	for range 12 + r.IntN(9) {
		b.WriteString(c.word(r))
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "ref%dx%d", d, k)
	return b.String()
}

func (c *catalogCorpus) title(d int) string {
	r := rand.New(rand.NewPCG(catalogSeed+2, uint64(d)))
	return c.word(r) + " " + c.word(r) + " " + c.word(r)
}

// vec is chunk k of document d's vector: its topic's direction and noise, normalised.
func (c *catalogCorpus) vec(d, k int) []float32 {
	r := rand.New(rand.NewPCG(catalogSeed+3, uint64(d)<<4|uint64(k)))
	t := c.topics[(c.label[d]*7+d)%catalogTopics]
	v := gauss(r, catalogDims)
	for i := range v {
		v[i] = t[i]*float32(math.Sqrt(catalogDims))*0.8 + v[i]*0.6
	}
	return unit(v)
}

func (c *catalogCorpus) document(d int) pgstore.Document {
	doc := pgstore.Document{
		Path:     fmt.Sprintf("track/%d.md", d),
		Title:    c.title(d),
		Tags:     []string{"track"},
		Facets:   map[string][]string{"label_id": {c.labels[c.label[d]]}},
		SourceID: strconv.Itoa(d),
	}
	if c.facets {
		c.facetsOf(d, &doc)
	}
	for k := range c.chunks[d] {
		body := c.text(d, k)
		doc.Chunks = append(doc.Chunks, gsearch.Chunk{Ord: k, Breadcrumb: doc.Path, Body: body, ByteEnd: len(body)})
	}
	return doc
}

// catalogFacets is the declaration of a corpus with facets: a release's status and territories, its
// release date and its tempo, as a music catalog filters them.
var catalogFacets = map[string]string{"status": pgstore.FacetKeyword, "territory": pgstore.FacetKeyword,
	"released": pgstore.FacetDate, "bpm": pgstore.FacetNumber}

// catalogTerritories are the territories a document may be released in.
var catalogTerritories = []string{"US", "CA", "GB", "DE", "JP", "BR"}

// facetsOf gives document d its facets, a pure function of d: status released (70%), scheduled
// (20%) or draft (10%); one to three territories; a release date uniform over 2016 to 2025; a
// tempo uniform from 60 to 180 bpm, whole.
func (c *catalogCorpus) facetsOf(d int, doc *pgstore.Document) {
	r := rand.New(rand.NewPCG(catalogSeed+5, uint64(d)))
	status := "released"
	switch x := r.IntN(10); {
	case x == 9:
		status = "draft"
	case x >= 7:
		status = "scheduled"
	}
	doc.Facets["status"] = []string{status}
	ts := slices.Clone(catalogTerritories)
	r.Shuffle(len(ts), func(i, j int) { ts[i], ts[j] = ts[j], ts[i] })
	doc.Facets["territory"] = ts[:1+r.IntN(3)]
	from := time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	to := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Unix()
	doc.Values = map[string][]float64{
		"released": {float64(from + r.Int64N(to-from))},
		"bpm":      {float64(60 + r.IntN(121))},
	}
}

// seed writes the corpus through the store's own paths: Batch for the documents, Embed for the
// vectors, MarkReady for each document. It reports the chunks written.
func (c *catalogCorpus) seed(ctx context.Context, b *testing.B, st *pgstore.Store) (chunks int) {
	const perBatch = 250
	var vecs []pgstore.Embedding
	flush := func() {
		if err := st.Embed(ctx, catalogModel, vecs); err != nil {
			b.Fatal(err)
		}
		vecs = vecs[:0]
	}
	for lo := 0; lo < c.n; lo += perBatch {
		hi := min(lo+perBatch, c.n)
		tx := st.Begin(ctx)
		docs := make([]pgstore.Document, 0, hi-lo)
		for d := lo; d < hi; d++ {
			doc := c.document(d)
			if _, err := tx.Put(ctx, doc); err != nil {
				tx.Rollback()
				b.Fatal(err)
			}
			docs = append(docs, doc)
		}
		if err := tx.Commit(); err != nil {
			b.Fatal(err)
		}
		for i, doc := range docs {
			for k, ch := range doc.Chunks {
				vecs = append(vecs, pgstore.Embedding{TextHash: ch.TextHash(), Vec: c.vec(lo+i, k)})
				chunks++
			}
		}
		flush()
		for _, doc := range docs {
			if ready, err := st.MarkReady(ctx, doc.Path, catalogModel); err != nil || !ready {
				b.Fatalf("mark %s ready: %v, %v", doc.Path, ready, err)
			}
		}
		if (lo/perBatch)%40 == 0 {
			b.Logf("seeded %d / %d documents", hi, c.n)
		}
	}
	return chunks
}

// catalogQuery is one query of a case: its words, its vector, and the scope it runs in.
type catalogQuery struct {
	text string
	vec  []float32
}

// queries makes n queries from documents of the given label ranks (nil is every label): three words
// of a chunk, and that chunk's vector with noise, as a user's query lands near what it asks for.
func (c *catalogCorpus) queries(n int, ranks []int, seed uint64) []catalogQuery {
	r := rand.New(rand.NewPCG(catalogSeed+4, seed))
	var out []catalogQuery
	for len(out) < n {
		var d int
		if ranks == nil {
			d = r.IntN(c.n)
		} else {
			docs := c.byLabel[ranks[r.IntN(len(ranks))]]
			d = docs[r.IntN(len(docs))]
		}
		k := r.IntN(c.chunks[d])
		words := strings.Fields(c.text(d, k))
		i := r.IntN(len(words) - 4)
		v := c.vec(d, k)
		noise := gauss(r, catalogDims)
		for j := range v {
			v[j] += noise[j] * 0.02
		}
		out = append(out, catalogQuery{text: strings.Join(words[i:i+3], " "), vec: unit(v)})
	}
	return out
}

func pct(sorted []time.Duration, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	i := min(int(math.Ceil(p*float64(len(sorted))))-1, len(sorted)-1)
	return float64(sorted[max(i, 0)]) / float64(time.Millisecond)
}

func reportLatency(b *testing.B, durs []time.Duration) {
	slices.Sort(durs)
	b.ReportMetric(pct(durs, 0.50), "p50-ms")
	b.ReportMetric(pct(durs, 0.95), "p95-ms")
	b.ReportMetric(pct(durs, 0.99), "p99-ms")
}

// BenchmarkCatalogShape measures Searcher.Search over a labelled catalogue's shape: AUTODOC_BENCH_DOCS
// documents (default 56,000) of two to four chunks, 1,024-d vectors, AUTODOC_BENCH_LABELS labels
// (default 200) of Zipf-skewed sizes, all semantic-ready, in one tenant, with the query vector given
// by a stub embedder (no model server). Each case reports ns/op and the p50, p95 and p99 of its
// operations in milliseconds. The cases run first as the store answers exactly (no approximate
// search configured), then, once the model's HNSW index is built, through it. AUTODOC_BENCH_EXPLAIN=1
// ends with every case's plans in the server's log (auto_explain), as one operation each.
func BenchmarkCatalogShape(b *testing.B) {
	ctx := context.Background()
	n := catalogEnvInt("AUTODOC_BENCH_DOCS", 56000)
	nl := catalogEnvInt("AUTODOC_BENCH_LABELS", 200)
	c := newLMCorpus(n, nl)
	c.facets = os.Getenv("AUTODOC_BENCH_FACETS") == "1"
	// the cases' labels are the largest and the smallest that hold a document: with few documents
	// over many labels, the Zipf tail is empty, and a case on an empty label would have no query
	small, large := -1, -1
	for k := range c.byLabel {
		if len(c.byLabel[k]) == 0 {
			continue
		}
		if large < 0 || len(c.byLabel[k]) > len(c.byLabel[large]) {
			large = k
		}
		// <=: of labels of one size the last is the small one and the first the large one, so two
		// labels that hold as many documents as each other are still two labels
		if small < 0 || len(c.byLabel[k]) <= len(c.byLabel[small]) {
			small = k
		}
	}
	if small < 0 || small == large {
		b.Fatalf("AUTODOC_BENCH_DOCS=%d over AUTODOC_BENCH_LABELS=%d leaves fewer than two labels with documents: give it more documents or fewer labels", n, nl)
	}
	b.Logf("corpus: %d documents, %d labels; label %s has %d documents (largest), %s has %d (smallest)",
		n, nl, c.labels[large], len(c.byLabel[large]), c.labels[small], len(c.byLabel[small]))

	dsn := pgtest.ScratchURL(b)
	conn, err := postgres.OpenNamed(ctx, mustDB(b, dsn), dsn)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { conn.Close() })
	if err := pgstore.Migrate(ctx, conn); err != nil {
		b.Fatal(err)
	}
	st, err := pgstore.New(conn, "t")
	if err != nil {
		b.Fatal(err)
	}
	if err := st.SetModels(ctx, catalogModel, ""); err != nil {
		b.Fatal(err)
	}
	policy := pgstore.Policy{AccessFields: []string{"label_id"}, View: "track"}
	if c.facets {
		policy.Facets = catalogFacets
	}
	if err := st.SetPolicy(ctx, policy); err != nil {
		b.Fatal(err)
	}
	t0 := time.Now()
	chunks := c.seed(ctx, b, st)
	seedTime := time.Since(t0)
	b.Logf("seeded %d documents, %d chunks, %d-d vectors in %s", n, chunks, catalogDims, seedTime.Round(time.Second))
	// VACUUM, not ANALYZE alone: the steady state a server reaches once autovacuum has run. Right
	// after a bulk load the GIN indexes still hold their pending lists, which every scoped search
	// then reads whole (a small label's hybrid search took 11.7 ms, against 5.8 after a VACUUM).
	if _, err := conn.ExecContext(ctx, "VACUUM (ANALYZE)"); err != nil {
		b.Fatal(err)
	}

	// the stub embedder: the vector made for the query's words
	var mu sync.Mutex
	vecs := map[string][]float32{}
	qe := func(_ context.Context, q string) (string, []float32, error) {
		mu.Lock()
		defer mu.Unlock()
		v, ok := vecs[q]
		if !ok {
			return "", nil, fmt.Errorf("the stub knows no query %q", q)
		}
		return catalogModel, v, nil
	}
	const pool = 128
	ranksOf := func(r ...int) []int { return r }
	allLabels := make(map[string][]string)
	allLabels["label_id"] = c.labels
	cases := []catalogCase{
		{name: "a_hybrid_small_label", scope: Scope{"label_id": {c.labels[small]}}, mode: gsearch.ModeAuto, qs: c.queries(pool, ranksOf(small), 1)},
		{name: "b_hybrid_large_label", scope: Scope{"label_id": {c.labels[large]}}, mode: gsearch.ModeAuto, qs: c.queries(pool, ranksOf(large), 2)},
		{name: "c_hybrid_unscoped", scope: Scope(allLabels), mode: gsearch.ModeAuto, qs: c.queries(pool, nil, 3)},
		{name: "d_semantic_small_label", scope: Scope{"label_id": {c.labels[small]}}, mode: gsearch.ModeSemantic, qs: c.queries(pool, ranksOf(small), 4)},
		{name: "e_lexical_small_label", scope: Scope{"label_id": {c.labels[small]}}, mode: gsearch.ModeLexical, qs: c.queries(pool, ranksOf(small), 5)},
	}
	if c.facets {
		// typed facets (AUTODOC_BENCH_FACETS=1): each filter against the same queries unfiltered
		inLarge, all := Scope{"label_id": {c.labels[large]}}, Scope(allLabels)
		released := gsearch.Filter{Facets: map[string][]string{"status": {"released"}}}
		combined := gsearch.Filter{Facets: map[string][]string{"status": {"released"}, "territory": {"US"}}}
		year := map[string]Range{"released": {Gte: "2024-01-01", Lt: "2025-01-01"}}
		cases = append(cases,
			catalogCase{name: "f_hybrid_large_status", scope: inLarge, mode: gsearch.ModeAuto, qs: c.queries(pool, ranksOf(large), 6), filter: released},
			catalogCase{name: "g_hybrid_large_year", scope: inLarge, mode: gsearch.ModeAuto, qs: c.queries(pool, ranksOf(large), 7), ranges: year},
			catalogCase{name: "h_hybrid_large_combined", scope: inLarge, mode: gsearch.ModeAuto, qs: c.queries(pool, ranksOf(large), 8), filter: combined,
				ranges: map[string]Range{"released": {Gte: "2020-01-01", Lt: "2025-01-01"}, "bpm": {Gte: "118", Lte: "126"}}},
			catalogCase{name: "i_hybrid_unscoped_bpm_narrow", scope: all, mode: gsearch.ModeAuto, qs: c.queries(pool, nil, 9),
				ranges: map[string]Range{"bpm": {Gte: "120", Lt: "121"}}},
			catalogCase{name: "j_hybrid_unscoped_bpm_wide", scope: all, mode: gsearch.ModeAuto, qs: c.queries(pool, nil, 10),
				ranges: map[string]Range{"bpm": {Gte: "70"}}},
			catalogCase{name: "k_lexical_large_combined", scope: inLarge, mode: gsearch.ModeLexical, qs: c.queries(pool, ranksOf(large), 11), filter: combined,
				ranges: map[string]Range{"released": {Gte: "2020-01-01"}}},
			catalogCase{name: "l_filters_alone_large_combined", scope: inLarge, mode: gsearch.ModeAuto, qs: []catalogQuery{{}}, filter: combined,
				ranges: map[string]Range{"released": {Gte: "2024-01-01", Lt: "2025-01-01"}}},
		)
	}
	for _, cs := range cases {
		for _, q := range cs.qs {
			vecs[q.text] = q.vec
		}
	}

	one := func(b *testing.B, s *Searcher, cs catalogCase, i int) time.Duration {
		q := cs.qs[i%len(cs.qs)]
		t := time.Now()
		res, err := s.Search(ctx, cs.scope, Request{Query: q.text, Mode: cs.mode, Limit: 20, Filter: cs.filter, Ranges: cs.ranges})
		d := time.Since(t)
		if err != nil {
			b.Error(err)
			return d
		}
		// a filtered case's words may come from a document its filter leaves out
		if len(res.Hits) == 0 && cs.filter.Facets == nil && cs.ranges == nil {
			b.Errorf("%s: no hits for %q", cs.name, q.text)
		}
		return d
	}
	runSerial := func(b *testing.B, s *Searcher, cs catalogCase) {
		one(b, s, cs, 0) // warm: the plan and the cache
		durs := make([]time.Duration, 0, 1024)
		i := 0
		for b.Loop() {
			durs = append(durs, one(b, s, cs, i))
			i++
		}
		reportLatency(b, durs)
	}
	runParallel := func(b *testing.B, s *Searcher, cs catalogCase, workers int) {
		one(b, s, cs, 0)
		// exactly workers searchers, whatever GOMAXPROCS is: RunParallel starts a multiple of
		// GOMAXPROCS goroutines, so 8 asked on 3 CPUs would be 9. They share b.N operations.
		var pmu sync.Mutex
		var durs []time.Duration
		var left atomic.Int64
		left.Store(int64(b.N))
		var wg sync.WaitGroup
		b.ResetTimer()
		for w := range workers {
			wg.Add(1)
			go func() {
				defer wg.Done()
				i := w * 977
				var mine []time.Duration
				for left.Add(-1) >= 0 {
					mine = append(mine, one(b, s, cs, i))
					i++
				}
				pmu.Lock()
				durs = append(durs, mine...)
				pmu.Unlock()
			}()
		}
		wg.Wait()
		b.StopTimer()
		reportLatency(b, durs)
	}

	phase := func(name string, s *Searcher, st *pgstore.Store) {
		mode, reason, err := st.ANNMode(ctx, catalogModel)
		if err != nil {
			b.Fatal(err)
		}
		b.Logf("%s: the store's plan for the model: %s %s", name, mode, reason)
		b.Run(name, func(b *testing.B) {
			for _, cs := range cases {
				b.Run(cs.name, func(b *testing.B) { runSerial(b, s, cs) })
			}
			b.Run("par8_"+cases[0].name, func(b *testing.B) { runParallel(b, s, cases[0], 8) })
			b.Run("par8_"+cases[1].name, func(b *testing.B) { runParallel(b, s, cases[1], 8) })
		})
	}

	exact, err := Open(ctx, conn, "t", qe)
	if err != nil {
		b.Fatal(err)
	}
	phase("exact", exact, st)

	// the approximate search, at its default settings: the model's index built, then served
	cfg := pgstore.DefaultANNConfig()
	ann := pgstore.NewANN(conn, cfg, "catalogbench")
	annSt, err := pgstore.New(conn, "t", pgstore.WithANN(ann))
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("ANN threshold %d embeddings; the tenant has %d", cfg.Threshold, chunks)
	if err := ann.Want(ctx, catalogModel); err != nil {
		b.Fatal(err)
	}
	t0 = time.Now()
	if err := ann.Tick(ctx); err != nil {
		b.Fatal(err)
	}
	b.Logf("HNSW index built in %s", time.Since(t0).Round(time.Second))
	sts, _ := ann.Status(ctx)
	b.Logf("ANN status: %+v", sts)
	if _, err := conn.ExecContext(ctx, "ANALYZE"); err != nil {
		b.Fatal(err)
	}
	approx, err := Open(ctx, conn, "t", qe, WithANN(ann))
	if err != nil {
		b.Fatal(err)
	}
	phase("ann", approx, annSt)
	if sts, err = ann.Status(ctx); err == nil {
		b.Logf("ANN status after the cases (underfill counts searches that fell back to exact): %+v", sts)
	}

	if os.Getenv("AUTODOC_BENCH_EXPLAIN") == "1" {
		explainPhase(ctx, b, dsn, cases)
	}
}

// catalogCase is one measured case: a scope and a mode, with its queries, and its filter and ranges
// (typed facets) when it has them.
type catalogCase struct {
	name   string
	scope  Scope
	mode   gsearch.Mode
	qs     []catalogQuery
	filter gsearch.Filter
	ranges map[string]Range
}

// explainPhase runs each case once more, exact then approximate, on fresh connections of a database
// that logs every statement's plan with its timings (auto_explain), a marker line in the log before
// each, so the plans can be read case by case.
func explainPhase(ctx context.Context, b *testing.B, dsn string, cases []catalogCase) {
	admin, err := postgres.Open(ctx, dsn)
	if err != nil {
		b.Fatal(err)
	}
	defer admin.Close()
	db := mustDB(b, dsn)
	for _, q := range []string{
		"ALTER DATABASE " + db + " SET session_preload_libraries = 'auto_explain'",
		"ALTER DATABASE " + db + " SET auto_explain.log_min_duration = 0",
		"ALTER DATABASE " + db + " SET auto_explain.log_analyze = on",
		"ALTER DATABASE " + db + " SET auto_explain.log_buffers = on",
		"ALTER DATABASE " + db + " SET auto_explain.log_nested_statements = on",
	} {
		if _, err := admin.ExecContext(ctx, q); err != nil {
			b.Fatal(err)
		}
	}
	// the Searchers' pools predate the setting: only their new connections log, so each runs on a
	// pool of its own, opened now
	fresh := func(approximate bool) *Searcher {
		conn, err := postgres.OpenNamed(ctx, db, dsn)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { conn.Close() })
		var opts []Option
		if approximate {
			opts = append(opts, WithANN(pgstore.NewANN(conn, pgstore.DefaultANNConfig(), "catalogbench-explain")))
		}
		s, err := Open(ctx, conn, "t", func(_ context.Context, q string) (string, []float32, error) {
			for _, cs := range cases {
				for _, x := range cs.qs {
					if x.text == q {
						return catalogModel, x.vec, nil
					}
				}
			}
			return "", nil, fmt.Errorf("unknown query %q", q)
		}, opts...)
		if err != nil {
			b.Fatal(err)
		}
		return s
	}
	for _, run := range []struct {
		name string
		s    *Searcher
	}{{"exact", fresh(false)}, {"ann", fresh(true)}} {
		for _, cs := range cases {
			mark := "MARK " + run.name + "/" + cs.name
			if _, err := admin.ExecContext(ctx, "DO $$ BEGIN RAISE LOG '"+mark+"'; END $$"); err != nil {
				b.Fatal(err)
			}
			for i := range 2 { // the second is the warm one
				t := time.Now()
				q := cs.qs[i%len(cs.qs)]
				if _, err := run.s.Search(ctx, cs.scope, Request{Query: q.text, Mode: cs.mode, Limit: 20, Filter: cs.filter, Ranges: cs.ranges}); err != nil {
					b.Fatal(err)
				}
				b.Logf("explain %s run %d: %s", mark, i, time.Since(t))
			}
		}
	}
}

var _ dao.DataConn

// mustDB is the database name a URL DSN names.
func mustDB(b testing.TB, dsn string) string {
	b.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		b.Fatal(err)
	}
	return u.Path[1:]
}
