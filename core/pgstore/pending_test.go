package pgstore

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
)

// planRead is what statements read, by table, as their plans report it: the rows each scan of the
// table read (those it returned and those its filters removed, over every loop), and the kinds of
// scan used.
type planRead struct {
	rows  map[string]float64
	scans map[string][]string
}

func newPlanRead() *planRead {
	return &planRead{rows: map[string]float64{}, scans: map[string][]string{}}
}

// explain runs q again under EXPLAIN ANALYZE on conn, against the same data, and adds what its plan
// read to into.
func explain(t *testing.T, conn dao.DataConn, q dao.QueryInfo, into *planRead) {
	t.Helper()
	rows, err := conn.QueryContext(context.Background(), "EXPLAIN (ANALYZE, FORMAT JSON) "+q.SQL, q.Args...)
	if err != nil {
		t.Fatalf("explain %s: %v", q.SQL, err)
	}
	var plan string
	if !rows.Next() {
		t.Fatalf("explain %s: no plan", q.SQL)
	}
	if err := rows.Scan(&plan); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var top []struct{ Plan map[string]any }
	if err := json.Unmarshal([]byte(plan), &top); err != nil || len(top) != 1 {
		t.Fatalf("the plan %s: %v", plan, err)
	}
	num := func(n map[string]any, k string) float64 {
		v, _ := n[k].(float64)
		return v
	}
	var walk func(n map[string]any)
	walk = func(n map[string]any) {
		if rel, ok := n["Relation Name"].(string); ok {
			read := num(n, "Actual Rows") + num(n, "Rows Removed by Filter") + num(n, "Rows Removed by Index Recheck")
			into.rows[rel] += read * num(n, "Actual Loops")
			if kind, _ := n["Node Type"].(string); !slices.Contains(into.scans[rel], kind) {
				into.scans[rel] = append(into.scans[rel], kind)
			}
		}
		children, _ := n["Plans"].([]any)
		for _, c := range children {
			if m, ok := c.(map[string]any); ok {
				walk(m)
			}
		}
	}
	walk(top[0].Plan)
}

