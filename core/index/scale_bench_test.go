package index

import (
	"context"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// BenchmarkSemanticScale measures the semantic path at scale (the 2026-10-28 audit's AP-3 and
// AP-4): AUTODOC_BENCH_DOCS documents (default 10000) of four sections each, every one
// semantic-ready, seeded (seedEnv).
//   - Unfiltered: a semantic query over the snapshot.
//   - OneFile: a query whose path filter admits one document, the most restrictive case: the
//     fetches (SQL round trips) it makes are reported as fetches/op.
//   - Publish: the writer's publish after a vector commit that changed 64 documents.
//   - Stale: a query whose snapshot is a commit behind, its commit published a millisecond into
//     it, as a query racing the writer sees: fallbacks/op is how often it scanned SQL instead.
func BenchmarkSemanticScale(b *testing.B) {
	n := 10000
	if v, err := strconv.Atoi(os.Getenv("AUTODOC_BENCH_DOCS")); err == nil && v > 0 {
		n = v
	}
	e := seedEnv(b, n)
	ctx := context.Background()
	m := e.ix.sem
	var fetches atomic.Int64
	m.beforeFetch = func() { fetches.Add(1) }
	b.Cleanup(func() { m.beforeFetch = nil })
	b.Run("Unfiltered", func(b *testing.B) {
		fetches.Store(0)
		for b.Loop() {
			if _, err := e.ix.Search(ctx, "storage vector snapshot", QueryOpts{Mode: ModeSemantic}); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(fetches.Load())/float64(b.N), "fetches/op")
	})
	b.Run("OneFile", func(b *testing.B) {
		fetches.Store(0)
		for b.Loop() {
			if _, err := e.ix.Search(ctx, "storage vector snapshot", QueryOpts{Mode: ModeSemantic, Paths: []string{"notes/0042.md"}}); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(fetches.Load())/float64(b.N), "fetches/op")
	})
	b.Run("Publish", func(b *testing.B) {
		changed := make([]int64, 64)
		for i := range changed {
			changed[i] = int64(1 + i*(n/64))
		}
		for b.Loop() {
			if err := e.ix.publish(ctx, changed); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("Stale", func(b *testing.B) {
		fallbacks := m.fallbackScans.Load()
		for b.Loop() {
			b.StopTimer()
			cur := m.snap.Load()
			m.snap.Store(cur.Next(cur.Watermark()-1, nil, nil)) // a snapshot one commit behind
			// the writer publishes the query's own commit a millisecond into it, as a query racing
			// the writer sees
			go func() { time.Sleep(time.Millisecond); m.snap.Store(cur); m.published() }()
			b.StartTimer()
			if _, err := e.ix.Search(ctx, "storage vector snapshot", QueryOpts{Mode: ModeSemantic}); err != nil {
				b.Fatal(err)
			}
		}
		b.ReportMetric(float64(m.fallbackScans.Load()-fallbacks)/float64(b.N), "fallbacks/op")
	})
}
