package pgstore

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/searchtest"
)

// sourced writes a ready document of label A rendered from source row id at at (the zero time:
// none recorded), one chunk per text, each embedded under model and under "other|model||64".
func sourced(t *testing.T, s *Store, path, id string, at time.Time, texts ...string) {
	t.Helper()
	ctx := context.Background()
	var chunks []search.Chunk
	var vecs []Embedding
	for i, body := range texts {
		// the text a vector is made of is the body alone, so two documents' equal bodies share one
		c := search.Chunk{Ord: i, Breadcrumb: path, Body: body, Embed: body, ByteEnd: len(body)}
		chunks = append(chunks, c)
		vecs = append(vecs, Embedding{TextHash: c.TextHash(), Vec: searchtest.Embed(body)})
	}
	d := Document{Path: path, SourceID: id, SourceAt: at, Facets: map[string][]string{"label_id": {"A"}}, Chunks: chunks,
		Values: map[string][]float64{"bpm": {120}}}
	if _, err := s.Put(ctx, d); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{model, "other|model||64"} {
		if err := s.Embed(ctx, m, vecs); err != nil {
			t.Fatal(err)
		}
	}
	if ready, err := s.MarkReady(ctx, path, model); err != nil || !ready {
		t.Fatalf("mark %s ready: %v, %v", path, ready, err)
	}
}

// deleteSource runs one DeleteSource in its own batch.
func deleteSource(t *testing.T, s *Store, id string, at time.Time) bool {
	t.Helper()
	ctx := context.Background()
	b := s.Begin(ctx)
	gone, err := b.DeleteSource(ctx, id, at)
	if err != nil {
		_ = b.Rollback()
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	return gone
}

// textsOf is how many embeddings tenant has of body's text, under every model.
func textsOf(t *testing.T, conn dao.DataConn, tenant, body string) string {
	t.Helper()
	h := search.Chunk{Body: body, Embed: body}.TextHash()
	rows, err := conn.QueryContext(context.Background(), "SELECT count(*)::text FROM rag_embedding WHERE tenant = $1 AND text_hash = $2", tenant, h[:])
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var n string
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
	}
	return n
}