// Pending pages a tenant without reading it again: a worker paging with next through more
// chunks than many pages reads each chunk, each vector probe and each unready document about
// once, as every statement's plan reports, in both scans; it lists every distinct text exactly
// once, skips what it is told to, and once the tenant is ready a new document costs only its own
// rows, not the tenant's. The plans use the indexes, never a sequential scan of the chunks or the
// documents. The mutants, named here: the scan starting from the beginning on every call, whatever
// After says (the rows read grow with the square of the tenant); and the unready scan reading the
// documents without the unready predicate (a new document costs every document).
//
// The tenant is small, and at its size PostgreSQL rightly prefers reading a table whole to an
// index; the connection turns sequential scans off, so the plans are the ones a large tenant gets
// and the rows they read measure Pending's access pattern. (The plans at scale are checked by hand
// with EXPLAIN; see the pull request.) A sequential scan that remains is one no index could answer.
func TestPendingReadsEachRowAboutOnce(t *testing.T) {
	ctx := context.Background()
	conn := indexedScratch(t)
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	wasPage, wasDocs := pendingPage, pendingDocs
	pendingPage, pendingDocs = 40, 16
	t.Cleanup(func() { pendingPage, pendingDocs = wasPage, wasDocs })

	const docs, perDoc = 300, 4
	put := func(i int) {
		t.Helper()
		var chunks []search.Chunk
		for j := 0; j < perDoc-1; j++ {
			chunks = append(chunks, search.Chunk{Ord: j, Breadcrumb: fmt.Sprintf("d%d", i), Body: fmt.Sprintf("words %d of %d", j, i)})
		}
		// every two documents share their last text
		chunks = append(chunks, search.Chunk{Ord: perDoc - 1, Breadcrumb: "shared", Body: fmt.Sprintf("pair %d", i/2)})
		if _, err := s.Put(ctx, Document{Path: fmt.Sprintf("d%04d.md", i), Chunks: chunks}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < docs; i++ {
		put(i)
	}
	distinct := docs*(perDoc-1) + docs/2
	skipped := search.Chunk{Ord: 0, Breadcrumb: "d7", Body: "words 0 of 7"}.TextHash()
	exec(t, conn, "ANALYZE rag_document, rag_chunk, rag_embedding")

	rec := &statements{}
	pendingHook = rec
	t.Cleanup(func() { pendingHook = nil })

	// fill pages a model through the tenant as a worker does, storing each call's vectors before
	// the next, until a scan from the start finds nothing: what the plans read, the calls, and how
	// many times each text was listed
	fill := func(m string, unready bool) (*planRead, int, map[[32]byte]int) {
		t.Helper()
		read, listed, calls := newPlanRead(), map[[32]byte]int{}, 0
		var cur PendingKey
		for {
			rec.got = nil
			from := cur
			texts, next, err := s.Pending(ctx, PendingQuery{Model: m, After: cur, Unready: unready,
				Skip: map[[32]byte]bool{skipped: true}, Limit: 25})
			if err != nil {
				t.Fatal(err)
			}
			for _, q := range rec.got {
				explain(t, conn, q, read)
			}
			calls++
			var vecs []Embedding
			for _, x := range texts {
				listed[x.TextHash]++
				vecs = append(vecs, Embedding{TextHash: x.TextHash, Vec: embedVec(x.Text)})
			}
			if err := s.Embed(ctx, m, vecs); err != nil {
				t.Fatal(err)
			}
			cur = next
			if len(texts) == 0 && from.IsZero() {
				return read, calls, listed
			}
			if calls > 1000 {
				t.Fatal("Pending never reached the end")
			}
		}
	}
	check := func(name string, read *planRead, calls int, listed map[[32]byte]int, docBound float64) {
		t.Helper()
		if len(listed) != distinct-1 || listed[skipped] != 0 {
			t.Errorf("%s: %d distinct texts listed, want %d (the skipped one never)", name, len(listed), distinct-1)
		}
		for _, n := range listed {
			if n != 1 {
				t.Errorf("%s: a text listed %d times", name, n)
				break
			}
		}
		chunks := float64(docs * perDoc)
		// each chunk read about once: the page a limit stops part way (a page of chunks, or the
		// chunks of a page of documents) is read again by the next call
		again := float64(calls * max(pendingPage, pendingDocs*perDoc))
		if bound := 2*chunks + again; read.rows["rag_chunk"] > bound {
			t.Errorf("%s: %v chunk rows read over %d calls, more than %v for %v chunks", name, read.rows["rag_chunk"], calls, bound, chunks)
		}
		if bound := 2*chunks + again; read.rows["rag_embedding"] > bound {
			t.Errorf("%s: %v vector rows read over %d calls, more than %v", name, read.rows["rag_embedding"], calls, bound)
		}
		if read.rows["rag_document"] > docBound {
			t.Errorf("%s: %v document rows read over %d calls, more than %v", name, read.rows["rag_document"], calls, docBound)
		}
		for _, rel := range []string{"rag_chunk", "rag_document"} {
			if slices.Contains(read.scans[rel], "Seq Scan") {
				t.Errorf("%s: %s read by a sequential scan: %v", name, rel, read.scans)
			}
		}
		t.Logf("%s: %d calls read %v; scans %v", name, calls, read.rows, read.scans)
	}

	read, calls, listed := fill("all|m||8", false)
	check("every chunk", read, calls, listed, 0)
	read, calls, listed = fill("unready|m||8", true)
	check("unready documents", read, calls, listed, float64(2*docs+calls*pendingDocs))

	// the tenant ready: a new document costs its own rows
	exec(t, conn, "UPDATE rag_document SET semantic_ready = true")
	exec(t, conn, "ANALYZE rag_document")
	put(docs)
	rec.got = nil
	texts, next, err := s.Pending(ctx, PendingQuery{Model: "unready|m||8", Unready: true, Limit: 25})
	if err != nil || len(texts) != perDoc || !next.IsZero() {
		t.Fatalf("the new document's texts: %d, next %+v, %v; want its %d, and the end", len(texts), next, err, perDoc)
	}
	one := newPlanRead()
	for _, q := range rec.got {
		explain(t, conn, q, one)
	}
	if one.rows["rag_document"] > 2 || one.rows["rag_chunk"] > 2*perDoc || slices.Contains(one.scans["rag_document"], "Seq Scan") {
		t.Errorf("one new document in a ready tenant read %v (scans %v): want its own rows", one.rows, one.scans)
	}
	t.Logf("a new document: read %v; scans %v; statements:\n%s", one.rows, one.scans, sqlOf(rec.got))
}

// indexedScratch is a migrated scratch database on a connection with sequential scans off: a small
// tenant then gets the plans a large one does, so the rows they read measure an access pattern.
func indexedScratch(t *testing.T) dao.DataConn {
	t.Helper()
	ctx := context.Background()
	admin, err := postgres.Open(ctx, dsnBase(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { admin.Close() })
	u, err := url.Parse(dsnOf(t, admin, scratchName(t, admin, "autodoc_pgstore_")))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("enable_seqscan", "off")
	u.RawQuery = q.Encode()
	conn, err := postgres.Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	return conn
}

func sqlOf(qs []dao.QueryInfo) string {
	var b strings.Builder
	for _, q := range qs {
		b.WriteString(q.SQL + "\n")
	}
	return b.String()
}

// Migration 000003 adds the partial indexes of unready documents, and its revert takes them away.
func TestMigration000003IndexesTheUnreadyDocuments(t *testing.T) {
	conn := scratch(t)
	ctx := context.Background()
	indexes := func() string {
		return scalar(t, conn, `SELECT coalesce(string_agg(indexname, ',' ORDER BY indexname), '') FROM pg_indexes
			WHERE tablename = 'rag_document' AND indexname LIKE 'rag_document_unready%'`)
	}
	if got := indexes(); got != "rag_document_unready_id,rag_document_unready_path" {
		t.Fatalf("the unready indexes: %q", got)
	}
	if !strings.Contains(scalar(t, conn, `SELECT indexdef FROM pg_indexes WHERE indexname = 'rag_document_unready_id'`), "WHERE (NOT semantic_ready)") {
		t.Error("rag_document_unready_id is not partial on the unready documents")
	}
	if _, err := deploy.New(migrations, deploy.Ledger(Ledger)).Revert(ctx, conn, 3); err != nil {
		t.Fatal(err)
	}
	if got := indexes(); got != "" {
		t.Errorf("after the revert: %q", got)
	}
	if err := Migrate(ctx, conn); err != nil {
		t.Fatal(err)
	}
	if got := indexes(); got != "rag_document_unready_id,rag_document_unready_path" {
		t.Errorf("migrated again: %q", got)
	}
}

// pendingRows records the statements Pending runs and the rows they returned.
type pendingRows struct {
	dao.NopHook
	got      []dao.QueryInfo
	returned int
}

func (r *pendingRows) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	r.got = append(r.got, dao.QueryInfo{Op: q.Op, Table: q.Table, SQL: q.SQL, Args: slices.Clone(q.Args)})
	return nil
}

func (r *pendingRows) AfterExec(_ context.Context, _ *dao.QueryInfo, out dao.Outcome) error {
	r.returned += max(out.Rows, 0)
	return out.Err
}

// One unready document of many chunks is read only as far as the limit needs: with a limit of
// one, Pending's statements read and return a row or two, from the start and resumed part way
// through the document, never the document's every chunk. The mutant, named here: the unready
// scan's chunk statement with no LIMIT (every chunk of the document read and sent to Go).
func TestPendingReadsAManyChunkDocumentOnlyAsFarAsItsLimit(t *testing.T) {
	ctx := context.Background()
	conn := indexedScratch(t)
	s, err := New(conn, "t")
	if err != nil {
		t.Fatal(err)
	}
	const chunks = 2000
	big := make([]search.Chunk, chunks)
	for i := range big {
		big[i] = search.Chunk{Ord: i, Breadcrumb: "big", Body: fmt.Sprintf("section %d", i)}
	}
	if _, err := s.Put(ctx, Document{Path: "big.md", Chunks: big}); err != nil {
		t.Fatal(err)
	}
	exec(t, conn, "ANALYZE rag_document, rag_chunk, rag_embedding")
	rec := &pendingRows{}
	pendingHook = rec
	t.Cleanup(func() { pendingHook = nil })
	var doc int64
	for _, after := range []int{-1, 1000} {
		var from PendingKey
		if after >= 0 {
			from = PendingKey{Doc: doc, Ord: after}
		}
		rec.got, rec.returned = nil, 0
		texts, next, err := s.Pending(ctx, PendingQuery{Model: model, After: from, Unready: true, Limit: 1})
		if err != nil || len(texts) != 1 || next.Ord != after+1 {
			t.Fatalf("after %d: %d texts, next %+v, %v; want the chunk after it", after, len(texts), next, err)
		}
		doc = next.Doc
		read := newPlanRead()
		for _, q := range rec.got {
			explain(t, conn, q, read)
		}
		if read.rows["rag_chunk"] > 2 || rec.returned > 3 {
			t.Errorf("after %d, a limit of one: %v chunk rows read and %d rows returned, of a %d-chunk document; statements:\n%s",
				after, read.rows["rag_chunk"], rec.returned, chunks, sqlOf(rec.got))
		}
	}
}
