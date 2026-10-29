package index

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/vfs/memfs"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestTwoWorkspacesShareTheWriter measures what one store costs two workspaces indexing at once:
// how long each commit holds the one writer, and when each workspace finishes. It asserts only
// that both finish; the numbers are logged, for the design's measurement.
func TestTwoWorkspacesShareTheWriter(t *testing.T) {
	if testing.Short() {
		t.Skip("a measurement")
	}
	const notes = 300
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var mu sync.Mutex
	var commits []time.Duration
	record := func(d time.Duration) { mu.Lock(); commits = append(commits, d); mu.Unlock() }
	type ws struct {
		name string
		s    *Store
		ix   *Indexer
		done time.Duration
	}
	var all []*ws
	for _, name := range []string{"a", "b"} {
		fsys := memfs.New()
		for i := range notes {
			body := fmt.Sprintf("# Note %d\n\n%s\n\n## More\n\n%s\n", i, strings.Repeat("kestrel marsh plover ", 40), strings.Repeat("coast tide ", 60))
			if _, err := fsys.WriteFile(ctx, fmt.Sprintf("n%03d.md", i), strings.NewReader(body)); err != nil {
				t.Fatal(err)
			}
		}
		row, err := db.AddWorkspace(ctx, name, "/"+name, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		s := NewStore(db, row.ID)
		ix := newIndexer(s, fsys, options{Match: testMatch, onCommit: record})
		all = append(all, &ws{name: name, s: s, ix: ix})
	}
	start := time.Now()
	for _, w := range all {
		go func() { _ = w.ix.Run(ctx) }()
		for i := range notes {
			w.ix.Touch(fmt.Sprintf("n%03d.md", i))
		}
	}
	// The wait fails when indexing stops moving, not after a fixed total: 600 notes through one
	// writer under -race is CPU-bound work, several times slower on a loaded machine, and a fixed
	// 60 s was a race against it (a test machine at load ~10: "not both indexed in 60 s"). It fails when neither
	// workspace's pending jobs, documents or chunks change for indexStall, and in any case at a cap
	// inside go test's own -timeout, so the named failure comes before the runner's panic.
	capped := start.Add(4 * time.Minute)
	if dl, ok := t.Deadline(); ok {
		capped = dl.Add(-30 * time.Second)
	}
	var last [6]int64
	stalled := time.Now().Add(indexStall)
	for ; ; time.Sleep(5 * time.Millisecond) {
		left := 0
		var now [6]int64
		for i, w := range all {
			st, err := w.s.Status(ctx)
			if err != nil {
				t.Fatalf("workspace %s: %v", w.name, err)
			}
			now[3*i], now[3*i+1], now[3*i+2] = st.PendingJobs, st.Docs, st.Chunks
			if w.done != 0 {
				continue
			}
			if st.Docs == notes && st.PendingJobs == 0 {
				w.done = time.Since(start)
			} else {
				left++
			}
		}
		if left == 0 {
			break
		}
		if now != last {
			last, stalled = now, time.Now().Add(indexStall)
		}
		switch {
		case time.Now().After(stalled):
			t.Fatalf("not both indexed: no progress in %s (pending, docs, chunks per workspace = %v)", indexStall, last)
		case time.Now().After(capped):
			t.Fatalf("not both indexed by %s, though still moving (pending, docs, chunks per workspace = %v)", time.Since(start).Round(time.Second), last)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Slice(commits, func(i, j int) bool { return commits[i] < commits[j] })
	pct := func(p float64) time.Duration { return commits[min(len(commits)-1, int(p*float64(len(commits))))] }
	t.Logf("two workspaces of %d notes each: %d commits holding the writer p50 %v, p90 %v, p99 %v, max %v; a finished at %v, b at %v",
		notes, len(commits), pct(0.5), pct(0.9), pct(0.99), commits[len(commits)-1], all[0].done.Round(time.Millisecond), all[1].done.Round(time.Millisecond))
}
