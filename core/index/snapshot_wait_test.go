package index

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search/vector"
)

// TestAQueryRacingThePublishWaitsForIt (AP-4): a query that read a commit before the writer
// published its snapshot finds the current one a commit behind; it waits for that publish and
// uses it, rather than loading every code from SQL. With no publish coming it gives up after
// publishWait; another model's or a newer snapshot is never waited for.
func TestAQueryRacingThePublishWaitsForIt(t *testing.T) {
	m := &semantic{}
	behind := vector.NewIndex[int64, chunkKey]("fp", 4, nil)
	m.snap.Store(behind)
	got := make(chan *codeIndex, 1)
	go func() { got <- m.snapshotAt("fp", 5) }()
	time.Sleep(20 * time.Millisecond) // the query is waiting
	head := behind.Next(5, nil, nil)
	m.snap.Store(head)
	m.published()
	select {
	case snap := <-got:
		if snap != head {
			t.Fatalf("the waiting query got %v, want the snapshot published at its watermark", snap)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting query never heard the publish")
	}
	m.snap.Store(behind)
	start := time.Now()
	if snap := m.snapshotAt("fp", 5); snap != nil || time.Since(start) < publishWait {
		t.Fatalf("no publish coming: %v after %v, want nil after %v", snap, time.Since(start), publishWait)
	}
	for name, snap := range map[string]*codeIndex{
		"another model's": vector.NewIndex[int64, chunkKey]("other", 4, nil),
		"a newer one":     vector.NewIndex[int64, chunkKey]("fp", 6, nil),
	} {
		m.snap.Store(snap)
		start := time.Now()
		if got := m.snapshotAt("fp", 5); got != nil || time.Since(start) > publishWait/2 {
			t.Errorf("%s snapshot: %v after %v, want nil at once", name, got, time.Since(start))
		}
	}
}

// TestAFilterNarrowsTheCodesBeforeOrdering (AP-4): a path filter admitting one document makes a
// semantic query order only that document's codes. It fetches once, and finds the document. Before,
// the codes of every document were ordered and fetched in windows of hammingTop, and since the
// admitted chunks are fewer than the query wants, every window of the whole order was fetched.
func TestAFilterNarrowsTheCodesBeforeOrdering(t *testing.T) {
	e := newEnv(t, options{Provider: newFake("m", "a"), BatchDelay: time.Millisecond, BatchSize: 200})
	var pc []string
	for i := range 500 {
		pc = append(pc, fmt.Sprintf("notes/%03d.md", i), fmt.Sprintf("# Note %d\n\nzebra giraffe hippo %d\n", i, i))
	}
	for i := 0; i < len(pc); i += 200 {
		e.put(pc[i:min(i+200, len(pc))]...)
	}
	e.ready()
	e.atHead()
	var fetches atomic.Int64
	e.ix.sem.beforeFetch = func() { fetches.Add(1) }
	defer func() { e.ix.sem.beforeFetch = nil }()
	hits := e.query("zebra giraffe", QueryOpts{Mode: ModeSemantic, Paths: []string{"notes/042.md"}})
	if fetches.Load() != 1 {
		t.Fatalf("%d fetches for a filter admitting one document, want 1: the codes were not narrowed first", fetches.Load())
	}
	if len(hits.Hits) == 0 || hits.Hits[0].Path != "notes/042.md" {
		t.Fatalf("the filtered query's hits: %+v, want notes/042.md", hits.Hits)
	}
}