// A source row's delete removes its document only when no older than the document's last write
// (source_at): an older one is a no-op, an equal one deletes (the tie), and a document written
// before source_at was kept (NULL) goes to any. It reports whether a document went; an id never
// written is a no-op. The document's chunks, values and the embeddings of its texts go with it,
// under every model, unless another chunk of the tenant still has the text; another tenant's
// document of the same source id is untouched. After it, no retrieval path returns the document.
// Batch.Delete drops a deleted document's unshared embeddings too. The mutants, named here: no
// source_at guard (the older delete removes it); shared embeddings deleted (the shared text loses
// its vectors).
func TestADeleteRemovesTheRowsDocumentWhenNoOlder(t *testing.T) {
	ctx := context.Background()
	conn := scratch(t)
	s := openCorpus(t, conn, "t", nil)
	u := openCorpus(t, conn, "u", nil)
	t1 := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	sourced(t, s, "track/7.md", "7", t1, "only seven sings this", "a chorus they share")
	sourced(t, s, "track/8.md", "8", t1, "eight alone", "a chorus they share")
	sourced(t, u, "track/7.md", "7", t1, "only seven sings this")

	if deleteSource(t, s, "7", t1.Add(-time.Second)) {
		t.Fatal("a delete older than the document's write removed it")
	}
	if !deleteSource(t, s, "7", t1) {
		t.Fatal("a delete at the document's write time (the tie) kept it")
	}
	for _, c := range []struct{ tenant, body, want string }{
		{"t", "only seven sings this", "0"}, {"t", "a chorus they share", "2"}, {"t", "eight alone", "2"},
		{"u", "only seven sings this", "2"},
	} {
		if got := textsOf(t, conn, c.tenant, c.body); got != c.want {
			t.Errorf("%s's embeddings of %q: %s; want %s", c.tenant, c.body, got, c.want)
		}
	}
	if got := scalar(t, conn, "SELECT count(*)::text FROM rag_chunk k JOIN rag_document d ON d.tenant = k.tenant AND d.id = k.doc WHERE d.tenant = 't' AND d.path = 'track/7.md'"); got != "0" {
		t.Errorf("track/7.md's chunks after its delete: %s", got)
	}
	if got := scalar(t, conn, "SELECT count(*)::text FROM rag_document_value v JOIN rag_document d ON d.tenant = v.tenant AND d.id = v.doc_id WHERE d.tenant = 't'"); got != "1" {
		t.Errorf("t's typed values after the delete: %s; want track/8.md's one", got)
	}
	err := s.ViewWithin(ctx, map[string][]string{"label_id": {"A"}}, func(v *View) error {
		l, err := v.Lexical(ctx, []search.Term{{Text: "seven"}}, search.Filter{}, 10)
		if err != nil {
			return err
		}
		m, err := v.Semantic(ctx, model, searchtest.Embed("only seven sings this"), search.Filter{}, 10)
		if err != nil {
			return err
		}
		ls, err := v.List(ctx, search.Filter{}, 10)
		if err != nil {
			return err
		}
		for path, got := range map[string][]string{"lexical": sortedPaths(l), "semantic": sortedPaths(m), "list": sortedPaths(ls)} {
			if slices.Contains(got, "track/7.md") {
				t.Errorf("%s answers the deleted track/7.md: %v", path, got)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleteSource(t, s, "99", t1) || deleteSource(t, s, "7", t1.Add(time.Hour)) {
		t.Error("a delete of an id never written, or already gone, reported a document")
	}
	if got := scalar(t, conn, "SELECT count(*)::text FROM rag_document WHERE tenant = 'u'"); got != "1" {
		t.Errorf("tenant u's documents: %s; want its track/7.md kept", got)
	}

	sourced(t, s, "track/9.md", "9", time.Time{}, "written before source_at was kept")
	if !deleteSource(t, s, "9", t1.Add(-24*time.Hour)) {
		t.Error("a document with no source_at was kept by a delete")
	}

	b := s.Begin(ctx)
	if err := b.Delete(ctx, "track/8.md"); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"eight alone", "a chorus they share"} {
		if got := textsOf(t, conn, "t", body); got != "0" {
			t.Errorf("t's embeddings of %q after the last document holding it was deleted: %s", body, got)
		}
	}
}

// The deletes cursor commits with its batch, apart from the rows' cursor and the last refresh, and
// reads back as set; none is the zero Cursor.
func TestTheDeletesCursorIsItsOwn(t *testing.T) {
	ctx := context.Background()
	s := openCorpus(t, scratch(t), "t", nil)
	if c, err := s.DeletesCursor(ctx); err != nil || !c.IsZero() {
		t.Fatalf("no tombstone yet: %+v, %v", c, err)
	}
	rows := Cursor{UpdatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC), ID: "r"}
	dels := Cursor{UpdatedAt: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), ID: "d"}
	b := s.Begin(ctx)
	if err := b.SetCursor(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if err := b.SetDeletesCursor(ctx, dels); err != nil {
		t.Fatal(err)
	}
	if err := b.Commit(); err != nil {
		t.Fatal(err)
	}
	b = s.Begin(ctx)
	if err := b.SetDeletesCursor(ctx, Cursor{UpdatedAt: dels.UpdatedAt.Add(time.Hour), ID: "e"}); err != nil {
		t.Fatal(err)
	}
	_ = b.Rollback()
	c, err := s.Cursor(ctx)
	d, derr := s.DeletesCursor(ctx)
	if err != nil || derr != nil || !c.UpdatedAt.Equal(rows.UpdatedAt) || c.ID != "r" || !d.UpdatedAt.Equal(dels.UpdatedAt) || d.ID != "d" {
		t.Fatalf("the cursors: rows %+v %v, deletes %+v %v; want each as committed, the rolled-back one not", c, err, d, derr)
	}
}

// A delete finds its document by an index of the tenant's source ids, not by a scan of the
// tenant's documents: at 20,000 documents, the plan of DeleteSource's read probes
// rag_document_source. The mutant, named here: 000010 without the index (a scan).
func TestADeleteFindsItsDocumentByAnIndex(t *testing.T) {
	conn := scratch(t)
	exec(t, conn, `INSERT INTO rag_document (tenant, path, source_id, source_at)
		SELECT 't', 'track/' || g || '.md', g::text, now() FROM generate_series(1, 20000) g`)
	exec(t, conn, `ANALYZE rag_document`)
	rows, err := conn.QueryContext(context.Background(), `EXPLAIN SELECT rag_document.id FROM rag_document
		WHERE rag_document.tenant = 't' AND rag_document.source_id = '777'
		  AND (rag_document.source_at IS NULL OR rag_document.source_at <= now())`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		plan += line + "\n"
	}
	if !strings.Contains(plan, "rag_document_source") {
		t.Fatalf("DeleteSource's read: %s; want it answered by rag_document_source", plan)
	}
}
