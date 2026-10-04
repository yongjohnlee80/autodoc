package deployments_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/dao/sqlite"

	"github.com/yongjohnlee80/autodoc/sql/deployments"
)

// released is every released script's digest, by engine and name. A released
// script NEVER changes (docs/ops/schema-scripts.md): the ledger records the
// digest it was applied with, and an edit under the same name is a schema no
// store agrees with. A script adds its line here when it ships in a tagged
// release; an existing line never changes.
var released = map[string]map[string]string{
	dao.DialectSQLite: {},
}

func names(t *testing.T, eng string) map[string]deploy.Script {
	t.Helper()
	all, err := deploy.Load(deployments.FS(), eng)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]deploy.Script{}
	for _, s := range all {
		out[s.Name] = s
	}
	return out
}

// Every engine's set loads: dao/deploy refuses one that is not dense from
// 000001 and paired, or a file it cannot place. Each script splits into
// statements, and the baseline has some.
func TestEveryEngineLoads(t *testing.T) {
	for _, eng := range deployments.Engines {
		all, err := deploy.Load(deployments.FS(), eng)
		if err != nil {
			t.Fatalf("%s: %v", eng, err)
		}
		if len(all) == 0 || all[0].Name != "000001_update_initialize_tables.sql" {
			t.Fatalf("%s: the baseline is not first: %v", eng, all)
		}
		for _, s := range all {
			stmts, err := s.Statements()
			if err != nil {
				t.Errorf("%s/%s: %v", eng, s.Name, err)
			}
			if s.Number == 1 && len(stmts) == 0 {
				t.Errorf("%s/%s: no statements", eng, s.Name)
			}
		}
	}
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

// The scripts apply on the driver the store uses, with foreign keys on, and a
// second apply has nothing to do. A row pointing into another workspace is
// refused, and a workspace's delete leaves none of its rows.
func TestTheScriptsApplyOnSQLite(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	st, err := deployments.Runner().Apply(ctx, db)
	if err != nil || len(st.Pending) != 0 || len(st.Applied) == 0 {
		t.Fatalf("a second Apply = %+v, %v; want nothing pending", st, err)
	}
	exec(t, db, "INSERT INTO workspace(id, name, root, created_at, updated_at) VALUES (1, 'a', '/a', 0, 0), (2, 'b', '/b', 0, 0)")
	exec(t, db, "INSERT INTO document(id, workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (10, 1, 'n.md', 'v', 1, 'i', 0)")
	chunk := "INSERT INTO chunk(workspace_id, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end) VALUES (?, 10, x'00', x'00', 1, 0, '', '', '', '', 0, 0)"
	exec(t, db, chunk, 1)
	if _, err := db.ExecContext(ctx, chunk, 2); err == nil {
		t.Fatal("a chunk of workspace 2 was stored against workspace 1's document")
	}
	exec(t, db, "DELETE FROM workspace WHERE id = 1")
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

// 000012 gives every chunk an embed text, '' unless a writer sets one, and its revert takes the
// column and nothing else: the chunks stay.
func TestTheChunkEmbedColumn(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	exec(t, db, "INSERT INTO workspace(id, name, root, created_at, updated_at) VALUES (1, 'a', '/a', 0, 0)")
	exec(t, db, "INSERT INTO document(id, workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (10, 1, 'n.go', 'v', 1, 'i', 0)")
	exec(t, db, "INSERT INTO chunk(workspace_id, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end) VALUES (1, 10, x'00', x'00', 1, 0, '', 'b', '', '', 0, 0)")
	exec(t, db, "INSERT INTO chunk(workspace_id, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end, embed) VALUES (1, 10, x'01', x'01', 1, 1, '', 'b', '', '', 0, 0, 'func F()')")
	embeds := func() []string {
		rows, err := db.QueryContext(ctx, "SELECT embed FROM chunk ORDER BY ord")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				t.Fatal(err)
			}
			out = append(out, s)
		}
		return out
	}
	if got := embeds(); fmt.Sprint(got) != "[ func F()]" {
		t.Fatalf("embeds %q, want '' and the one written", got)
	}
	if name, err := deployments.Runner().Revert(ctx, db, 12); err != nil || !strings.Contains(name, "000012") {
		t.Fatalf("revert 000012: %s, %v", name, err)
	}
	if _, err := db.QueryContext(ctx, "SELECT embed FROM chunk"); err == nil {
		t.Fatal("the revert left the column")
	}
	rows, err := db.QueryContext(ctx, "SELECT COUNT(*) FROM chunk")
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for rows.Next() {
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	_ = rows.Close()
	if n != 2 {
		t.Fatalf("%d chunks after the revert, want 2", n)
	}
}

func TestEmbeddingModelLookupIndexIsUsed(t *testing.T) {
	db := open(t)
	rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN SELECT text_hash FROM embedding WHERE workspace_id = ? AND model_fp = ?", 1, "model")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(detail, "embedding_workspace_model_text") {
			found = true
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("model lookup did not use embedding_workspace_model_text")
	}
}

