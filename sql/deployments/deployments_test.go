package deployments_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/sqlite"

	"github.com/yongjohnlee80/autodoc/sql/deployments"
)

// released is every released script's digest, by engine and name. A released
// script NEVER changes (docs/ops/schema-scripts.md): the ledger records the
// digest it was applied with, and an edit under the same name is a schema no
// store agrees with. A script adds its line here when it ships in a tagged
// release; an existing line never changes.
var released = map[deployments.Engine]map[string]string{
	deployments.SQLite: {},
}

func names(t *testing.T, eng deployments.Engine) map[string]deployments.Script {
	t.Helper()
	all, err := deployments.Scripts(eng)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]deployments.Script{}
	for _, s := range all {
		out[s.Name] = s
	}
	return out
}

// Every engine names the same scripts: the ledgers must always agree on what
// "applied" means.
func TestEveryEngineNamesTheSameScripts(t *testing.T) {
	first := deployments.Engines[0]
	want := names(t, first)
	for _, eng := range deployments.Engines[1:] {
		got := names(t, eng)
		for n := range want {
			if _, ok := got[n]; !ok {
				t.Errorf("%s has %s; %s does not", first, n, eng)
			}
		}
		for n := range got {
			if _, ok := want[n]; !ok {
				t.Errorf("%s has %s; %s does not", eng, n, first)
			}
		}
	}
}

// Numbers are dense from 1; the baseline has no revert; every later update
// has exactly one revert, of the same slug, and no revert is without its
// update.
func TestNumbersAreDenseAndEveryChangeHasItsUndo(t *testing.T) {
	for _, eng := range deployments.Engines {
		all, err := deployments.Scripts(eng)
		if err != nil {
			t.Fatal(err)
		}
		updates := map[int]string{}
		reverts := map[int]string{}
		for _, s := range all {
			m := updates
			if s.Kind == deployments.Revert {
				m = reverts
			}
			if _, dup := m[s.Number]; dup {
				t.Errorf("%s: two %s scripts numbered %06d", eng, s.Kind, s.Number)
			}
			m[s.Number] = s.Slug
		}
		for n := 1; n <= len(updates); n++ {
			if _, ok := updates[n]; !ok {
				t.Errorf("%s: no update numbered %06d; numbers are dense", eng, n)
			}
		}
		if _, ok := reverts[1]; ok {
			t.Errorf("%s: 000001 has a revert; the baseline has none", eng)
		}
		for n, slug := range updates {
			if n == 1 {
				continue
			}
			if rs, ok := reverts[n]; !ok || rs != slug {
				t.Errorf("%s: update %06d_%s has no revert of the same slug (have %q)", eng, n, slug, rs)
			}
		}
		for n := range reverts {
			if _, ok := updates[n]; !ok {
				t.Errorf("%s: revert %06d has no update", eng, n)
			}
		}
	}
}

// Every script splits into statements with its engine's rules, and the
// baseline has at least one: an empty baseline would record work it never did.
func TestEveryScriptSplitsIntoStatements(t *testing.T) {
	for _, eng := range deployments.Engines {
		all, err := deployments.Scripts(eng)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range all {
			stmts, err := s.Statements()
			if err != nil {
				t.Errorf("%s/%s: %v", eng, s.Name, err)
				continue
			}
			if len(stmts) == 0 && s.Number == 1 {
				t.Errorf("%s/%s: no statements", eng, s.Name)
			}
			for _, st := range stmts {
				if st.Line < 1 {
					t.Errorf("%s/%s: a statement with no line: %q", eng, s.Name, st.Text)
				}
			}
		}
	}
}

// A released script is immutable: its digest is the one it shipped with.
func TestReleasedScriptsAreUnchanged(t *testing.T) {
	for eng, want := range released {
		got := names(t, eng)
		for name, digest := range want {
			s, ok := got[name]
			switch {
			case !ok:
				t.Errorf("%s/%s was released and is gone", eng, name)
			case digest == "":
				t.Errorf("%s/%s: record its released digest here: %q", eng, name, s.SHA256)
			case s.SHA256 != digest:
				t.Errorf("%s/%s changed after release: digest %s, released %s", eng, name, s.SHA256, digest)
			}
		}
	}
}

