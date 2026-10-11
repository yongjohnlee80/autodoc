package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/todo"
	"github.com/yongjohnlee80/autodoc/core/todo/todotest"
)

// The SQLite journal behaves as the in-memory one does: the same suite runs on both.
func TestTheTodoJournalConforms(t *testing.T) {
	stores := map[todo.Journal]*Store{}
	todotest.RunJournalConformance(t, todotest.Harness{
		New: func(t *testing.T) todo.Journal {
			s := openStore(t)
			j := s.TodoJournal()
			stores[j] = s
			return j
		},
		Reopen: func(t *testing.T, j todo.Journal) todo.Journal {
			s := stores[j]
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err := Open(context.Background(), s.path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			j = s.TodoJournal()
			stores[j] = s
			return j
		},
		// a trigger refuses every write of a root record: the last table a transaction writes, so the
		// row, the snapshot and the store records are written before it fails
		Break: func(t *testing.T, j todo.Journal) func() {
			raw := stores[j].raw(t)
			exec := func(q string) {
				t.Helper()
				if _, err := raw.ExecContext(context.Background(), q); err != nil {
					t.Fatalf("%s: %v", q, err)
				}
			}
			for _, op := range []string{"INSERT", "UPDATE"} {
				exec("CREATE TRIGGER todo_root_" + strings.ToLower(op) + " BEFORE " + op + " ON todo_root BEGIN SELECT RAISE(ABORT, 'injected'); END")
			}
			return func() {
				exec("DROP TRIGGER todo_root_insert")
				exec("DROP TRIGGER todo_root_update")
			}
		},
	})
}

// Each method's storage failure is its result: a table that cannot be read or written fails the
// call, and a column that does not decode fails the read.
func TestTheTodoJournalSurfacesAStorageFailure(t *testing.T) {
	ctx := context.Background()
	when := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	s := openStore(t)
	j := s.TodoJournal()
	raw := s.raw(t)
	exec := func(q string) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	failed := func(what string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: no error", what)
		}
	}
	seq, err := j.Begin(ctx, todo.Change{Store: "A", Kind: todo.KindAdd, Op: todo.OpCreate})
	if err != nil {
		t.Fatal(err)
	}
	put := todo.Edit{Store: "A", Put: []todo.SnapshotRow{{Path: "open/x.md", ID: "x"}}}
	del := todo.Edit{Store: "A", Delete: []string{"open/x.md"}}
	repath := todo.Edit{Store: "A", Repath: &todo.Repath{From: "/a/.todo-list", To: "/b"}}
	update := func(fn func(*todo.Settings)) error {
		return j.UpdateSettings(ctx, func(s *todo.Settings) error { fn(s); return nil })
	}
	if err := update(func(s *todo.Settings) {
		s.Stores["A"] = todo.StoreRecord{ID: "A", Path: "/a/.todo-list"}
		s.Roots["/r"] = todo.RootRecord{Root: "/r"}
		s.Vars["V"] = "v"
	}); err != nil {
		t.Fatal(err)
	}

	// the change log cannot be read
	exec("ALTER TABLE todo_change RENAME COLUMN state TO state_gone")
	failed("Stage", j.Stage(ctx, seq))
	failed("Commit", j.Commit(ctx, seq, todo.Edit{}))
	failed("Drop", j.Drop(ctx, seq))
	_, err = j.Pending(ctx, "A")
	failed("Pending", err)
	_, err = j.Head(ctx, "A")
	failed("Head", err)
	_, err = j.After(ctx, "A", 0, 10)
	failed("After", err)
	_, err = j.Prune(ctx, "A", when, 10)
	failed("Prune", err)
	_, err = j.Begin(ctx, todo.Change{Store: "A"})
	failed("Begin", err)
	_, err = j.Record(ctx, todo.Change{Store: "A"}, todo.Edit{})
	failed("Record", err)
	exec("ALTER TABLE todo_change RENAME COLUMN state_gone TO state")

	// a row's state cannot be written
	exec("CREATE TRIGGER no_change_update BEFORE UPDATE ON todo_change BEGIN SELECT RAISE(ABORT, 'injected'); END")
	failed("Commit (state)", j.Commit(ctx, seq, todo.Edit{}))
	exec("DROP TRIGGER no_change_update")
	exec("CREATE TRIGGER no_change_delete BEFORE DELETE ON todo_change BEGIN SELECT RAISE(ABORT, 'injected'); END")
	if _, err := j.Record(ctx, todo.Change{Store: "A", At: when}, todo.Edit{}); err != nil {
		t.Fatal(err)
	}
	_, err = j.Prune(ctx, "A", when.Add(1), 1<<62)
	failed("Prune (delete)", err)
	exec("DROP TRIGGER no_change_delete")

	// the snapshot cannot be read or written
	exec("CREATE TRIGGER no_snap_insert BEFORE INSERT ON todo_snapshot BEGIN SELECT RAISE(ABORT, 'injected'); END")
	failed("Commit (put)", j.Commit(ctx, seq, put))
	failed("Baseline (put)", j.Baseline(ctx, "A", put.Put))
	exec("DROP TRIGGER no_snap_insert")
	exec("CREATE TRIGGER no_snap_delete BEFORE DELETE ON todo_snapshot BEGIN SELECT RAISE(ABORT, 'injected'); END")
	if err := j.Baseline(ctx, "A", put.Put); err != nil {
		t.Fatal(err)
	}
	failed("Commit (delete)", j.Commit(ctx, seq, del))
	failed("Baseline (delete)", j.Baseline(ctx, "A", nil))
	exec("DROP TRIGGER no_snap_delete")
	exec("UPDATE todo_snapshot SET fields = 'not json'")
	_, err = j.Snapshot(ctx, "A")
	failed("Snapshot (fields)", err)
	exec("ALTER TABLE todo_snapshot RENAME COLUMN rev TO rev_gone")
	_, err = j.Snapshot(ctx, "A")
	failed("Snapshot", err)
	exec("ALTER TABLE todo_snapshot RENAME COLUMN rev_gone TO rev")

	// a change's fields do not decode
	exec("UPDATE todo_change SET fields = 'not json'")
	_, err = j.Pending(ctx, "A")
	failed("Pending (fields)", err)
	exec("UPDATE todo_change SET fields = NULL")

	// the consumers cannot be read or written
	exec("ALTER TABLE todo_consumer RENAME COLUMN acked_seq TO acked_gone")
	_, err = j.Consumers(ctx, "A")
	failed("Consumers", err)
	failed("PutConsumer", j.PutConsumer(ctx, "A", todo.Consumer{Name: "c"}))
	exec("ALTER TABLE todo_consumer RENAME COLUMN acked_gone TO acked_seq")

	// each settings table cannot be read, or written
	for _, tc := range []struct{ table, col string }{{"todo_store", "path"}, {"todo_root", "override"}, {"todo_var", "value"}} {
		exec("ALTER TABLE " + tc.table + " RENAME COLUMN " + tc.col + " TO gone")
		_, err = j.Settings(ctx)
		failed("Settings: "+tc.table, err)
		failed("UpdateSettings: "+tc.table, update(func(*todo.Settings) {}))
		failed("Commit (repath): "+tc.table, j.Commit(ctx, seq, repath))
		exec("ALTER TABLE " + tc.table + " RENAME COLUMN gone TO " + tc.col)
	}
	for _, tc := range []struct {
		table string
		edit  func(*todo.Settings)
	}{
		{"todo_store", func(s *todo.Settings) { s.Stores["B"] = todo.StoreRecord{ID: "B"} }},
		{"todo_root", func(s *todo.Settings) { s.Roots["/s"] = todo.RootRecord{Root: "/s"} }},
		{"todo_var", func(s *todo.Settings) { s.Vars["W"] = "w" }},
		{"todo_store", func(s *todo.Settings) { delete(s.Stores, "A") }},
		{"todo_root", func(s *todo.Settings) { delete(s.Roots, "/r") }},
		{"todo_var", func(s *todo.Settings) { delete(s.Vars, "V") }},
	} {
		exec("CREATE TRIGGER no_write_i BEFORE INSERT ON " + tc.table + " BEGIN SELECT RAISE(ABORT, 'injected'); END")
		exec("CREATE TRIGGER no_write_d BEFORE DELETE ON " + tc.table + " BEGIN SELECT RAISE(ABORT, 'injected'); END")
		failed("UpdateSettings writing "+tc.table, update(tc.edit))
		exec("DROP TRIGGER no_write_i")
		exec("DROP TRIGGER no_write_d")
	}

	// and the journal works again once the storage is whole
	if err := j.Commit(ctx, seq, put); err != nil {
		t.Fatalf("after the repair: %v", err)
	}
}

// The log's lookups go through its index, not a scan of every store's rows.
func TestTheTodoLogLookupsUseTheirIndex(t *testing.T) {
	s := openStore(t)
	raw := s.raw(t)
	for _, q := range []string{
		"SELECT seq FROM todo_change WHERE store = 'A' AND state IN ('pending', 'staged') ORDER BY seq",
		"SELECT seq FROM todo_change WHERE store = 'A' AND state = 'committed' AND seq > 5 ORDER BY seq LIMIT 10",
		"SELECT seq FROM todo_change WHERE store = 'A' AND state = 'committed' ORDER BY seq DESC LIMIT 1",
	} {
		rows, err := raw.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+q)
		if err != nil {
			t.Fatal(err)
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
		_ = rows.Close()
		if p := strings.Join(plan, "; "); !strings.Contains(p, "todo_change_store") {
			t.Errorf("%s: plan %q does not use todo_change_store", q, p)
		}
	}
}
