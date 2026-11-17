package search

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
)

// BenchmarkLMShapeTenants measures a search across ten tenants, as a catalogue's global search is:
// AUTODOC_BENCH_DOCS documents (default 56,000) split over ten tenants (t0 to t9) of the
// LM-shape corpus, each scoped to its largest label, hybrid, with the query vector given by a stub.
// The cases: the ten single searches one after another (seq10), the ten at once (par10), one
// search across the ten (global10), and eight such requests at once (global10_par8, a Global of
// eight slots). Each reports ns/op and the p50, p95 and p99 of its operations in milliseconds.
func BenchmarkLMShapeTenants(b *testing.B) {
	ctx := context.Background()
	n := lmEnvInt("AUTODOC_BENCH_DOCS", 56000)
	nl := lmEnvInt("AUTODOC_BENCH_LABELS", 200)
	const tenants = 10
	c := newLMCorpus(n/tenants, nl)
	large := 0
	for k := range c.byLabel {
		if len(c.byLabel[k]) > len(c.byLabel[large]) {
			large = k
		}
	}
	dsn := pgtest.ScratchURL(b)
	conn, err := postgres.OpenNamed(ctx, mustDB(b, dsn), dsn)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { conn.Close() })
	if err := pgstore.Migrate(ctx, conn); err != nil {
		b.Fatal(err)
	}
	t0 := time.Now()
	for i := range tenants {
		st, err := pgstore.New(conn, fmt.Sprintf("t%d", i))
		if err != nil {
			b.Fatal(err)
		}
		if err := st.SetModels(ctx, lmModel, ""); err != nil {
			b.Fatal(err)
		}
		if err := st.SetPolicy(ctx, pgstore.Policy{AccessFields: []string{"label_id"}, View: "track"}); err != nil {
			b.Fatal(err)
		}
		c.seed(ctx, b, st)
	}
	b.Logf("seeded %d tenants of %d documents in %s; label %s has %d documents in each",
		tenants, c.n, time.Since(t0).Round(time.Second), c.labels[large], len(c.byLabel[large]))
	if _, err := conn.ExecContext(ctx, "VACUUM (ANALYZE)"); err != nil {
		b.Fatal(err)
	}

	qs := c.queries(128, []int{large}, 12)
	vecs := map[string][]float32{}
	for _, q := range qs {
		vecs[q.text] = q.vec
	}
	qe := func(_ context.Context, q string) (string, []float32, error) {
		v, ok := vecs[q]
		if !ok {
			return "", nil, fmt.Errorf("the stub knows no query %q", q)
		}
		return lmModel, v, nil
	}
	scope := Scope{"label_id": {c.labels[large]}}
	ts := make([]TenantSearch, tenants)
	for i := range tenants {
		s, err := Open(ctx, conn, fmt.Sprintf("t%d", i), qe)
		if err != nil {
			b.Fatal(err)
		}
		ts[i] = TenantSearch{Tenant: fmt.Sprintf("t%d", i), Searcher: s, Scope: scope}
	}
	single := func(t TenantSearch, q string) {
		if _, err := t.Searcher.Search(ctx, t.Scope, Request{Query: q, Limit: 20}); err != nil {
			b.Error(err)
		}
	}
	g := NewGlobal(8)
	global := func(q string) {
		res, err := g.Search(ctx, GlobalRequest{Query: q, Limit: 20, Tenants: ts})
		if err != nil {
			b.Error(err)
		} else if len(res.Hits) == 0 {
			b.Errorf("no hits for %q", q)
		}
	}
	measure := func(b *testing.B, op func(q string)) {
		op(qs[0].text) // warm
		var durs []time.Duration
		i := 0
		for b.Loop() {
			t := time.Now()
			op(qs[i%len(qs)].text)
			durs = append(durs, time.Since(t))
			i++
		}
		reportLatency(b, durs)
	}
	b.Run("seq10", func(b *testing.B) {
		measure(b, func(q string) {
			for _, t := range ts {
				single(t, q)
			}
		})
	})
	b.Run("par10", func(b *testing.B) {
		measure(b, func(q string) {
			var wg sync.WaitGroup
			for _, t := range ts {
				wg.Add(1)
				go func() { defer wg.Done(); single(t, q) }()
			}
			wg.Wait()
		})
	})
	b.Run("global10", func(b *testing.B) { measure(b, global) })
	b.Run("global10_par8", func(b *testing.B) {
		global(qs[0].text)
		var mu sync.Mutex
		var durs []time.Duration
		var left atomic.Int64
		left.Store(int64(b.N))
		var wg sync.WaitGroup
		b.ResetTimer()
		for w := range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				i := w * 977
				var mine []time.Duration
				for left.Add(-1) >= 0 {
					t := time.Now()
					global(qs[i%len(qs)].text)
					mine = append(mine, time.Since(t))
					i++
				}
				mu.Lock()
				durs = append(durs, mine...)
				mu.Unlock()
			}()
		}
		wg.Wait()
		b.StopTimer()
		reportLatency(b, durs)
	})
}