// The updates run, in order, on the driver the store uses, with foreign keys
// on; run again they change nothing, since every statement is IF NOT EXISTS.
// A row pointing into another workspace is refused: the composite keys hold.
func TestUpdatesApplyOnSQLite(t *testing.T) {
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "store.db") + "?_pragma=foreign_keys(1)"
	db, err := sqlite.Open(ctx, dsn, sqlite.MaxOpenConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	updates, err := deployments.Updates(deployments.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	for pass := 1; pass <= 2; pass++ {
		for _, s := range updates {
			stmts, err := s.Statements()
			if err != nil {
				t.Fatal(err)
			}
			for _, st := range stmts {
				if _, err := db.ExecContext(ctx, st.Text); err != nil {
					t.Fatalf("pass %d: %s line %d: %v", pass, s.Name, st.Line, err)
				}
			}
		}
	}
	for _, q := range []string{
		"INSERT INTO workspace(id, name, root, created_at, updated_at) VALUES (1, 'a', '/a', 0, 0), (2, 'b', '/b', 0, 0)",
		"INSERT INTO document(id, workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (10, 1, 'n.md', 'v', 1, 'i', 0)",
	} {
		if _, err := db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	chunk := "INSERT INTO chunk(workspace_id, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end) VALUES (?, 10, x'00', x'00', 1, 0, '', '', '', '', 0, 0)"
	if _, err := db.ExecContext(ctx, chunk, 1); err != nil {
		t.Fatalf("a chunk of its own workspace's document: %v", err)
	}
	if _, err := db.ExecContext(ctx, chunk, 2); err == nil {
		t.Fatal("a chunk of workspace 2 was stored against workspace 1's document")
	}
	if _, err := db.ExecContext(ctx, "DELETE FROM workspace WHERE id = 1"); err != nil {
		t.Fatal(err)
	}
	rows, err := db.QueryContext(ctx, "SELECT (SELECT COUNT(*) FROM document) + (SELECT COUNT(*) FROM chunk)")
	if err != nil {
		t.Fatal(err)
	}
	var left int
	for rows.Next() {
		if err := rows.Scan(&left); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if left != 0 {
		t.Fatalf("deleting the workspace left %d of its rows", left)
	}
}

// open is a fresh store with every update applied, on the store's driver, with
// foreign keys on.
func open(t *testing.T) dao.DataConn {
	t.Helper()
	ctx := context.Background()
	dsn := "file:" + filepath.Join(t.TempDir(), "store.db") + "?_pragma=foreign_keys(1)"
	db, err := sqlite.Open(ctx, dsn, sqlite.MaxOpenConns(1))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	updates, err := deployments.Updates(deployments.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range updates {
		stmts, err := s.Statements()
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range stmts {
			if _, err := db.ExecContext(ctx, st.Text); err != nil {
				t.Fatalf("%s line %d: %v", s.Name, st.Line, err)
			}
		}
	}
	return db
}

func exec(t *testing.T, db dao.DataConn, q string, args ...any) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// matches are the rowids the full-text index returns for q.
func matches(t *testing.T, db dao.DataConn, q string) []int64 {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), "SELECT rowid FROM chunk_fts WHERE chunk_fts MATCH ? ORDER BY rowid", q)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// The full-text index follows the chunk table by itself, through every way a
// chunk changes: written, its text rewritten, deleted, and deleted by a
// workspace's cascade. The last is the one no caller could do by hand, and a
// posting it left behind would answer for whatever chunk next reused the
// rowid, in another workspace.
func TestTheFullTextIndexFollowsTheChunks(t *testing.T) {
	db := open(t)
	chunk := "INSERT INTO chunk(id, workspace_id, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end) VALUES (?, ?, ?, x'00', x'00', 1, 0, '', ?, '', '', 0, 0)"
	doc := "INSERT INTO document(id, workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (?, ?, 'n.md', 'v', 1, 'i', 0)"
	exec(t, db, "INSERT INTO workspace(id, name, root, created_at, updated_at) VALUES (1, 'a', '/a', 0, 0)")
	exec(t, db, doc, 10, 1)
	exec(t, db, chunk, 5, 1, 10, "zebra")
	if got := fmt.Sprint(matches(t, db, "zebra")); got != "[5]" {
		t.Fatalf("a written chunk: MATCH zebra = %s, want [5]", got)
	}
	exec(t, db, "UPDATE chunk SET body = 'yak' WHERE id = 5")
	if got := fmt.Sprint(matches(t, db, "zebra"), matches(t, db, "yak")); got != "[] [5]" {
		t.Fatalf("a rewritten chunk: MATCH zebra, yak = %s, want [] [5]", got)
	}
	exec(t, db, chunk, 6, 1, 10, "ibis")
	exec(t, db, "DELETE FROM chunk WHERE id = 6")
	if got := fmt.Sprint(matches(t, db, "ibis")); got != "[]" {
		t.Fatalf("a deleted chunk: MATCH ibis = %s, want []", got)
	}
	exec(t, db, "DELETE FROM workspace WHERE id = 1")
	if got := fmt.Sprint(matches(t, db, "yak")); got != "[]" {
		t.Fatalf("after the workspace's cascade: MATCH yak = %s, want []", got)
	}
	exec(t, db, "INSERT INTO workspace(id, name, root, created_at, updated_at) VALUES (2, 'b', '/b', 0, 0)")
	exec(t, db, doc, 20, 2)
	exec(t, db, chunk, 5, 2, 20, "otter")
	if got := fmt.Sprint(matches(t, db, "yak"), matches(t, db, "otter")); got != "[] [5]" {
		t.Fatalf("rowid 5 reused in another workspace: MATCH yak, otter = %s, want [] [5]", got)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT workspace_id FROM chunk_fts WHERE chunk_fts MATCH 'otter'")
	if err != nil {
		t.Fatal(err)
	}
	var ws int64
	for rows.Next() {
		if err := rows.Scan(&ws); err != nil {
			t.Fatal(err)
		}
	}
	_ = rows.Close()
	if ws != 2 {
		t.Fatalf("the reused rowid's posting carries workspace %d, want 2", ws)
	}
}

// The schema is written down: docs/ops/schema-scripts.md lists every table the
// updates create, and no table they do not. FTS5's shadow tables are the
// engine's, not the schema's.
func TestTheSchemaDocListsEveryTable(t *testing.T) {
	ctx := context.Background()
	db, err := sqlite.Open(ctx, "file:"+filepath.Join(t.TempDir(), "store.db"), sqlite.MaxOpenConns(1))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	updates, err := deployments.Updates(deployments.SQLite)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range updates {
		stmts, err := s.Statements()
		if err != nil {
			t.Fatal(err)
		}
		for _, st := range stmts {
			if _, err := db.ExecContext(ctx, st.Text); err != nil {
				t.Fatal(err)
			}
		}
	}
	rows, err := db.QueryContext(ctx, "SELECT name FROM sqlite_schema WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'chunk_fts_%'")
	if err != nil {
		t.Fatal(err)
	}
	var store []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		store = append(store, n)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../docs/ops/schema-scripts.md")
	if err != nil {
		t.Fatal(err)
	}
	var listed []string
	for _, m := range regexp.MustCompile("(?m)^\\| `([a-z_]+)` \\|").FindAllSubmatch(doc, -1) {
		listed = append(listed, string(m[1]))
	}
	sort.Strings(store)
	sort.Strings(listed)
	if fmt.Sprint(store) != fmt.Sprint(listed) {
		t.Errorf("the schema doc's table list is not the store's tables:\n  store: %v\n  doc:   %v", store, listed)
	}
}

func ExampleScripts() {
	all, _ := deployments.Updates(deployments.SQLite)
	fmt.Println(all[0].Name)
	// Output: 000001_update_initialize_tables.sql
}
