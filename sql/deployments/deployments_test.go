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
// release; an existing line never changes. Each digest is the script as the
// first tag that shipped it has it, and no later tag changed it.
var released = map[string]map[string]string{
	dao.DialectSQLite: {
		"000001_update_initialize_tables.sql":          "a363a5140ac9d04e34e2b9a314f3e89fdd2477f628cd015b6e88e4df2d42c2bd", // v0.1.0
		"000002_revert_add_preferences.sql":            "e56c72b7784a1bf92a629bc27d0830518bc6e36f5b80c867814d6a4352ebd536", // v0.1.0
		"000002_update_add_preferences.sql":            "1758bad5ae89a5218493f797f2e49c4a8f526b599fc8177341707b295b439750", // v0.1.0
		"000003_revert_provider_context.sql":           "65f3e377db4804716302b304501c45983ede49193f6e8857ab34d09e59187f60", // v0.1.0
		"000003_update_provider_context.sql":           "14b92989312ddc7ccf9c94de83653c5ff050cf4f485459ecedfd9094f06655b0", // v0.1.0
		"000004_revert_embedding_model_lookup.sql":     "d4e3553ccb0e8c273eadc8464b74a18ac92135af6323137738e44345fd53f1bb", // v0.1.6
		"000004_update_embedding_model_lookup.sql":     "335d1ae41ec587a5c7e1f63dc0779b4951b79ba1c6bc90e1059265b763666107", // v0.1.6
		"000005_revert_workspace_embedding_policy.sql": "5c6a0c102779517c2dca0ae2c940bb148eb37d43c53d3aeb53bf9e70df587c81", // v0.1.7
		"000005_update_workspace_embedding_policy.sql": "a506ba3c38479f61c9b5768de517b68237e648ea793c7fb5b1df02fa67083155", // v0.1.7
		"000006_revert_empty_include.sql":              "7e72672711aff13544dfb93a07889ae5b26d5f14de892db5fb88d217c5b8f3e3", // v0.1.9
		"000006_update_empty_include.sql":              "9ccb8a03c176a29dd4641e34da8c185d991aafc9857f46fe4273d8b8a98fe2e9", // v0.1.9
		"000007_revert_frontmatter_schema.sql":         "5f62859ddc37f99502e0cb454ff4b6ed4ca224ecef0c3402fa1158c3f0049207", // v0.1.9
		"000007_update_frontmatter_schema.sql":         "e39cfcc1596e7f22fa0bcc161644b03926f6270aff91654d4304877e0f4fbd99", // v0.1.9
		"000008_revert_text_extensions.sql":            "a26c62f660167c3dff020e17c1c49a999adb434f94915113bb9a761a52af57d1", // v0.1.9
		"000008_update_text_extensions.sql":            "052f0795e753d7d320811c7208c96a9f51583f2096247da0d951819fbd657f5f", // v0.1.9
		"000009_revert_events.sql":                     "a3c42a491894a5ff8813c1b4068a2bc4f9237b721963614fd2431b238ada41c0", // v0.1.9
		"000009_update_events.sql":                     "8e5662d6d916f0932919e851652789b20198c6c4b547798244afb18304702e4d", // v0.1.9
		"000010_revert_workspace_provider.sql":         "8c0affd25711ea353cbdcc03ef517ffbc679673c9e149f9ce958b48e586d7055", // v0.1.9
		"000010_update_workspace_provider.sql":         "015f78da4218145aca891e22384fbf3b7b3ff66709be159dad134e16e7c7be24", // v0.1.9
		"000011_revert_workspace_databases.sql":        "0658fcc035bd45ed539112f8dbd4aee55cef606244c405689210ef849bd9b61b", // v0.1.11
		"000011_update_workspace_databases.sql":        "4edfa9ac9e8bb7303b481f578d523c8f36fc4268867e3b6d72c7c92837c8cee9", // v0.1.11
		"000012_revert_chunk_embed.sql":                "43cea207cb7e2858c9138fdad7f9ee34eadd08e634bc4e1ba79a06ca3cd44abf", // v0.1.12
		"000012_update_chunk_embed.sql":                "851c770f4839876e68ffc7691490a670abf49b2f6908c3794a46054dcc2933ea", // v0.1.12
		"000013_revert_rankers.sql":                    "5e7317139ad05fb7f07e3076cea4b78d142d63e15ca70395173fbfcb03c34d63", // v0.1.13
		"000013_update_rankers.sql":                    "b56a17a96cbb8058dec9370e70ee287bd21e69b02b6bce48a92eca9dbae52fee", // v0.1.13
		"000014_revert_relation_keys.sql":              "c3ea96f0ffb95f95a4d72732850aaa5506b7428c8e09bdb19faaff2dac9ba1dd", // v0.1.20
		"000014_update_relation_keys.sql":              "c93a22159807ec24241a968b6fa2ac1a06b82d41f2564e4bdb3e12ac30ec4935", // v0.1.20
		"000015_revert_model_target.sql":               "e68d1845c9809789e2a95eb51194892bb75d1bdd2c7bdc66d24f5468dde30e43", // v0.1.21
		"000015_update_model_target.sql":               "b3e427c8cf038b31b75ea293a1e39e56d9a5045af01b36fdf6917e5b9cf1cd52", // v0.1.21
		"000016_revert_chunk_text.sql":                 "b6abd73c61c995fe025987a4a9fccbd14def5c5c8b578821a000b08b61b1d41e", // v0.1.22
		"000016_update_chunk_text.sql":                 "20095490a20b1bf3bf24100fd9cc3c32b5be2f4f8a762ac3baa4d74ee639d457", // v0.1.22
		"000017_revert_todo_journal.sql":               "4d6a59d6bbbc49c41e3cff44bda6ed6067e653812cdb692fd57553906acb430c", // v0.1.38
		"000017_update_todo_journal.sql":               "722ea9820c78f62864d612d396f7378882141a67240bf993b6c1efa42f4c1159", // v0.1.38
		"000018_revert_todo_consumer_resync.sql":       "aff6a4e6a53dbcf39671096990a68e12cca83e2dc82a7ee9ac3a1910f70d0929", // v0.1.41
		"000018_update_todo_consumer_resync.sql":       "66efc8ab196b6753a0453533a165ca9a1f55df07cb1179c48ae605d8570c06af", // v0.1.41
		"000019_revert_todo_store_refreshed.sql":       "511502af936c922beec17fb3b12851b132ce67217f4604fee3720578b6ff1ea5", // v0.1.41
		"000019_update_todo_store_refreshed.sql":       "2939760ddf372d1fc76b8533adc644cfd9d27d57b7e85e62e56792d5177b26b5", // v0.1.41
		"000020_revert_todo_store_vars.sql":            "ce90a4775cab2f33c98b874b2fd1d3978d82e716842a0aaac2c21ec91a615177", // v0.1.49
		"000020_update_todo_store_vars.sql":            "ff8ace3ebac253787bb2454d8c08471a08f3643714dc834fb14eeca964558de7", // v0.1.49
		"000021_revert_todo_graph_only.sql":            "e613711d6f5a39e3e6e9f262c1f49eb81bdabce725f435d009ec6b68fb4bffd4", // v0.1.50
		"000021_update_todo_graph_only.sql":            "2da44e9b2e17040063269c44a7673594e51e6b08843cd1c5df367a0eb1b30607", // v0.1.50
	},
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

// 000022 stores todo_graph_only, the opt-out, in place of 000021's todo_search: every workspace has
// its todo tasks in search, a new one by the column's default and an old one whatever 000021 held.
// The revert puts back todo_search, the inverse of todo_graph_only.
func TestTheTodoSearchDefault(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	ints := func(q string) []int64 {
		t.Helper()
		rows, err := db.QueryContext(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []int64
		for rows.Next() {
			var n int64
			if err := rows.Scan(&n); err != nil {
				t.Fatal(err)
			}
			out = append(out, n)
		}
		return out
	}
	exec(t, db, "INSERT INTO workspace(id, name, root, created_at, updated_at) VALUES (1, 'a', '/a', 0, 0), (2, 'b', '/b', 0, 0)")
	if got := fmt.Sprint(ints("SELECT todo_graph_only FROM workspace ORDER BY id")); got != "[0 0]" {
		t.Fatalf("a new workspace's todo_graph_only %s, want 0: its tasks in search", got)
	}
	exec(t, db, "UPDATE workspace SET todo_graph_only = 1 WHERE id = 2")
	if name, err := deployments.Runner().Revert(ctx, db, 23); err != nil || !strings.Contains(name, "000023") {
		t.Fatalf("revert 000023, the script after: %s, %v", name, err)
	}
	if name, err := deployments.Runner().Revert(ctx, db, 22); err != nil || !strings.Contains(name, "000022") {
		t.Fatalf("revert 000022: %s, %v", name, err)
	}
	if got := fmt.Sprint(ints("SELECT todo_search FROM workspace ORDER BY id")); got != "[1 0]" {
		t.Fatalf("reverted todo_search %s, want [1 0]: the inverse", got)
	}
	if _, err := db.QueryContext(ctx, "SELECT todo_graph_only FROM workspace"); err == nil {
		t.Fatal("the revert left todo_graph_only")
	}
	exec(t, db, "UPDATE workspace SET todo_search = 0") // 000021's default, and an opt-in turned off
	if _, err := deployments.Runner().Apply(ctx, db); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(ints("SELECT todo_graph_only FROM workspace ORDER BY id")); got != "[0 0]" {
		t.Fatalf("todo_graph_only after 000022 %s, want [0 0]: every workspace's tasks in search", got)
	}
	if _, err := db.QueryContext(ctx, "SELECT todo_search FROM workspace"); err == nil {
		t.Fatal("000022 left todo_search")
	}
}

// 000023 records each todo store's workspace (ADR 1792132105 §2.4), empty until the daemon records it,
// and keeps this machine's workspace variables, one row per workspace and name. The revert drops both.
func TestTheWorkspaceVarsScript(t *testing.T) {
	ctx := context.Background()
	db := open(t)
	exec(t, db, "INSERT INTO todo_store(id, path) VALUES ('s', '/kb/.todo-list')")
	rows, err := db.QueryContext(ctx, "SELECT kb FROM todo_store WHERE id = 's'")
	if err != nil {
		t.Fatal(err)
	}
	kb := "unread"
	for rows.Next() {
		if err := rows.Scan(&kb); err != nil {
			t.Fatal(err)
		}
	}
	if err := rows.Close(); err != nil || kb != "" {
		t.Fatalf("a store's kb %q, %v; want '' until recorded", kb, err)
	}
	exec(t, db, "INSERT INTO todo_workspace_var(kb, name, value) VALUES ('/kb', 'DUMPS', '/data'), ('/other', 'DUMPS', '/d')")
	if _, err := db.ExecContext(ctx, "INSERT INTO todo_workspace_var(kb, name, value) VALUES ('/kb', 'DUMPS', '/x')"); err == nil {
		t.Fatal("two values of one name in one workspace")
	}
	if name, err := deployments.Runner().Revert(ctx, db, 23); err != nil || !strings.Contains(name, "000023") {
		t.Fatalf("revert 000023: %s, %v", name, err)
	}
	if _, err := db.QueryContext(ctx, "SELECT kb FROM todo_store"); err == nil {
		t.Fatal("the revert left todo_store.kb")
	}
	if _, err := db.QueryContext(ctx, "SELECT 1 FROM todo_workspace_var"); err == nil {
		t.Fatal("the revert left todo_workspace_var")
	}
}

// 000012 gives every chunk an embed text, ” unless a writer sets one, and its revert takes the
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
	// the later scripts go first: only the latest applied can be reverted
	all, err := deploy.Load(deployments.FS(), deployments.Engines[0])
	if err != nil {
		t.Fatal(err)
	}
	for n := all[len(all)-1].Number; n > 12; n-- {
		if _, err := deployments.Runner().Revert(ctx, db, n); err != nil {
			t.Fatalf("reverting %06d: %v", n, err)
		}
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
