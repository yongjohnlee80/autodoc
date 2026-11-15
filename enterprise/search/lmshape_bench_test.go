package search

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"net/url"
	"os"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
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
	lmModel  = "bench|embed||1024"
	lmDims   = 1024
	lmTopics = 64
	lmSeed   = 20261114
)

// lmEnvInt reads a positive integer from the environment.
func lmEnvInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

// lmCorpus is the generated corpus: documents' labels and chunk counts, and the generators of their
// text and vectors, every one a pure function of the seed and the document's index, so a query can
// be made from a document without keeping the corpus.
type lmCorpus struct {
	n       int
	labels  []string // by rank: labels[0] is the largest
	label   []int    // document -> rank
	chunks  []int    // document -> chunk count
	byLabel [][]int  // rank -> documents
	vocab   []string
	cum     []float64 // the vocabulary's cumulative Zipf weights
	topics  [][]float32
}

func newLMCorpus(n, nlabels int) *lmCorpus {
	r := rand.New(rand.NewPCG(lmSeed, 1))
	c := &lmCorpus{n: n, label: make([]int, n), chunks: make([]int, n), byLabel: make([][]int, nlabels)}
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
	for range lmTopics {
		c.topics = append(c.topics, unit(gauss(r, lmDims)))
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

func (c *lmCorpus) word(r *rand.Rand) string {
	x := r.Float64() * c.cum[len(c.cum)-1]
	i, _ := slices.BinarySearch(c.cum, x)
	return c.vocab[min(i, len(c.vocab)-1)]
}

// text is chunk k of document d: a dozen to twenty words, ending in a token no other chunk has.
func (c *lmCorpus) text(d, k int) string {
	r := rand.New(rand.NewPCG(lmSeed+1, uint64(d)<<4|uint64(k)))
	var b strings.Builder
	for range 12 + r.IntN(9) {
		b.WriteString(c.word(r))
		b.WriteByte(' ')
	}
	fmt.Fprintf(&b, "ref%dx%d", d, k)
	return b.String()
}

func (c *lmCorpus) title(d int) string {
	r := rand.New(rand.NewPCG(lmSeed+2, uint64(d)))
	return c.word(r) + " " + c.word(r) + " " + c.word(r)
}

// vec is chunk k of document d's vector: its topic's direction and noise, normalised.
func (c *lmCorpus) vec(d, k int) []float32 {
	r := rand.New(rand.NewPCG(lmSeed+3, uint64(d)<<4|uint64(k)))
	t := c.topics[(c.label[d]*7+d)%lmTopics]
	v := gauss(r, lmDims)
	for i := range v {
		v[i] = t[i]*float32(math.Sqrt(lmDims))*0.8 + v[i]*0.6
	}
	return unit(v)
}

func (c *lmCorpus) document(d int) pgstore.Document {
	doc := pgstore.Document{
		Path:     fmt.Sprintf("track/%d.md", d),
		Title:    c.title(d),
		Tags:     []string{"track"},
		Facets:   map[string][]string{"label_id": {c.labels[c.label[d]]}},
		SourceID: strconv.Itoa(d),
	}
	for k := range c.chunks[d] {
		body := c.text(d, k)
		doc.Chunks = append(doc.Chunks, gsearch.Chunk{Ord: k, Breadcrumb: doc.Path, Body: body, ByteEnd: len(body)})
	}
	return doc
}

// seed writes the corpus through the store's own paths: Batch for the documents, Embed for the
// vectors, MarkReady for each document. It reports the chunks written.
func (c *lmCorpus) seed(ctx context.Context, b *testing.B, st *pgstore.Store) (chunks int) {
	const perBatch = 250
	var vecs []pgstore.Embedding
	flush := func() {
		if err := st.Embed(ctx, lmModel, vecs); err != nil {
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
			if ready, err := st.MarkReady(ctx, doc.Path, lmModel); err != nil || !ready {
				b.Fatalf("mark %s ready: %v, %v", doc.Path, ready, err)
			}
		}
		if (lo/perBatch)%40 == 0 {
			b.Logf("seeded %d / %d documents", hi, c.n)
		}
	}
	return chunks
}

// lmQuery is one query of a case: its words, its vector, and the scope it runs in.
type lmQuery struct {
	text string
	vec  []float32
}

// queries makes n queries from documents of the given label ranks (nil is every label): three words
// of a chunk, and that chunk's vector with noise, as a user's query lands near what it asks for.
func (c *lmCorpus) queries(n int, ranks []int, seed uint64) []lmQuery {
	r := rand.New(rand.NewPCG(lmSeed+4, seed))
	var out []lmQuery
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
		noise := gauss(r, lmDims)
		for j := range v {
			v[j] += noise[j] * 0.02
		}
		out = append(out, lmQuery{text: strings.Join(words[i:i+3], " "), vec: unit(v)})
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

// BenchmarkLMShape measures Searcher.Search over a labelled catalogue's shape: AUTODOC_BENCH_DOCS
// documents (default 56,000) of two to four chunks, 1,024-d vectors, AUTODOC_BENCH_LABELS labels
// (default 200) of Zipf-skewed sizes, all semantic-ready, in one tenant, with the query vector given
// by a stub embedder (no model server). Each case reports ns/op and the p50, p95 and p99 of its
// operations in milliseconds. The cases run first as the store answers exactly (no approximate
// search configured), then, once the model's HNSW index is built, through it. AUTODOC_BENCH_EXPLAIN=1
// ends with every case's plans in the server's log (auto_explain), as one operation each.
func BenchmarkLMShape(b *testing.B) {
	ctx := context.Background()
	n := lmEnvInt("AUTODOC_BENCH_DOCS", 56000)
	nl := lmEnvInt("AUTODOC_BENCH_LABELS", 200)
	c := newLMCorpus(n, nl)
	small, large := nl-1, 0
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
	if err := st.SetModels(ctx, lmModel, ""); err != nil {
		b.Fatal(err)
	}
	if err := st.SetPolicy(ctx, pgstore.Policy{AccessFields: []string{"label_id"}, View: "track"}); err != nil {
		b.Fatal(err)
	}
	t0 := time.Now()
	chunks := c.seed(ctx, b, st)
	seedTime := time.Since(t0)
	b.Logf("seeded %d documents, %d chunks, %d-d vectors in %s", n, chunks, lmDims, seedTime.Round(time.Second))
	if _, err := conn.ExecContext(ctx, "ANALYZE"); err != nil {
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
		return lmModel, v, nil
	}
	const pool = 128
	ranksOf := func(r ...int) []int { return r }
	allLabels := make(map[string][]string)
	allLabels["label_id"] = c.labels
	cases := []lmCase{
		{"a_hybrid_small_label", Scope{"label_id": {c.labels[small]}}, gsearch.ModeAuto, c.queries(pool, ranksOf(small), 1)},
		{"b_hybrid_large_label", Scope{"label_id": {c.labels[large]}}, gsearch.ModeAuto, c.queries(pool, ranksOf(large), 2)},
		{"c_hybrid_unscoped", Scope(allLabels), gsearch.ModeAuto, c.queries(pool, nil, 3)},
		{"d_semantic_small_label", Scope{"label_id": {c.labels[small]}}, gsearch.ModeSemantic, c.queries(pool, ranksOf(small), 4)},
		{"e_lexical_small_label", Scope{"label_id": {c.labels[small]}}, gsearch.ModeLexical, c.queries(pool, ranksOf(small), 5)},
	}
	for _, cs := range cases {
		for _, q := range cs.qs {
			vecs[q.text] = q.vec
		}
	}

	one := func(b *testing.B, s *Searcher, cs lmCase, i int) time.Duration {
		q := cs.qs[i%len(cs.qs)]
		t := time.Now()
		res, err := s.Search(ctx, cs.scope, Request{Query: q.text, Mode: cs.mode, Limit: 20})
		d := time.Since(t)
		if err != nil {
			b.Error(err)
			return d
		}
		if len(res.Hits) == 0 {
			b.Errorf("%s: no hits for %q", cs.name, q.text)
		}
		return d
	}
	runSerial := func(b *testing.B, s *Searcher, cs lmCase) {
		one(b, s, cs, 0) // warm: the plan and the cache
		durs := make([]time.Duration, 0, 1024)
		i := 0
		for b.Loop() {
			durs = append(durs, one(b, s, cs, i))
			i++
		}
		reportLatency(b, durs)
	}
	runParallel := func(b *testing.B, s *Searcher, cs lmCase, workers int) {
		one(b, s, cs, 0)
		gmp := runtime.GOMAXPROCS(min(runtime.GOMAXPROCS(0), workers))
		defer runtime.GOMAXPROCS(gmp)
		b.SetParallelism((workers + runtime.GOMAXPROCS(0) - 1) / runtime.GOMAXPROCS(0))
		var pmu sync.Mutex
		var durs []time.Duration
		var next int
		b.ResetTimer()
		b.RunParallel(func(pb *testing.PB) {
			pmu.Lock()
			i := next
			next += 977
			pmu.Unlock()
			var mine []time.Duration
			for pb.Next() {
				mine = append(mine, one(b, s, cs, i))
				i++
			}
			pmu.Lock()
			durs = append(durs, mine...)
			pmu.Unlock()
		})
		b.StopTimer()
		reportLatency(b, durs)
	}

	phase := func(name string, s *Searcher, st *pgstore.Store) {
		mode, reason, err := st.ANNMode(ctx, lmModel)
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
	ann := pgstore.NewANN(conn, cfg, "lmbench")
	annSt, err := pgstore.New(conn, "t", pgstore.WithANN(ann))
	if err != nil {
		b.Fatal(err)
	}
	b.Logf("ANN threshold %d embeddings; the tenant has %d", cfg.Threshold, chunks)
	if err := ann.Want(ctx, lmModel); err != nil {
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

// lmCase is one measured case: a scope and a mode, with its queries.
type lmCase struct {
	name  string
	scope Scope
	mode  gsearch.Mode
	qs    []lmQuery
}

// explainPhase runs each case once more, exact then approximate, on fresh connections of a database
// that logs every statement's plan with its timings (auto_explain), a marker line in the log before
// each, so the plans can be read case by case.
func explainPhase(ctx context.Context, b *testing.B, dsn string, cases []lmCase) {
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
			opts = append(opts, WithANN(pgstore.NewANN(conn, pgstore.DefaultANNConfig(), "lmbench-explain")))
		}
		s, err := Open(ctx, conn, "t", func(_ context.Context, q string) (string, []float32, error) {
			for _, cs := range cases {
				for _, x := range cs.qs {
					if x.text == q {
						return lmModel, x.vec, nil
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
				if _, err := run.s.Search(ctx, cs.scope, Request{Query: cs.qs[i].text, Mode: cs.mode, Limit: 20}); err != nil {
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
