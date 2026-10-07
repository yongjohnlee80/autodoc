package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// textsOf lists path's alive chunks' text hashes, in ord order.
func (e *env) textsOf(p string) [][]byte {
	e.t.Helper()
	rows, err := e.raw.QueryContext(context.Background(), `SELECT c.text_hash FROM chunk c JOIN document d ON d.id = c.doc_id
		WHERE d.path = ? AND c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen) ORDER BY c.ord`, p)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out [][]byte
	for rows.Next() {
		var h []byte
		if err := rows.Scan(&h); err != nil {
			e.t.Fatal(err)
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		e.t.Fatalf("%s has no alive chunk", p)
	}
	return out
}

// vectorsFor counts text h's vectors under model fp, or under any model when fp is "".
func (e *env) vectorsFor(h []byte, fp string) int {
	e.t.Helper()
	var n int
	q, args := "SELECT COUNT(*) FROM embedding WHERE text_hash = ?", []any{h}
	if fp != "" {
		q, args = q+" AND model_fp = ?", append(args, fp)
	}
	if err := scanOne(context.Background(), e.raw, &n, q, args...); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// seedVector adds text h's vector under model fp in workspace ws, with no chunk of its own.
func (e *env) seedVector(ws int64, fp string, h []byte) {
	e.t.Helper()
	if _, err := e.raw.ExecContext(context.Background(), "INSERT OR IGNORE INTO embedding (workspace_id, text_hash, model_fp, bits, f32) VALUES (?, ?, ?, x'00', x'00000000')",
		ws, h, fp); err != nil {
		e.t.Fatal(err)
	}
}

// seedTexts adds to workspace ws a document of n chunks whose texts are prefix-0 … prefix-(n-1),
// each with a vector under fp: vectors in use, by chunks no indexer read. Only for a workspace no
// indexer runs: one would revalidate the document, find no file, and remove it.
func (e *env) seedTexts(ws int64, fp, prefix string, n int) {
	e.t.Helper()
	ctx := context.Background()
	res, err := e.raw.ExecContext(ctx, "INSERT INTO document (workspace_id, path, version, active_gen, indexer, indexed_at) VALUES (?, ?, 'v', 1, 'seed', 0)",
		ws, "seed-"+prefix+".md")
	if err != nil {
		e.t.Fatal(err)
	}
	doc, err := res.LastInsertId()
	if err != nil {
		e.t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		h := []byte(fmt.Sprintf("%s-%05d", prefix, i))
		if _, err := e.raw.ExecContext(ctx, `INSERT INTO chunk (workspace_id, doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end)
			VALUES (?, ?, ?, ?, 1, ?, '', 'seed', '', '', 0, 0)`, ws, doc, h, h, i); err != nil {
			e.t.Fatal(err)
		}
		e.seedVector(ws, fp, h)
	}
}

// countVectors counts the workspace's vectors whose text starts with prefix.
func (e *env) countVectors(ws int64, prefix string) int {
	e.t.Helper()
	var n int
	if err := scanOne(context.Background(), e.raw, &n, "SELECT COUNT(*) FROM embedding WHERE workspace_id = ? AND substr(text_hash, 1, ?) = ?",
		ws, len(prefix), []byte(prefix)); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// orphanBatchOf sets orphanBatch for one test.
func orphanBatchOf(t *testing.T, n uint64) {
	old := orphanBatch
	orphanBatch = n
	t.Cleanup(func() { orphanBatch = old })
}

// TestAReplacedTextsVectorsGoAtGC (ADR 1791329335 §2.3, item 1): an edit that replaces a text leaves
// its vectors until gc deletes its dead chunk; then they go, under the active model and under a
// switch's target alike. A text another document still has keeps its vectors.
func TestAReplacedTextsVectorsGoAtGC(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	// one heading and body: one breadcrumb and text, so one text hash
	e.put("x.md", "# Same\n\nzebra\n", "s.md", "# Same\n\nzebra\n", "y.md", "hippo\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	zebra, hippo := e.textsOf("x.md")[0], e.textsOf("y.md")[0]
	if !bytes.Equal(zebra, e.textsOf("s.md")[0]) {
		t.Fatal("precondition: x.md and s.md do not share their text")
	}
	const target = "t|target" // a switch's target, as setupModels records it
	e.seedModel(e.ws, target, 0, 0, 1)
	e.seedVector(e.ws, target, zebra)
	e.seedVector(e.ws, target, hippo)

	e.put("x.md", "tiger\n", "y.md", "lion\n")
	e.eventually("gc took the dead chunks", func() bool {
		var dead int
		_ = scanOne(context.Background(), e.raw, &dead, "SELECT COUNT(*) FROM chunk WHERE gen_to IS NOT NULL")
		return dead == 0
	})
	if n := e.vectorsFor(hippo, ""); n != 0 {
		t.Errorf("the replaced text kept %d vectors (active and target)", n)
	}
	if e.vectorsFor(zebra, fpA) != 1 || e.vectorsFor(zebra, target) != 1 {
		t.Errorf("the text s.md still has lost vectors: active %d, target %d", e.vectorsFor(zebra, fpA), e.vectorsFor(zebra, target))
	}
}

// TestADeadChunksVectorStaysUntilGC: a replaced text's chunk is dead, not deleted, until gc runs, and
// its vector stays with it.
func TestADeadChunksVectorStaysUntilGC(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a, noGC: true})
	e.put("y.md", "hippo\n")
	e.ready()
	hippo := e.textsOf("y.md")[0]
	e.put("y.md", "lion\n")
	e.ready()
	if n := e.vectorsFor(hippo, a.Model().Fingerprint()); n != 1 {
		t.Errorf("the dead chunk's vector: %d, want 1 until gc", n)
	}
}

// TestARemovedDocumentsVectorsGoWithIt (§2.3, item 2): a document's removal deletes its own texts'
// vectors in its commit (gc is off: nothing else could), and keeps a text another document has.
func TestARemovedDocumentsVectorsGoWithIt(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a, noGC: true})
	e.put("x.md", "# Same\n\nzebra\n", "s.md", "# Same\n\nzebra\n", "y.md", "hippo\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	zebra, hippo := e.textsOf("x.md")[0], e.textsOf("y.md")[0]
	if !bytes.Equal(zebra, e.textsOf("s.md")[0]) {
		t.Fatal("precondition: x.md and s.md do not share their text")
	}
	e.remove("x.md", "y.md")
	if n := e.vectorsFor(hippo, fpA); n != 0 {
		t.Errorf("the removed document's text kept %d vectors", n)
	}
	if n := e.vectorsFor(zebra, fpA); n != 1 {
		t.Errorf("the text s.md still has: %d vectors, want 1", n)
	}
}

// TestALateVectorForAReplacedTextIsNotStored (§2.3, item 3): a batch of vectors stores those whose
// text a chunk has, and none for a text no chunk has.
func TestALateVectorForAReplacedTextIsNotStored(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	a.hold("lion") // lion's own embedding waits: the batch below is the one that stores it
	e.put("y.md", "lion\n")
	lion := e.textsOf("y.md")[0]
	gone := []byte("a text no chunk has")
	vb := vecBatch{fp: fpA, items: []vecItem{{textHash: lion, vec: make([]float32, 64)}, {textHash: gone, vec: make([]float32, 64)}}}
	if err := e.ix.handVectors(context.Background(), vb); err != nil {
		t.Fatal(err)
	}
	if e.vectorsFor(lion, fpA) != 1 || e.vectorsFor(gone, "") != 0 {
		t.Errorf("after the batch: lion %d (want 1), the orphan %d (want 0)", e.vectorsFor(lion, fpA), e.vectorsFor(gone, ""))
	}
	a.unhold()
}

// TestTheStartPassSweepsOrphansInBatches (§2.4): 5,000 orphans under the active model and the target
// go in batches, each a writer turn, a document committing between two of them before the last; the
// vectors chunks use stay.
func TestTheStartPassSweepsOrphansInBatches(t *testing.T) {
	orphanBatchOf(t, 1000)
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n")
	e.ready()
	fpA := a.Model().Fingerprint()
	const target = "t|target"
	e.seedModel(e.ws, target, 0, 0, 1)
	for i := 0; i < 2500; i++ {
		e.seedVector(e.ws, fpA, []byte(fmt.Sprintf("orphan-a-%05d", i)))
		e.seedVector(e.ws, target, []byte(fmt.Sprintf("orphan-t-%05d", i)))
	}
	var sections strings.Builder
	for i := 0; i < 10; i++ {
		fmt.Fprintf(&sections, "# Section %d\n\nword%d\n\n", i, i)
	}
	e.put("u.md", sections.String())
	used := e.textsOf("u.md")
	for _, h := range used {
		e.seedVector(e.ws, target, h) // the target's fill had reached them
	}

	turns, committedAt := 0, 0
	write := func(ctx context.Context, fn func(context.Context, *store.Tx) error) error {
		turns++
		if turns == 2 {
			e.put("n.md", "newt\n") // a document commits between two batches
			committedAt = turns
		}
		return e.ix.do(ctx, fn)
	}
	n, err := e.store.sweepOrphans(context.Background(), write)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5000 || e.countVectors(e.ws, "orphan-") != 0 {
		t.Errorf("swept %d, %d orphans left; want 5000, 0", n, e.countVectors(e.ws, "orphan-"))
	}
	// about 2,500 keys under each model (the orphans, and the texts chunks use), 1,000 a turn: 3 + 3
	if turns != 6 || committedAt == 0 || committedAt >= turns {
		t.Errorf("%d writer turns, the document committed before turn %d; want 6, before the last", turns, committedAt)
	}
	kept := 0
	for _, h := range used {
		kept += e.vectorsFor(h, target)
	}
	if z := e.vectorsFor(e.textsOf("x.md")[0], fpA); kept != len(used) || z != 1 {
		t.Errorf("the pass took vectors that chunks use: the target keeps %d of %d, x.md %d of 1", kept, len(used), z)
	}
	e.ready() // n.md, committed mid-pass, is embedded and kept
	if e.vectorsFor(e.textsOf("n.md")[0], fpA) != 1 {
		t.Error("the document committed mid-pass has no vector")
	}
}

// TestThePassIsBoundedByTheKeysItExamines: with no orphan at all, a batch still reads no more than
// orphanBatch keys: 2,500 used vectors take three turns, not one scan.
func TestThePassIsBoundedByTheKeysItExamines(t *testing.T) {
	orphanBatchOf(t, 1000)
	e := newEnv(t, Options{})
	w2, err := e.db.AddWorkspace(context.Background(), "other", "/other", nil, nil) // no indexer
	if err != nil {
		t.Fatal(err)
	}
	const fp = "u|used"
	e.seedModel(w2.ID, fp, 0, 1, 1)
	e.seedTexts(w2.ID, fp, "used", 2500)
	s2 := Open(e.db, w2.ID)
	turns := 0
	n, err := s2.sweepOrphans(context.Background(), func(ctx context.Context, fn func(context.Context, *store.Tx) error) error {
		turns++
		return s2.storeWrite(ctx, fn)
	})
	if err != nil || n != 0 || turns != 3 {
		t.Errorf("sweep: %d deleted, %d turns, %v; want 0, 3", n, turns, err)
	}
	if n := e.countVectors(w2.ID, "used-"); n != 2500 {
		t.Errorf("used vectors were taken: %d left of 2500", n)
	}
}

// TestAnInterruptedPassIsFinishedNext: a pass that fails at its second batch keeps the first's work
// and stops; the next pass finishes.
func TestAnInterruptedPassIsFinishedNext(t *testing.T) {
	orphanBatchOf(t, 1000)
	e := newEnv(t, Options{})
	const fp = "u|used"
	e.seedModel(e.ws, fp, 0, 1, 1)
	for i := 0; i < 2500; i++ {
		e.seedVector(e.ws, fp, []byte(fmt.Sprintf("orphan-%05d", i)))
	}
	e.exec(fmt.Sprintf("CREATE TRIGGER refuse BEFORE DELETE ON embedding WHEN old.text_hash = x'%x' BEGIN SELECT RAISE(ABORT, 'injected'); END", "orphan-01500"))
	if _, err := SweepOrphansStore(context.Background(), e.db, e.ws); err == nil || !strings.Contains(err.Error(), "injected") {
		t.Fatalf("the pass with its second batch refused: %v", err)
	}
	if left := e.countVectors(e.ws, "orphan-"); left != 1500 {
		t.Fatalf("after the failed pass: %d orphans, want 1500 (the first batch's work kept)", left)
	}
	e.exec("DROP TRIGGER refuse")
	if n, err := SweepOrphansStore(context.Background(), e.db, e.ws); err != nil || n != 1500 || e.countVectors(e.ws, "orphan-") != 0 {
		t.Errorf("the next pass: %d, %v, %d left", n, err, e.countVectors(e.ws, "orphan-"))
	}
}

// TestThePassReachesAWorkspaceWithNoIndexer: a stored workspace no indexer runs is swept through the
// store, keeping what its chunks use.
func TestThePassReachesAWorkspaceWithNoIndexer(t *testing.T) {
	e := newEnv(t, Options{})
	w2, err := e.db.AddWorkspace(context.Background(), "other", "/other", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	const fp = "w2|active"
	e.seedModel(w2.ID, fp, 0, 1, 0)
	e.seedTexts(w2.ID, fp, "used", 3)
	for i := 0; i < 4; i++ {
		e.seedVector(w2.ID, fp, []byte(fmt.Sprintf("orphan-%d", i)))
	}
	if n, err := SweepOrphansStore(context.Background(), e.db, w2.ID); err != nil || n != 4 {
		t.Fatalf("SweepOrphansStore: %d, %v; want 4", n, err)
	}
	if e.countVectors(w2.ID, "used-") != 3 || e.countVectors(w2.ID, "orphan-") != 0 {
		t.Errorf("after the pass: %d used (want 3), %d orphans", e.countVectors(w2.ID, "used-"), e.countVectors(w2.ID, "orphan-"))
	}
}

// TestAQueryHeldAcrossTheDeletionRescoresFromWhatItRead (§2.1, Lector r0/r1): a semantic query whose
// read transaction is open while an edit replaces its hit's text, gc deletes the dead chunk, and the
// chunk's vector goes, still validates and rescores that hit from its own snapshot.
func TestAQueryHeldAcrossTheDeletionRescoresFromWhatItRead(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n", "y.md", "hippo\n")
	e.ready()
	hippo := e.textsOf("y.md")[0]
	held := false
	e.ix.sem.beforeFetch = func() {
		if held {
			return
		}
		held = true
		e.write("y.md", "lion\n")
		e.ix.Touch("y.md")
		e.eventually("the vector deleted under the open query", func() bool { return e.vectorsFor(hippo, "") == 0 })
	}
	res := e.query("hippo", QueryOpts{Mode: ModeSemantic})
	e.ix.sem.beforeFetch = nil
	if !held {
		t.Fatal("the query never reached its fetch")
	}
	i := slices.IndexFunc(res.Hits, func(h Hit) bool { return h.Path == "y.md" })
	if i < 0 || !strings.Contains(res.Hits[i].Snippet, "hippo") || res.Hits[i].Score <= 0 {
		t.Fatalf("the held query's hits: %+v; want y.md's hippo chunk, rescored", res.Hits)
	}
	if e.vectorsFor(hippo, "") != 0 {
		t.Error("the vector came back")
	}
	if got := e.query("hippo", QueryOpts{Mode: ModeSemantic}); slices.ContainsFunc(got.Hits, func(h Hit) bool { return strings.Contains(h.Snippet, "hippo") }) {
		t.Errorf("a query after the deletion still found the old text: %+v", got.Hits)
	}
}

// sqlCapture keeps the SQL and arguments of every statement it sees, as rendered.
type sqlCapture struct {
	dao.NopHook
	stmts []dao.QueryInfo
}

func (c *sqlCapture) BeforeExec(_ context.Context, q *dao.QueryInfo) error {
	c.stmts = append(c.stmts, *q)
	return nil
}

// TestTheLookupsUseChunkText: the statements the writer renders for its checks, captured from dao,
// are planned through chunk_text, not a scan of the workspace's chunks through chunk_doc (no ANALYZE
// statistics, as in every store): referencedTexts (gc, removal, a vector batch, the start pass) and
// docsWithText (a vector batch's documents).
func TestTheLookupsUseChunkText(t *testing.T) {
	e := newEnv(t, Options{})
	ctx := context.Background()
	for name, run := range map[string]func(*Store, *store.Tx) error{
		"referencedTexts": func(s *Store, tx *store.Tx) error {
			_, err := s.referencedTexts(tx, [][]byte{[]byte("a"), []byte("b")})
			return err
		},
		"docsWithText": func(s *Store, tx *store.Tx) error {
			_, err := s.docsWithText(tx, []vecItem{{textHash: []byte("a")}, {textHash: []byte("b")}})
			return err
		},
	} {
		capture := &sqlCapture{}
		s := &Store{db: e.db, sc: e.db.Workspace(e.ws).WithHooks(capture)}
		if err := e.db.Read(ctx, func(tx *store.Tx) error { return run(s, tx) }); err != nil {
			t.Fatal(name, err)
		}
		if len(capture.stmts) != 1 {
			t.Fatalf("%s rendered %d statements, want 1", name, len(capture.stmts))
		}
		q := capture.stmts[0]
		rows, err := e.raw.QueryContext(ctx, "EXPLAIN QUERY PLAN "+q.SQL, q.Args...)
		if err != nil {
			t.Fatal(name, err)
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
		rows.Close()
		if !strings.Contains(strings.Join(plan, "\n"), "INDEX chunk_text") {
			t.Errorf("%s: %s\n%s", name, q.SQL, strings.Join(plan, "\n"))
		}
	}
}

// TestABatchsReadinessLooksUpOnlyItsTexts (§2.6): setReady for a few documents, looking their texts
// up by key, marks them as setReady for every document does, with the model's vectors read whole.
// Some texts have a vector only under another model, which must not count.
func TestABatchsReadinessLooksUpOnlyItsTexts(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	words := []string{"zebra", "hippo", "lion", "tiger", "newt", "otter", "heron", "bison"}
	for _, w := range words {
		e.put(w+".md", "# "+w+"\n\n"+w+"\n\n## more\n\n"+w+" again\n")
	}
	e.ready()
	fpA := a.Model().Fingerprint()
	ctx := context.Background()
	e.stop() // no writer from here: the two computations read one state
	db, err := store.Open(ctx, e.dir+"/autodoc.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := Open(db, e.ws)
	// lion, otter and bison lose one text's vector under a, which another model has
	e.exec("INSERT INTO model (workspace_id, fp, provider, name, dims, active, target) VALUES (?, 'other|x', 'fake', 'x', 4, 0, 1)", e.ws)
	for _, w := range []string{"lion", "otter", "bison"} {
		h := e.textsOf(w + ".md")[1]
		e.exec("DELETE FROM embedding WHERE text_hash = ? AND model_fp = ?", h, fpA)
		e.seedVector(e.ws, "other|x", h)
	}
	readiness := func(docs []int64) map[int64]int64 {
		out := map[int64]int64{}
		rollback := errors.New("rollback")
		err := db.Write(ctx, func(tx *store.Tx) error {
			if err := s.setReady(tx, docs); err != nil {
				return err
			}
			rows, err := s.sc.Documents(tx).Select(store.DocID, store.DocPath, store.DocSemanticReady)
			if err != nil {
				return err
			}
			for _, r := range rows {
				out[r.ID] = r.SemanticReady
			}
			return rollback
		})
		if !errors.Is(err, rollback) {
			t.Fatal(err)
		}
		return out
	}
	all := readiness(nil)
	var ids []int64
	for id := range all {
		ids = append(ids, id)
	}
	// both reads: by key, and (past readinessLookups sections) the model's every vector
	for _, bound := range []int{inPart, 0} {
		old := readinessLookups
		readinessLookups = bound
		for _, pick := range [][]int64{ids[:3], ids[3:], ids} {
			some := readiness(pick)
			for _, id := range pick {
				if some[id] != all[id] {
					t.Errorf("bound %d, document %d: ready %d by its own texts, %d from every vector", bound, id, some[id], all[id])
				}
			}
		}
		readinessLookups = old
	}
	unready := 0
	for _, v := range all {
		if v == 0 {
			unready++
		}
	}
	if unready != 3 {
		t.Errorf("%d documents unready, want the 3 missing a vector under the active model", unready)
	}
}

// TestAFailedVectorDeleteRollsBackItsChunks (§2.3): the vectors go in the transaction that deletes
// their last chunk, so a refused vector delete keeps the chunk too: gc's dead chunk stays dead and
// counted, a removed document stays indexed, both with their vectors. Once the failure lifts, both
// go with their vectors.
func TestAFailedVectorDeleteRollsBackItsChunks(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n", "y.md", "hippo\n")
	e.ready()
	zebra, hippo := e.textsOf("x.md")[0], e.textsOf("y.md")[0]
	dead := func() int {
		var n int
		_ = scanOne(context.Background(), e.raw, &n, "SELECT COUNT(*) FROM chunk WHERE gen_to IS NOT NULL")
		return n
	}
	settle := func() { time.Sleep(200 * time.Millisecond) } // gc and the writer try, and fail, several times

	undo := e.failWrites("embedding", "DELETE")
	e.put("y.md", "lion\n") // the edit commits: it deletes no vector
	settle()
	if dead() != 1 || e.vectorsFor(hippo, "") != 1 {
		t.Errorf("gc with the vector delete refused: %d dead chunks (want 1), %d vectors (want 1)", dead(), e.vectorsFor(hippo, ""))
	}
	if err := e.fsys.Remove(context.Background(), "x.md"); err != nil {
		t.Fatal(err)
	}
	e.ix.Touch("x.md")
	settle()
	if _, ok := e.store.Version("x.md"); !ok || e.vectorsFor(zebra, "") != 1 {
		t.Errorf("the removal with the vector delete refused: indexed %v (want true), %d vectors (want 1)", ok, e.vectorsFor(zebra, ""))
	}

	undo()
	e.eventually("the removal and gc once the failure lifts", func() bool {
		_, ok := e.store.Version("x.md")
		return !ok && dead() == 0 && e.vectorsFor(zebra, "") == 0 && e.vectorsFor(hippo, "") == 0
	})
}

// TestGCTakesMoreThanOneBatch: 1,001 dead chunks take two gc transactions of at most 1,000, and the
// vectors of their texts go with them, in each.
func TestGCTakesMoreThanOneBatch(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	var before, after strings.Builder
	for i := 0; i < 1001; i++ {
		fmt.Fprintf(&before, "# s%d\n\nold%d\n\n", i, i)
		fmt.Fprintf(&after, "# s%d\n\nnew%d\n\n", i, i)
	}
	e.put("big.md", before.String())
	e.ready()
	old := e.textsOf("big.md")
	e.put("big.md", after.String())
	e.eventually("gc took every dead chunk", func() bool {
		var dead int
		_ = scanOne(context.Background(), e.raw, &dead, "SELECT COUNT(*) FROM chunk WHERE gen_to IS NOT NULL")
		return dead == 0
	})
	left := 0
	for _, h := range old {
		left += e.vectorsFor(h, "")
	}
	if left != 0 {
		t.Errorf("%d of the %d replaced texts' vectors left after gc", left, len(old))
	}
}

// TestARemovalWhoseChunksCannotBeReadIsRetried: a removal that cannot read its chunks' texts writes
// nothing (the document stays, with its vectors) and runs again once they can be read.
func TestARemovalWhoseChunksCannotBeReadIsRetried(t *testing.T) {
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a})
	e.put("x.md", "zebra\n")
	e.ready()
	zebra := e.textsOf("x.md")[0]
	e.exec(`ALTER TABLE chunk RENAME COLUMN text_hash TO text_hash_aside`)
	if err := e.fsys.Remove(context.Background(), "x.md"); err != nil {
		t.Fatal(err)
	}
	e.ix.Touch("x.md")
	time.Sleep(200 * time.Millisecond) // the writer tries, and fails, several times
	if _, ok := e.store.Version("x.md"); !ok {
		t.Error("the removal went through without its chunks' texts")
	}
	e.exec(`ALTER TABLE chunk RENAME COLUMN text_hash_aside TO text_hash`)
	e.eventually("the removal once the texts can be read", func() bool {
		_, ok := e.store.Version("x.md")
		return !ok && e.vectorsFor(zebra, "") == 0
	})
}

// TestDoAwaitsAnOpItsWriterTook: a caller whose ctx ends while the writer runs its op waits for the
// op, since the op writes the caller's variables (a reclaim's or a sweep's counts and cursor); before,
// do returned on ctx.Done, and the caller read them under the writer (a race -race caught).
func TestDoAwaitsAnOpItsWriterTook(t *testing.T) {
	e := newEnv(t, Options{})
	ctx, cancel := context.WithCancel(context.Background())
	entered, release := make(chan struct{}), make(chan struct{})
	wrote := false
	returned := make(chan error, 1)
	go func() {
		returned <- e.ix.do(ctx, func(context.Context, *store.Tx) error {
			close(entered)
			<-release
			wrote = true
			return nil
		})
	}()
	<-entered
	cancel()
	select {
	case <-returned:
		close(release) // let the writer finish, so the env stops
		t.Fatal("do returned while the writer still ran its op")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-returned; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !wrote {
		t.Error("the op's write is not visible to its caller")
	}
}
