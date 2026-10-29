package index

import (
	"context"
	"crypto/sha256"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestTheFillResumesWhereItsLastBatchStopped (AP-1): a batch reads on from the chunk the last one
// stopped at, not from the workspace's first chunk, so a fill reads each chunk about once. A text
// behind the cursor that loses its vector is not the next batch's, while texts past the cursor
// remain; and the fill still embeds it before it says it is done, by looking again from the start.
func TestTheFillResumesWhereItsLastBatchStopped(t *testing.T) {
	p := newFake("m", "a")
	e := newEnv(t, options{Provider: p, ExternalEmbedding: true})
	var files []string
	for i := range 3 * embedBatch {
		files = append(files, fmt.Sprintf("d%03d.md", i), fmt.Sprintf("# T%03d\n\nbody %03d\n", i, i))
	}
	e.put(files...)
	ctx := context.Background()
	fp := p.Model().Fingerprint()
	batch := func() []string {
		t.Helper()
		before := len(p.texts())
		if _, err := e.ix.EmbedBatch(ctx, 0); err != nil {
			t.Fatal(err)
		}
		return p.texts()[before:]
	}
	first := batch()
	if len(first) != embedBatch {
		t.Fatalf("the first batch embedded %d texts, want %d", len(first), embedBatch)
	}
	behind := first[0]
	h := sha256.Sum256([]byte(behind))
	e.exec(`DELETE FROM embedding WHERE model_fp = ? AND text_hash = ?`, fp, h[:])
	if second := batch(); slices.Contains(second, behind) {
		t.Fatalf("the second batch began again from the first chunk: it embedded %q, which lies behind the first batch's", behind)
	}
	for range 4 * embedBatch {
		if slices.Contains(p.texts()[len(first):], behind) {
			break
		}
		worked, err := e.ix.EmbedBatch(ctx, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !worked {
			break
		}
	}
	if !slices.Contains(p.texts()[len(first):], behind) {
		t.Fatalf("the fill finished without embedding %q again, a text behind its cursor with no vector", behind)
	}
	if n := e.vectorsFor(h[:], fp); n != 1 {
		t.Errorf("%q has %d vectors after the fill, want 1", behind, n)
	}
}

// TestTheFillsChecksProbeTheModelsVectorsByKey: the statements that find texts with no vector (the
// fill's batch, and the writer's check whether the target covers the workspace) are planned as a
// lookup of embedding by key for each chunk, never a scan of the embedding table (no ANALYZE
// statistics, as in every store).
func TestTheFillsChecksProbeTheModelsVectorsByKey(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "# A\n\nalpha\n", "b.md", "# B\n\nbeta\n")
	ctx := context.Background()
	for name, run := range map[string]func(*Store) error{
		"unembedded": func(s *Store) error {
			_, _, _, err := s.unembedded(ctx, "fp", nil, 0)
			return err
		},
		"pendingUnrefused": func(s *Store) error {
			return s.read(ctx, func(tx *store.Tx) error {
				_, err := s.pendingUnrefused(tx, "fp", nil)
				return err
			})
		},
	} {
		capture := &sqlCapture{}
		s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(capture)}
		if err := run(s); err != nil {
			t.Fatal(name, err)
		}
		if len(capture.stmts) == 0 {
			t.Fatalf("%s rendered no statement", name)
		}
		q := capture.stmts[0]
		rows, err := e.raw.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q.SQL, q.Args...)
		if err != nil {
			t.Fatal(name, err)
		}
		var plan []string
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		rows.Close()
		all := strings.Join(plan, "\n")
		if !strings.Contains(all, "SEARCH embedding USING") || strings.Contains(all, "SCAN embedding") {
			t.Errorf("%s: %s\n%s", name, q.SQL, all)
		}
	}
}
