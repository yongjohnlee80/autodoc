package pgstore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/search"
)

// Pending with Edits lists the texts of the documents edited since their first write and not
// ready, from the start whatever After says, and nothing of the documents written once: a backlog
// of first writes costs it nothing, as its plan reports (the documents it reads are the edited
// ones, through their own partial index, never a sequential scan). The mutant, named here: the
// edits read through the unready documents' predicate (the whole backlog read, and listed).
func TestPendingEditsReadsOnlyTheEditedDocuments(t *testing.T) {
	ctx := context.Background()
	conn := indexedScratch(t)
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	put := func(i int, body string) {
		t.Helper()
		if _, err := s.Put(ctx, Document{Path: fmt.Sprintf("d%04d.md", i), Chunks: []search.Chunk{{Ord: 0, Breadcrumb: fmt.Sprintf("d%d", i), Body: body}}}); err != nil {
			t.Fatal(err)
		}
	}
	const docs = 200
	for i := range docs {
		put(i, "first words")
	}
	edited := []int{3, 50, 150}
	want := map[[32]byte]bool{}
	for _, i := range edited {
		body := fmt.Sprintf("edited words of %d", i)
		put(i, body)
		want[search.Chunk{Ord: 0, Breadcrumb: fmt.Sprintf("d%d", i), Body: body}.TextHash()] = true
	}
	exec(t, conn, "ANALYZE rag_document, rag_chunk, rag_embedding")
	rec := &statements{}
	pendingHook = rec
	t.Cleanup(func() { pendingHook = nil })
	texts, _, err := s.Pending(ctx, PendingQuery{Model: "m", Edits: true, After: PendingKey{Doc: 1 << 40}, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	got := map[[32]byte]bool{}
	for _, x := range texts {
		got[x.TextHash] = true
	}
	if len(texts) != len(edited) || len(got) != len(want) {
		t.Fatalf("listed %d texts, want the %d edited documents' alone", len(texts), len(edited))
	}
	for h := range want {
		if !got[h] {
			t.Errorf("an edited document's text is not listed")
		}
	}
	read := newPlanRead()
	for _, q := range rec.got {
		explain(t, conn, q, read)
	}
	if read.rows["rag_document"] > float64(len(edited)) || slices.Contains(read.scans["rag_document"], "Seq Scan") {
		t.Errorf("the edits read %v of the documents (scans %v): want the %d edited ones", read.rows, read.scans, len(edited))
	}
	t.Logf("the edits read %v; scans %v; statements:\n%s", read.rows, read.scans, sqlOf(rec.got))
}

// Migration 000011 adds the partial index of the edited unready documents, and its revert takes it
// away.
func TestMigration000011IndexesTheEditedUnreadyDocuments(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	def := func() string {
		return scalar(t, conn, `SELECT coalesce((SELECT indexdef FROM pg_indexes WHERE indexname = 'rag_document_edited_unready'), '')`)
	}
	if got := def(); !strings.Contains(got, "WHERE ((NOT semantic_ready) AND (generation > 1))") {
		t.Fatalf("the edited documents' index: %q", got)
	}
	if _, err := deploy.New(migrations, deploy.Ledger(Ledger)).Revert(ctx, conn, 11); err != nil {
		t.Fatal(err)
	}
	if got := def(); got != "" {
		t.Errorf("after the revert: %q", got)
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if def() == "" {
		t.Error("migrated again: no index")
	}
}
