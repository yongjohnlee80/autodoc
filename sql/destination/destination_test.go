package destination_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/sql/destination"
)

func TestTheScriptsLoad(t *testing.T) {
	for _, eng := range destination.Engines {
		scripts, err := deploy.Load(destination.FS(), eng)
		if err != nil {
			t.Fatalf("%s: %v", eng, err)
		}
		if len(scripts) == 0 || scripts[0].Name != "000001_update_initialize_index.sql" {
			t.Errorf("%s: scripts %v, want the baseline first", eng, scripts)
		}
	}
}

// The scripts on a real Postgres with pgvector, in a schema of its own that is dropped after:
// applied once, then again with nothing pending; a workspace's index written and read back by
// vector distance and by words; another workspace's rows never answered; and removing a workspace
// takes its index with it. Runs when AUTODOC_TEST_PGURL names a database it may create schemas in.
func TestThePostgresDestination(t *testing.T) {
	base := os.Getenv("AUTODOC_TEST_PGURL")
	if base == "" {
		t.Skip("AUTODOC_TEST_PGURL not set; skipping the Postgres destination test")
	}
	ctx := context.Background()
	admin, err := postgres.Open(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	// pgvector lives where a real database has it: in a schema every connection searches. The
	// script's CREATE EXTENSION IF NOT EXISTS then finds it rather than making another.
	if _, err := admin.ExecContext(ctx, `CREATE EXTENSION IF NOT EXISTS vector SCHEMA public`); err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	schema := "autodoc_dest_" + hex.EncodeToString(b)
	if _, err := admin.ExecContext(ctx, `CREATE SCHEMA `+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := admin.ExecContext(context.Background(), `DROP SCHEMA `+schema+` CASCADE`); err != nil {
			t.Errorf("dropping the test schema %s: %v", schema, err)
		}
		_ = admin.Close()
	})

	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("search_path", schema+",public")
	u.RawQuery = q.Encode()
	conn, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	st, err := destination.Runner().Apply(ctx, conn)
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(st.Applied) != 0 || len(st.Pending) != 1 {
		t.Errorf("first apply: had %v, ran %v; want nothing before and the baseline run", st.Applied, st.Pending)
	}
	if st, err := destination.Runner().Apply(ctx, conn); err != nil || len(st.Pending) != 0 || len(st.Applied) != 1 {
		t.Errorf("second apply: had %v, ran %v, %v; want the baseline recorded and nothing run", st.Applied, st.Pending, err)
	}

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := conn.ExecContext(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	for _, ws := range []string{"aaaa", "bbbb"} {
		exec(`INSERT INTO workspace (uid, name, created_at, updated_at) VALUES ($1, $2, 0, 0)`, ws, "ws-"+ws)
		exec(`INSERT INTO model (workspace_uid, fp, dims, active) VALUES ($1, 'm3', 3, true)`, ws)
	}
	doc := func(ws, path, title, body, vec string) {
		t.Helper()
		var id int64
		rows, err := conn.QueryContext(ctx, `INSERT INTO document (workspace_uid, path, version, active_gen, indexer, indexed_at)
			VALUES ($1, $2, 'v', 1, 'i', 0) RETURNING id`, ws, path)
		if err != nil {
			t.Fatal(err)
		}
		if !rows.Next() || rows.Scan(&id) != nil {
			t.Fatal("no document id")
		}
		_ = rows.Close()
		th := []byte(path)
		exec(`INSERT INTO chunk (workspace_uid, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end)
			VALUES ($1, $2, $3, $3, 1, 0, '', $4, $5, '', 0, 1)`, ws, id, th, body, title)
		exec(`INSERT INTO embedding (workspace_uid, text_hash, model_fp, vec) VALUES ($1, $2, 'm3', $3::vector)`, ws, th, vec)
	}
	doc("aaaa", "alone.md", "Alone", "an upbeat electronic dance track with female vocals", "[1,0,0]")
	doc("aaaa", "calm.md", "Calm", "a slow ambient meditation piece", "[0,1,0]")
	doc("bbbb", "other.md", "Other", "an upbeat electronic dance track from another machine", "[1,0,0]")

	// A model's index, as the store makes it when the model becomes active: a partial HNSW index at
	// its dimensions, cosine distance.
	exec(`CREATE INDEX embedding_vec_aaaa_m3 ON embedding USING hnsw ((vec::vector(3)) vector_cosine_ops)
		WHERE workspace_uid = 'aaaa' AND model_fp = 'm3'`)

	nearest := func(ws, q string) []string {
		t.Helper()
		rows, err := conn.QueryContext(ctx, `SELECT d.path FROM embedding e
			JOIN chunk c ON c.workspace_uid = e.workspace_uid AND c.text_hash = e.text_hash
			JOIN document d ON d.workspace_uid = c.workspace_uid AND d.id = c.doc_id
			WHERE e.workspace_uid = $1 AND e.model_fp = 'm3'
			ORDER BY e.vec::vector(3) <=> $2::vector(3) LIMIT 5`, ws, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		return out
	}
	if got := nearest("aaaa", "[0.9,0.1,0]"); strings.Join(got, ",") != "alone.md,calm.md" {
		t.Errorf("nearest in aaaa = %v, want alone.md first and nothing of bbbb", got)
	}

	words := func(ws, q string) []string {
		t.Helper()
		rows, err := conn.QueryContext(ctx, `SELECT d.path FROM chunk c JOIN document d ON d.workspace_uid = c.workspace_uid AND d.id = c.doc_id
			WHERE c.workspace_uid = $1 AND c.fts @@ plainto_tsquery('english', $2)
			ORDER BY ts_rank(c.fts, plainto_tsquery('english', $2)) DESC`, ws, q)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out []string
		for rows.Next() {
			var p string
			if err := rows.Scan(&p); err != nil {
				t.Fatal(err)
			}
			out = append(out, p)
		}
		return out
	}
	if got := words("aaaa", "dancing tracks"); strings.Join(got, ",") != "alone.md" {
		t.Errorf("words in aaaa = %v, want alone.md (stemmed, and none of bbbb)", got)
	}
	if got := words("aaaa", "calm"); strings.Join(got, ",") != "calm.md" {
		t.Errorf("a title word = %v, want calm.md", got)
	}

	exec(`DELETE FROM workspace WHERE uid = 'aaaa'`)
	count := func(table string) int64 {
		t.Helper()
		rows, err := conn.QueryContext(ctx, `SELECT count(*) FROM `+table+` WHERE workspace_uid = 'aaaa'`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var n int64
		if !rows.Next() || rows.Scan(&n) != nil {
			t.Fatal("no count")
		}
		return n
	}
	for _, table := range []string{"document", "chunk", "embedding", "model"} {
		if n := count(table); n != 0 {
			t.Errorf("%d %s rows outlived their workspace", n, table)
		}
	}
	if got := nearest("bbbb", "[1,0,0]"); strings.Join(got, ",") != "other.md" {
		t.Errorf("removing aaaa touched bbbb: %v", got)
	}
	var _ dao.DataConn = conn
}