func TestSectionMigrationRevertsWithoutDroppingVectors(t *testing.T) {
	db := open(t)
	exec(t, db, "INSERT INTO workspace(id, name, root, created_at, updated_at) VALUES (1, 'a', '/a', 0, 0)")
	exec(t, db, "INSERT INTO model(workspace_id, fp, active) VALUES (1, 'm', 1)")
	exec(t, db, "INSERT INTO embedding(workspace_id, text_hash, model_fp, bits, f32) VALUES (1, x'01', 'm', x'02', x'03')")
	exec(t, db, "UPDATE workspace SET section_tokens = 256 WHERE id = 1")
	// every script after 000004 is reverted first, newest first: only the latest can be
	all, err := deploy.Load(deployments.FS(), deployments.Engines[0])
	if err != nil {
		t.Fatal(err)
	}
	for n := all[len(all)-1].Number; n > 4; n-- {
		if _, err := deployments.Runner().Revert(context.Background(), db, n); err != nil {
			t.Fatalf("reverting %06d: %v", n, err)
		}
	}
	name, err := deployments.Runner().Revert(context.Background(), db, 4)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(name, "000004") {
		t.Fatalf("reverted %s, want 000004", name)
	}
	rows, err := db.QueryContext(context.Background(), "SELECT hex(bits), hex(f32) FROM embedding WHERE model_fp = 'm'")
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("revert dropped the vector")
	}
	var bits, f32 string
	if err := rows.Scan(&bits, &f32); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	if bits != "02" || f32 != "03" {
		t.Fatalf("vector after revert = %s/%s", bits, f32)
	}
	if _, err := deployments.Runner().Apply(context.Background(), db); err != nil {
		t.Fatal(err)
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
	if _, err := deployments.Runner().Apply(ctx, db); err != nil {
		t.Fatal(err)
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
	db := open(t)
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

// The writer's lookups each probe an index on both of what they are given, the
// workspace and the key: a probe on the workspace alone reads every row the
// workspace has, once per document indexed. SQLite does this unasked where
// the index would not cover the lookup and the primary key leads with the
// workspace, so the plans are checked, on the store with no statistics.
func TestLookupsProbeTheirKey(t *testing.T) {
	db := open(t)
	for _, c := range []struct{ query, probe string }{
		{`SELECT name_key, is_path FROM doc_name WHERE doc_id = ? AND workspace_id = ?`, "doc_id=?"},
		{`DELETE FROM doc_name WHERE doc_id = ? AND workspace_id = ?`, "doc_id=?"},
		{`SELECT doc_id, is_path FROM doc_name WHERE name_key = ? AND workspace_id = ?`, "name_key=?"},
		{`SELECT tag FROM doc_tag WHERE doc_id = ? AND workspace_id = ? ORDER BY tag`, "doc_id=?"},
		{`DELETE FROM doc_tag WHERE doc_id = ? AND workspace_id = ?`, "doc_id=?"},
		{`DELETE FROM doc_alias WHERE doc_id = ? AND workspace_id = ?`, "doc_id=?"},
		{`SELECT id, kind, name, dst_doc FROM link WHERE dst_doc = ? AND workspace_id = ?`, "dst_doc=?"},
		{`SELECT id, kind, name, dst_doc FROM link WHERE name IN (?, ?) AND workspace_id = ?`, "name=?"},
		{`DELETE FROM link WHERE src_doc = ? AND workspace_id = ?`, "src_doc=?"},
		{`SELECT id, hash FROM chunk WHERE doc_id = ? AND gen_from <= ? AND (gen_to IS NULL OR gen_to > ?) AND workspace_id = ? ORDER BY ord`, "doc_id=?"},
		{`SELECT version FROM document WHERE path = ? AND workspace_id = ? LIMIT 1`, "path=?"},
	} {
		rows, err := db.QueryContext(context.Background(), "EXPLAIN QUERY PLAN "+c.query, make([]any, strings.Count(c.query, "?"))...)
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
		if !strings.Contains(strings.Join(plan, "; "), "(workspace_id=? AND "+c.probe) {
			t.Errorf("%s\n  plan %q probes no index on the workspace and %s", c.query, plan, c.probe)
		}
	}
}

func ExampleFS() {
	all, _ := deploy.Load(deployments.FS(), dao.DialectSQLite)
	fmt.Println(all[0].Name)
	// Output: 000001_update_initialize_tables.sql
}
