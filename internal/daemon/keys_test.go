package daemon

import (
	"context"
	"testing"

	"github.com/yongjohnlee80/golib/dao/sqlite"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// unkeyed counts workspace ws's documents with no stored sort keys
// (docs/decisions/search.md#stored-sort-keys), through a raw connection to the store's file.
func unkeyed(t *testing.T, db *store.Store, ws int64) int {
	t.Helper()
	raw, err := sqlite.Open(context.Background(), "file:"+db.Path()+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	rows, err := raw.QueryContext(context.Background(), "SELECT count(*) FROM document WHERE workspace_id = ? AND k_upd_s IS NULL", ws)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n int
	if !rows.Next() {
		t.Fatal("no count")
	}
	if err := rows.Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// execRaw runs q on the store's file, as a build from before 000027 left it.
func execRaw(t *testing.T, db *store.Store, q string, args ...any) {
	t.Helper()
	raw, err := sqlite.OpenNamed(context.Background(), "keys:"+db.Path(), "file:"+db.Path()+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// TestTheStartSweepKeysTheDocuments (docs/decisions/search.md#stored-sort-keys): a store whose
// documents have no sort keys, as one indexed before 000027 has, gets them at the start sweep the
// daemon runs (app/serve.go: SweepModels): a served workspace's through its indexer, a stored one
// no indexer serves through the store. Without it, every index.documents call would list by the Go
// fallback for good.
func TestTheStartSweepKeysTheDocuments(t *testing.T) {
	ctx := context.Background()
	m, db := openWith(t, Options{})
	kbWith(t, m, map[string]string{"a.md": "# A\n", "b.md": "---\nupdated: 2026-05-06\n---\n# B\n"})
	indexed(t, m, "kb", 2)
	kb := mustID(t, m, "kb")
	unserved, err := db.AddWorkspace(ctx, "elsewhere", "/nowhere", nil, nil) // stored, not served: no indexer
	if err != nil {
		t.Fatal(err)
	}
	execRaw(t, db, "INSERT INTO document(workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (?, 'old.md', 'v', 1, 'i', 7)", unserved.ID)
	execRaw(t, db, "UPDATE document SET k_upd_s = NULL, k_upd_ns = NULL, k_indexed = NULL")
	if unkeyed(t, db, kb) != 2 || unkeyed(t, db, unserved.ID) != 1 {
		t.Fatalf("the seed: %d and %d documents without keys", unkeyed(t, db, kb), unkeyed(t, db, unserved.ID))
	}
	m.SweepModels(ctx)
	if n, u := unkeyed(t, db, kb), unkeyed(t, db, unserved.ID); n != 0 || u != 0 {
		t.Errorf("after the start sweep: the served workspace has %d documents without keys, the unserved one %d", n, u)
	}
}
