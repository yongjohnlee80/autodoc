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
// ready, and nothing of the documents written once: a backlog
// of first writes costs it nothing, as its plan reports (the documents it reads are the edited
// ones, through their own partial index, never a sequential scan). The mutant, named here: the
// edits read through the unready documents' predicate (the whole backlog read, and listed).
func TestPendingEditsReadsOnlyTheEditedDocuments(t *testing.T) {
	ctx := context.Background()
	for _, l := range layouts {
		t.Run(l.name, func(t *testing.T) {
			conn := l.open(t, true)
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
			texts, _, err := s.Pending(ctx, PendingQuery{Model: "m", Edits: true, Limit: 100})
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
		})
	}
}

// Migration 000011 adds the partial index of the edited unready documents, and its revert takes it
// away.
func TestMigration000011IndexesTheEditedUnreadyDocuments(t *testing.T) {
	for _, l := range layouts {
		t.Run(l.name, func(t *testing.T) {
			conn := l.open(t, false)
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
		})
	}
}

// Edited documents whose pending texts are all skipped (set aside by the worker) yield nothing and
// stay edited, so a read of the edits that looked through all of them would cost every call a read
// of every one. With MaxDocs a call looks through at most that many and stops at the last, and a
// caller paging with next passes them a bounded number at a time and reaches the edit behind them.
// The scale: 300 such documents, the statements of each call counted. The mutant, named here:
// MaxDocs ignored (one call reads all 300, in some 40 statements).
func TestPendingEditsLooksThroughABoundedNumberOfDocuments(t *testing.T) {
	ctx := context.Background()
	for _, l := range layouts {
		t.Run(l.name, func(t *testing.T) {
			conn := l.open(t, false)
			s, err := New(conn, "t")
			if err != nil {
				t.Fatal(err)
			}
			put := func(i int, body string) [32]byte {
				t.Helper()
				c := search.Chunk{Ord: 0, Breadcrumb: fmt.Sprintf("d%d", i), Body: body}
				if _, err := s.Put(ctx, Document{Path: fmt.Sprintf("d%04d.md", i), Chunks: []search.Chunk{c}}); err != nil {
					t.Fatal(err)
				}
				return c.TextHash()
			}
			const setAside, maxDocs, limit = 300, 64, 8
			skip := map[[32]byte]bool{}
			for i := range setAside {
				put(i, "first words")
				skip[put(i, fmt.Sprintf("a text set aside, %d", i))] = true
			}
			put(setAside, "first words")
			want := put(setAside, "the edit behind them")
			rec := &statements{}
			pendingHook = rec
			t.Cleanup(func() { pendingHook = nil })

			// unbounded, as before: one call reaches the edit, reading every set-aside document
			texts, _, err := s.Pending(ctx, PendingQuery{Model: "m", Edits: true, Skip: skip, Limit: limit})
			if err != nil {
				t.Fatal(err)
			}
			if len(texts) != 1 || texts[0].TextHash != want {
				t.Fatalf("unbounded: listed %d texts, want the edit's alone", len(texts))
			}
			unbounded := len(rec.got)

			// bounded: each call one page of documents and the chunk pages for them
			most := 1 + (maxDocs+limit-1)/limit + 1
			var next PendingKey
			calls := 0
			for found := false; !found; {
				if calls++; calls > setAside/maxDocs+2 {
					t.Fatalf("%d calls and the edit not reached", calls-1)
				}
				rec.got = nil
				texts, next, err = s.Pending(ctx, PendingQuery{Model: "m", Edits: true, After: next, MaxDocs: maxDocs, Skip: skip, Limit: limit})
				if err != nil {
					t.Fatal(err)
				}
				if n := len(rec.got); n > most {
					t.Fatalf("call %d ran %d statements: want at most %d, one page of %d documents", calls, n, most, maxDocs)
				}
				for _, x := range texts {
					if x.TextHash != want {
						t.Fatal("listed a skipped text")
					}
					found = true
				}
				if !found && next.IsZero() {
					t.Fatalf("call %d reached the end without the edit", calls)
				}
			}
			if unbounded <= most {
				t.Errorf("the unbounded read ran %d statements, no more than a bounded call's %d: the cell measures nothing", unbounded, most)
			}
			t.Logf("unbounded: one call, %d statements; bounded at %d documents: %d calls, each at most %d statements", unbounded, maxDocs, calls, most)
		})
	}
}
