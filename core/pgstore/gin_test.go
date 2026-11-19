package pgstore

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
)

// pendingPages is index's GIN pending list, in pages (pgstattuple's pgstatginindex).
func pendingPages(t *testing.T, conn dao.DataConn, index string) int {
	t.Helper()
	exec(t, conn, "CREATE EXTENSION IF NOT EXISTS pgstattuple")
	n, err := strconv.Atoi(scalar(t, conn, "SELECT pending_pages::text FROM pgstatginindex('"+index+"'::regclass)"))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// fastupdate is index's fastupdate option as the catalog keeps it: "" when unset (on).
func fastupdate(t *testing.T, conn dao.DataConn, index string) string {
	t.Helper()
	return scalar(t, conn, `SELECT coalesce((SELECT option_value FROM pg_options_to_table(reloptions) WHERE option_name = 'fastupdate'), '')
		FROM pg_class WHERE relname = '`+index+`'`)
}

// putMany writes n documents with facets, tags and a chunk of text each.
func putMany(t *testing.T, s *Store, n int, prefix string) {
	t.Helper()
	ctx := context.Background()
	b := s.Begin(ctx)
	for i := range n {
		body := fmt.Sprintf("words of %s number %d", prefix, i)
		if _, err := b.Put(ctx, Document{Path: fmt.Sprintf("%s/%d.md", prefix, i), Tags: []string{"t" + strconv.Itoa(i%7)},
			Facets: map[string][]string{"label_id": {"L" + strconv.Itoa(i%13)}}, Chunks: []search.Chunk{{Body: body, ByteEnd: len(body)}}}); err != nil {
			_ = b.Rollback()
			t.Fatal(err)
		}
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
}

// The document's GIN indexes keep no pending list: after a load their lists are empty, while the
// text index's holds the load's words until MergePending merges them, and says how many pages it
// merged. The mutant, named here: 000009 not setting fastupdate off (the facets index's list fills).
func TestTheDocumentsGINIndexesKeepNoPendingList(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	for _, ix := range []string{"rag_document_facets", "rag_document_tags"} {
		if got := fastupdate(t, conn, ix); got != "off" {
			t.Errorf("%s: fastupdate %q; want off", ix, got)
		}
	}
	if got := fastupdate(t, conn, "rag_chunk_tsv"); got != "" {
		t.Errorf("rag_chunk_tsv: fastupdate %q; want the default (on)", got)
	}
	putMany(t, s, 500, "load")
	for _, ix := range []string{"rag_document_facets", "rag_document_tags"} {
		if n := pendingPages(t, conn, ix); n != 0 {
			t.Errorf("%s after a load: %d pending pages; want none", ix, n)
		}
	}
	if n := pendingPages(t, conn, "rag_chunk_tsv"); n == 0 {
		t.Fatal("rag_chunk_tsv after a load: no pending page; the cell needs one to see it merged")
	}
	merged, err := s.MergePending(ctx)
	if err != nil || merged == 0 {
		t.Fatalf("MergePending: %d pages, %v; want the list's pages", merged, err)
	}
	if n := pendingPages(t, conn, "rag_chunk_tsv"); n != 0 {
		t.Errorf("rag_chunk_tsv after MergePending: %d pending pages; want none", n)
	}
	if merged, err := s.MergePending(ctx); err != nil || merged != 0 {
		t.Errorf("MergePending again: %d pages, %v; want none, safely", merged, err)
	}
}

// 000009 over a schema whose document indexes already hold pending lists merges them in the same
// script, so no scan reads them after the upgrade; its revert puts fastupdate back to the default.
// The mutant, named here: the script without its gin_clean_pending_list (setting fastupdate off
// leaves a list as it is).
func TestMigration000009MergesTheListsAlreadyThere(t *testing.T) {
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
	if _, err := deploy.New(migrationsUpTo(t, 8), deploy.Ledger(Ledger)).Apply(ctx, conn); err != nil {
		t.Fatal(err)
	}
	exec(t, conn, `INSERT INTO rag_document (tenant, path, tags, facets)
		SELECT 't', 'd/' || g, ARRAY['tag' || (g % 7)], ARRAY['label_id=L' || (g % 13)] FROM generate_series(1, 2000) g`)
	for _, ix := range []string{"rag_document_facets", "rag_document_tags"} {
		if n := pendingPages(t, conn, ix); n == 0 {
			t.Fatalf("%s before 000009: no pending page; the cell needs one", ix)
		}
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	for _, ix := range []string{"rag_document_facets", "rag_document_tags"} {
		if n, fu := pendingPages(t, conn, ix), fastupdate(t, conn, ix); n != 0 || fu != "off" {
			t.Errorf("%s after 000009: %d pending pages, fastupdate %q; want none, off", ix, n, fu)
		}
	}
	if !strings.Contains(scalar(t, conn, "SELECT string_agg(script, ',') FROM autodoc_schema"), "000009_update_gin_without_pending_lists.sql") {
		t.Fatal("000009 is not in the ledger")
	}
	for _, n := range []int{11, 10, 9} { // the latest first, down to 000009
		if _, err := deploy.New(migrations, deploy.Ledger(Ledger)).Revert(ctx, conn, n); err != nil {
			t.Fatal(err)
		}
	}
	for _, ix := range []string{"rag_document_facets", "rag_document_tags"} {
		if fu := fastupdate(t, conn, ix); fu != "" {
			t.Errorf("%s after the revert: fastupdate %q; want the default", ix, fu)
		}
	}
}
