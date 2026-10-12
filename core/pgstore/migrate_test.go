package pgstore

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/search"
)

// migrationsUpTo is pgstore's script set as a release that shipped only the scripts up to n had it.
func migrationsUpTo(t *testing.T, n int) fs.FS {
	t.Helper()
	all, err := deploy.Load(migrations, dao.DialectPostgres)
	if err != nil {
		t.Fatal(err)
	}
	out := fstest.MapFS{}
	for _, s := range all {
		if s.Number <= n {
			out["postgres/"+s.Name] = &fstest.MapFile{Data: s.Body}
		}
	}
	return out
}

// exec runs q on conn, failing the test on an error.
func exec(t *testing.T, conn dao.DataConn, q string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(context.Background(), q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// ledgerScripts is the scripts the autodoc_schema ledger records, in order.
func ledgerScripts(t *testing.T, conn dao.DataConn) string {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), "SELECT script FROM autodoc_schema ORDER BY script")
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
	return strings.Join(out, " ")
}

// Migration 000002 on a schema a 000001 release migrated and wrote: every row it wrote survives
// and still answers by words and by meaning, its chunks read as sections, the three ingest tables
// exist empty, and the ledger records both scripts. Its revert undoes exactly 000002 and keeps the
// rows; migrating again comes back. The mutant, named here: without 000002 in the script set the
// ledger records one script and the kind column and ingest tables are missing.
func TestMigration000002KeepsWhat000001Wrote(t *testing.T) {
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	name := scratchName(t, admin, "autodoc_pgstore_")
	conn, err := postgres.OpenNamed(ctx, name, dsnOf(t, admin, name))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })

	// the 000001 release: its schema, and rows written as its Store wrote them (by SQL here: this
	// binary's Store writes the kind column, which 000001 does not have)
	if _, err := deploy.New(migrationsUpTo(t, 1), deploy.Ledger(Ledger)).Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if got := ledgerScripts(t, conn); got != "000001_update_initialize_tables.sql" {
		t.Fatalf("the 000001 release's ledger: %q", got)
	}
	exec(t, conn, "INSERT INTO rag_meta (tenant, active_model, target_model) VALUES ('t', $1, '')", model)
	exec(t, conn, `INSERT INTO rag_document (tenant, path, title, tags, facets, version, indexer, generation, semantic_ready)
		VALUES ('t', 'a.md', 'Alpha', '{x}', '{label_id=A}', 'v1', 'ix1', 3, true)`)
	exec(t, conn, `INSERT INTO rag_document (tenant, path) VALUES ('t', 'b.md')`)
	id := scalar(t, conn, "SELECT id::text FROM rag_document WHERE path = 'a.md'")
	c := search.Chunk{Ord: 0, Breadcrumb: "a", Body: "surviving words"}
	h := c.TextHash()
	exec(t, conn, `INSERT INTO rag_chunk (tenant, doc, ord, breadcrumb, body, title, text_hash, byte_end)
		VALUES ('t', $1::bigint, 0, $2, $3, 'Alpha', $4, 15)`, id, c.Breadcrumb, c.Body, h[:])
	exec(t, conn, "INSERT INTO rag_embedding (tenant, model, text_hash, vec) VALUES ('t', $1, $2, $3)",
		model, h[:], postgres.Vector(embedVec(c.EmbedText())))
	exec(t, conn, "INSERT INTO rag_link (tenant, src, dst_path) SELECT 't', id, 'a.md' FROM rag_document WHERE path = 'b.md'")

	if _, err := deploy.New(migrationsUpTo(t, 2), deploy.Ledger(Ledger)).Apply(ctx, conn); err != nil {
		t.Fatalf("000002 onto a populated 000001: %v", err)
	}
	if got := ledgerScripts(t, conn); got != "000001_update_initialize_tables.sql 000002_update_enterprise_ingest.sql" {
		t.Fatalf("the ledger after 000002: %q", got)
	}
	survived := func(stage string) {
		t.Helper()
		for q, want := range map[string]string{
			"SELECT count(*)::text FROM rag_document":                                "2",
			"SELECT count(*)::text FROM rag_chunk":                                   "1",
			"SELECT count(*)::text FROM rag_embedding":                               "1",
			"SELECT count(*)::text FROM rag_link":                                    "1",
			"SELECT active_model FROM rag_meta WHERE tenant = 't'":                   model,
			"SELECT generation::text FROM rag_document WHERE path = 'a.md'":          "3",
			"SELECT array_to_string(facets, ',') FROM rag_document WHERE id = " + id: "label_id=A",
		} {
			if got := scalar(t, conn, q); got != want {
				t.Errorf("%s: %s = %q, want %q", stage, q, got, want)
			}
		}
	}
	survived("after 000002")
	if got := scalar(t, conn, "SELECT kind FROM rag_chunk"); got != ChunkSection {
		t.Errorf("an existing chunk's kind: %q, want %q", got, ChunkSection)
	}
	for _, table := range []string{"rag_cursor", "rag_ingest_error", "rag_tenant_policy"} {
		if got := scalar(t, conn, "SELECT count(*)::text FROM "+table); got != "0" {
			t.Errorf("%s holds %s rows after the migration, want none", table, got)
		}
	}
	// the Store reads the migrated rows: by words, by meaning, and as the consumer's revalidation
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	if got := lexicalPaths(t, s, "surviving"); strings.Join(got, ",") != "a.md" {
		t.Errorf("by words after 000002: %v", got)
	}
	if err := s.View(ctx, func(v *View) error {
		got, err := v.Semantic(ctx, model, embedVec(c.EmbedText()), search.Filter{}, 5)
		if err != nil || len(got) != 1 || got[0].Path != "a.md" {
			t.Errorf("by meaning after 000002: %+v, %v", got, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if docs, _, err := s.Docs(ctx, "", 10); err != nil || len(docs) != 2 || docs[0].Version != "v1" || docs[0].Indexer != "ix1" {
		t.Errorf("Docs after 000002: %+v, %v", docs, err)
	}

	// the revert undoes exactly 000002: the rows stay, the ingest tables and the kind go
	if _, err := deploy.New(migrations, deploy.Ledger(Ledger)).Revert(ctx, conn, 2); err != nil {
		t.Fatalf("reverting 000002: %v", err)
	}
	if got := ledgerScripts(t, conn); got != "000001_update_initialize_tables.sql" {
		t.Errorf("the ledger after the revert: %q", got)
	}
	survived("after the revert")
	if got := scalar(t, conn, "SELECT count(*)::text FROM information_schema.columns WHERE table_name = 'rag_chunk' AND column_name = 'kind'"); got != "0" {
		t.Errorf("the kind column outlived the revert")
	}
	if got := scalar(t, conn, "SELECT count(*)::text FROM information_schema.tables WHERE table_name IN ('rag_cursor', 'rag_ingest_error', 'rag_tenant_policy')"); got != "0" {
		t.Errorf("%s ingest tables outlived the revert", got)
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatalf("migrating again after the revert: %v", err)
	}
	survived("migrated again")
}

// A chunk keeps its kind, and WithSectionsOnly leaves the abstracts out of Lexical and Semantic
// before the limit; without it both kinds answer. Kinds that do not match the chunks one for one
// are refused. The mutants, named here: Put writing no kind (every chunk a section), or the
// retrievers ignoring WithSectionsOnly — either way the abstract answers a sections-only search.
func TestAChunkKeepsItsKind(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	s := openCorpus(t, conn, "t", nil)
	sec := search.Chunk{Ord: 0, Breadcrumb: "doc", Body: "pump section words"}
	abs := search.Chunk{Ord: 1, Breadcrumb: "doc", Body: "pump abstract words"}
	putDoc(t, s, Document{Path: "doc.md", Chunks: []search.Chunk{sec, abs}, Kinds: []string{"", ChunkAbstract}}, true)
	if got := scalar(t, conn, "SELECT string_agg(kind, ',' ORDER BY ord) FROM rag_chunk"); got != "section,abstract" {
		t.Errorf("the kinds kept: %q", got)
	}
	ords := func(ctx context.Context) (lex, sem []int) {
		t.Helper()
		if err := s.View(ctx, func(v *View) error {
			l, err := v.Lexical(ctx, []search.Term{{Text: "abstract"}}, search.Filter{}, 1)
			if err != nil {
				return err
			}
			for _, c := range l {
				lex = append(lex, c.Ord)
			}
			m, err := v.Semantic(ctx, model, embedVec(abs.EmbedText()), search.Filter{}, 1)
			if err != nil {
				return err
			}
			for _, c := range m {
				sem = append(sem, c.Ord)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return lex, sem
	}
	// with a limit of one, the abstract is the nearest by meaning, and the only chunk with its
	// word: the filter must come before the limit
	if lex, sem := ords(ctx); len(lex) != 1 || lex[0] != 1 || len(sem) != 1 || sem[0] != 1 {
		t.Errorf("both kinds: lexical %v, semantic %v; want the abstract (1)", lex, sem)
	}
	lex, sem := ords(WithSectionsOnly(ctx))
	if len(lex) != 0 || len(sem) != 1 || sem[0] != 0 {
		t.Errorf("sections only: lexical %v, semantic %v; want no lexical hit and the section (0) by meaning", lex, sem)
	}
	_, err := s.Put(ctx, Document{Path: "bad.md", Chunks: []search.Chunk{sec, abs}, Kinds: []string{ChunkSection}})
	if !errors.Is(err, errs.ErrInvalidArgument) {
		t.Errorf("one kind for two chunks: %v, want ErrInvalidArgument", err)
	}
	b := s.Begin(ctx)
	_, err = b.Put(ctx, Document{Path: "bad.md", Chunks: []search.Chunk{sec}, Kinds: []string{ChunkSection, ChunkAbstract}})
	_ = b.Rollback()
	if !errors.Is(err, errs.ErrInvalidArgument) {
		t.Errorf("a batch's two kinds for one chunk: %v, want ErrInvalidArgument", err)
	}
}
