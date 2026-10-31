package index

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// failWrites makes every op (INSERT, UPDATE or DELETE) on table fail, as a full disk or a
// corrupted page would; undo lifts it.
func (e *env) failWrites(table, op string) (undo func()) {
	e.t.Helper()
	name := fmt.Sprintf("fail_%s_%s", table, strings.ToLower(op))
	e.exec(fmt.Sprintf(`CREATE TRIGGER %s BEFORE %s ON "%s" BEGIN SELECT RAISE(ABORT, 'injected failure'); END`, name, op, table))
	return func() { e.exec("DROP TRIGGER " + name) }
}

// renamed moves table aside, so every read of it fails; undo puts it back.
func (e *env) renamed(table string) (undo func()) {
	e.t.Helper()
	e.exec(fmt.Sprintf(`ALTER TABLE "%s" RENAME TO "%s_aside"`, table, table))
	return func() { e.exec(fmt.Sprintf(`ALTER TABLE "%s_aside" RENAME TO "%s"`, table, table)) }
}

// countRefusedCommits counts the commits an injected failure (failWrites) refuses, so a test waits
// for the failure to be met instead of sleeping past it. Called before the test's env is made: its
// reset runs after the env's cleanup has stopped the indexer.
func countRefusedCommits(t *testing.T) *atomic.Int64 {
	t.Helper()
	var n atomic.Int64
	commitFailed = func(err error) {
		if strings.Contains(err.Error(), "injected failure") {
			n.Add(1)
		}
	}
	t.Cleanup(func() { commitFailed = nil })
	return &n
}

// TestAFailedCommitWritesNothingAndIsRetried: when the writer's transaction fails (any table's
// write refused), nothing of the batch is written, and its jobs run again: once the failure lifts,
// every change lands.
func TestAFailedCommitWritesNothingAndIsRetried(t *testing.T) {
	failed := countRefusedCommits(t)
	e := newEnv(t, options{})
	e.put("keep.md", "---\ntags: [old]\n---\n# Keep\n\nfirst words [[other]]\n", "gone.md", "to be removed\n")
	version := func(p string) string { v, _, _ := e.store.Version(context.Background(), p); return string(v) }
	for _, c := range []struct {
		name, table, op string
		act             func()
		unchanged       func() bool
		landed          func()
	}{
		{"a new document's row", "document", "INSERT",
			func() { e.write("new1.md", "fresh\n"); e.ix.Touch("new1.md") },
			func() bool { _, ok, _ := e.store.Version(context.Background(), "new1.md"); return !ok },
			func() { e.indexedAt("new1.md") }},
		{"a new document's chunks", "chunk", "INSERT",
			func() { e.write("new2.md", "fresh words\n"); e.ix.Touch("new2.md") },
			func() bool { _, ok, _ := e.store.Version(context.Background(), "new2.md"); return !ok },
			func() { e.indexedAt("new2.md") }},
		{"a changed document's row", "document", "UPDATE",
			func() { e.write("keep.md", "# Keep\n\nsecond words\n"); e.ix.Touch("keep.md") },
			func() bool { return len(e.match("second")) == 0 },
			func() { e.indexedAt("keep.md") }},
		{"its old chunks' end", "chunk", "UPDATE",
			func() { e.write("keep.md", "# Keep\n\nthird words\n"); e.ix.Touch("keep.md") },
			func() bool { return len(e.match("third")) == 0 },
			func() { e.indexedAt("keep.md") }},
		{"its tags", "doc_tag", "INSERT",
			func() { e.write("keep.md", "---\ntags: [fresh]\n---\n# Keep\n\nfourth words\n"); e.ix.Touch("keep.md") },
			func() bool { return len(e.match("fourth")) == 0 },
			func() { e.indexedAt("keep.md") }},
		{"its old tags' removal", "doc_tag", "DELETE",
			func() { e.write("keep.md", "---\ntags: [newer]\n---\n# Keep\n\nsixth words\n"); e.ix.Touch("keep.md") },
			func() bool { return len(e.match("sixth")) == 0 },
			func() { e.indexedAt("keep.md") }},
		{"its links", "link", "INSERT",
			func() { e.write("keep.md", "# Keep\n\nfifth words [[gone]]\n"); e.ix.Touch("keep.md") },
			func() bool { return len(e.match("fifth")) == 0 },
			func() { e.indexedAt("keep.md") }},
		{"the change log", "change", "INSERT",
			func() { e.write("new3.md", "logged\n"); e.ix.Touch("new3.md") },
			func() bool { _, ok, _ := e.store.Version(context.Background(), "new3.md"); return !ok },
			func() { e.indexedAt("new3.md") }},
		{"the workspace's counters", "workspace", "UPDATE",
			func() { e.write("new4.md", "counted\n"); e.ix.Touch("new4.md") },
			func() bool { _, ok, _ := e.store.Version(context.Background(), "new4.md"); return !ok },
			func() { e.indexedAt("new4.md") }},
		{"its job's completion", "index_job", "DELETE",
			func() { e.write("new5.md", "finished\n"); e.ix.Touch("new5.md") },
			func() bool { _, ok, _ := e.store.Version(context.Background(), "new5.md"); return !ok },
			func() { e.indexedAt("new5.md") }},
		{"a removed document's row", "document", "DELETE",
			func() { _ = e.fsys.Remove(context.Background(), "gone.md"); e.ix.Touch("gone.md") },
			func() bool { return version("gone.md") != "" },
			func() { e.eventually("gone.md gone", func() bool { return version("gone.md") == "" }) }},
	} {
		undo := e.failWrites(c.table, c.op)
		before := failed.Load()
		c.act()
		e.eventually(c.name+": a commit refused", func() bool { return failed.Load() > before })
		if !c.unchanged() {
			undo()
			t.Fatalf("%s: a failed commit wrote part of its batch", c.name)
		}
		undo()
		c.landed()
	}
}

// TestAFailedVectorCommitIsRetried: vectors whose commit fails are not stored, and the document
// stays not ready; once the failure lifts, the worker embeds again and the document becomes ready.
func TestAFailedVectorCommitIsRetried(t *testing.T) {
	failed := countRefusedCommits(t)
	p := newFake("m", "a")
	e := newEnv(t, options{Provider: p})
	e.put("a.md", "zebra plains\n")
	e.ready()
	undo := e.failWrites("embedding", "INSERT")
	before := failed.Load()
	e.put("b.md", "hippo river\n")
	e.eventually("b.md's text asked for", func() bool {
		for _, s := range p.texts() {
			if strings.Contains(s, "hippo river") {
				return true
			}
		}
		return false
	})
	e.eventually("b.md's vectors refused", func() bool { return failed.Load() > before })
	var ready int
	_ = scanOne(context.Background(), e.raw, &ready, "SELECT semantic_ready FROM document WHERE path = 'b.md'")
	if ready != 0 {
		t.Fatal("b.md is ready though its vectors were never stored")
	}
	undo()
	e.ready()
}

// TestTheStatusAndTheViewFailAsThemselves: a read that fails in a status or a search comes back
// as an error, not as an empty answer.
func TestTheStatusAndTheViewFailAsThemselves(t *testing.T) {
	ctx := context.Background()
	p := newFake("m", "a")
	e := newEnv(t, options{Provider: p})
	e.put("a.md", "zebra plains\n")
	e.ready()
	e.atHead()
	for _, table := range []string{"change", "chunk"} {
		undo := e.renamed(table)
		_, err := e.store.Status(ctx)
		undo()
		if err == nil {
			t.Errorf("the status read with %s gone", table)
		}
	}
	st, model, vec := searchStore{s: e.store, sem: e.ix.sem}, e.activeModel(), queryVec(t, p, "a\nzebra plains")
	for _, c := range []struct {
		table string
		read  func(v *View) error
	}{
		{"chunk_fts", func(v *View) error {
			_, err := v.Lexical(ctx, []search.Term{{Text: "zebra"}}, search.Filter{}, 5)
			return err
		}},
		{"document", func(v *View) error { _, err := v.SemanticState(ctx); return err }},
		{"model", func(v *View) error { _, err := v.Semantic(ctx, model, vec, search.Filter{}, 5); return err }},
	} {
		undo := e.renamed(c.table)
		err := st.View(ctx, c.read)
		undo()
		if err == nil {
			t.Errorf("a read with %s gone answered", c.table)
		}
	}
}

// TestASearchFailsWhenItsSettingsCannotBeRead: the workspace's retrieval settings are a read of
// their own; its failing fails the search, rather than answering as if nothing were set.
func TestASearchFailsWhenItsSettingsCannotBeRead(t *testing.T) {
	e := newEnv(t, options{})
	e.put("a.md", "# A\n\nkestrel\n")
	undo := e.renamed("workspace")
	_, err := e.store.Search(context.Background(), "kestrel", QueryOpts{Limit: 5, Mode: ModeLexical})
	undo()
	if err == nil {
		t.Error("a search with the workspace table gone answered")
	}
}

// TestASweepOfOrphansFailsAsItsStatements: each read and write a sweep of orphaned vectors makes
// (the models, the vectors, the chunks that keep them, the delete) fails as an error, and the
// vectors stay; once the failure lifts, the sweep deletes them. A sweep asked before the indexer
// is ready, its request gone, returns that.
func TestASweepOfOrphansFailsAsItsStatements(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, options{Provider: newFake("m", "a")})
	e.put("a.md", "zebra plains\n")
	e.ready()
	e.exec("DELETE FROM chunk") // every vector an orphan; the indexer, idle, writes nothing more
	vectors := func() (n int) {
		t.Helper()
		if err := scanOne(ctx, e.raw, &n, "SELECT count(*) FROM embedding"); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := vectors()
	if before == 0 {
		t.Fatal("no vectors to sweep")
	}
	for _, fail := range []struct {
		what string
		undo func() func()
	}{
		{"the models unreadable", func() func() { return e.renamed("model") }},
		{"the vectors unreadable", func() func() { return e.renamed("embedding") }},
		{"the chunks unreadable", func() func() { return e.renamed("chunk") }},
		{"the delete refused", func() func() { return e.failWrites("embedding", "DELETE") }},
	} {
		undo := fail.undo()
		if _, err := SweepOrphansStore(ctx, e.db, e.ws); err == nil {
			t.Errorf("%s: the sweep succeeded", fail.what)
		}
		undo()
		if n := vectors(); n != before {
			t.Errorf("%s: %d vectors left of %d", fail.what, n, before)
		}
	}
	// the delete path's own check, in the transaction that deleted the chunks, and the vector
	// write's: a chunk table it cannot read is an error
	undo := e.renamed("chunk")
	err := e.db.Write(ctx, func(tx *store.Tx) error {
		if _, err := e.store.dropOrphans(tx, [][]byte{{1}}); err == nil {
			t.Error("dropOrphans read no chunks and said nothing")
		}
		if _, err := e.store.withChunks(tx, []vecItem{{textHash: []byte{1}}}); err == nil {
			t.Error("withChunks read no chunks and said nothing")
		}
		return nil
	})
	undo()
	if err != nil {
		t.Fatal(err)
	}
	if n, err := SweepOrphansStore(ctx, e.db, e.ws); err != nil || n != before || vectors() != 0 {
		t.Fatalf("the sweep after: %d, %v; %d vectors left; want %d deleted", n, err, vectors(), before)
	}
	gone, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := newIndexer(e.store, e.fsys, options{}).SweepOrphans(gone); err == nil {
		t.Error("a sweep of an indexer never ready, its request gone, returned no error")
	}
}

// TestAFrontmatterWriteFailsAsItsStatements: replacing a document's facets and diagnostics fails
// as whichever of its deletes and inserts is refused.
func TestAFrontmatterWriteFailsAsItsStatements(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, options{})
	e.put("a.md", "zebra\n")
	e.stop() // the store's own writes from here on: the indexer is done with a.md
	db, err := store.Open(ctx, e.db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := NewStore(db, e.ws)
	r := schema.Result{Facets: []schema.Facet{{Field: "status", Value: "active"}},
		Diagnostics: []schema.Diagnostic{{Field: "type", Line: 2, Rule: "required", Message: "type is required"}}}
	var docID int64
	if err := scanOne(ctx, e.raw, &docID, "SELECT id FROM document WHERE path = 'a.md'"); err != nil {
		t.Fatal(err)
	}
	replace := func() error {
		return db.Write(ctx, func(tx *store.Tx) error { return s.replaceFrontmatter(tx, docID, r) })
	}
	if err := replace(); err != nil { // the instrument: unrefused, the replacement lands
		t.Fatal(err)
	}
	for _, op := range []struct{ table, op string }{
		{"doc_facet", "DELETE"}, {"doc_diagnostic", "DELETE"}, {"doc_facet", "INSERT"}, {"doc_diagnostic", "INSERT"},
	} {
		undo := e.failWrites(op.table, op.op)
		if err := replace(); err == nil {
			t.Errorf("%s on %s refused, and the replacement succeeded", op.op, op.table)
		}
		undo()
	}
	// a document's put writes its frontmatter: refused, the put writes nothing, and lands once it is not
	c := search.Chunk{Breadcrumb: "B", Body: "faceted words"}
	th := c.TextHash()
	b := Doc{Path: "b.md", Version: "1", Indexer: "test", Meta: chunk.Meta{Title: "B"}, Frontmatter: r,
		Chunks: []DocChunk{{Chunk: c, Hash: []byte("b1"), TextHash: th[:], Kind: ChunkSection}}}
	put := func() error {
		batch, err := BufferedWriter(s).Begin(ctx)
		if err != nil {
			return err
		}
		if err := batch.Put(ctx, b); err != nil {
			return err
		}
		return batch.Commit()
	}
	facets := func() (n int) {
		t.Helper()
		if err := scanOne(ctx, e.raw, &n, "SELECT count(*) FROM doc_facet"); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := facets()
	undo := e.failWrites("doc_facet", "INSERT")
	err = put()
	undo()
	if err == nil {
		t.Error("b.md's facets refused, and its put succeeded")
	}
	if _, ok, _ := s.Version(ctx, "b.md"); ok {
		t.Error("b.md's put failed on its facets, and its row was written")
	}
	if err := put(); err != nil {
		t.Fatal(err)
	}
	if n := facets(); n != before+1 {
		t.Errorf("after b.md's put: %d facets, want %d", n, before+1)
	}
}

// TestAGCFailsAsItsChangeLogPrune: the gc prunes the change log in the transaction that deletes the
// dead chunks; the prune failing fails the gc, and the chunks stay; once it lifts, the gc deletes them.
func TestAGCFailsAsItsChangeLogPrune(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, options{})
	e.put("a.md", "zebra\n")
	e.stop() // the gc is the test's own from here on
	db, err := store.Open(ctx, e.db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	x := newIndexer(NewStore(db, e.ws), e.fault, options{})
	e.exec("UPDATE chunk SET gen_to = gen_from") // every chunk dead at the generation it was born in
	dead := func() (n int) {
		t.Helper()
		if err := scanOne(ctx, e.raw, &n, "SELECT count(*) FROM chunk WHERE gen_to IS NOT NULL"); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := dead()
	if before == 0 {
		t.Fatal("no dead chunks to collect")
	}
	undo := e.renamed("change")
	_, err = x.gc(ctx)
	undo()
	if err == nil {
		t.Error("the gc succeeded with the change log unreadable")
	}
	if n := dead(); n != before {
		t.Errorf("a failed gc left %d dead chunks of %d", n, before)
	}
	if _, err := x.gc(ctx); err != nil || dead() != 0 {
		t.Fatalf("the gc after: %v, %d dead chunks left", err, dead())
	}
}

// TestALinkWriteFailsAsItsStatements: a document's names, links and link keys, and the
// re-resolution of another document's dangling link to it, are written in its put; whichever of
// those statements is refused, or whichever of their tables cannot be read, fails the commit, and
// the same put then lands.
func TestALinkWriteFailsAsItsStatements(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t, options{})
	e.stop() // the store's own writes from here on
	db, err := store.Open(ctx, e.db.Path())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := BufferedWriter(NewStore(db, e.ws))
	put := func(d Doc) error {
		b, err := w.Begin(ctx)
		if err != nil {
			return err
		}
		if err := b.Put(ctx, d); err != nil {
			return err
		}
		return b.Commit()
	}
	doc := func(path, version string, links ...DocLink) Doc {
		c := search.Chunk{Breadcrumb: "T", Body: "text of " + path}
		th := c.TextHash()
		return Doc{Path: path, Version: vfs.Version(version), Indexer: "test",
			Meta:   chunk.Meta{Title: "T", Aliases: []string{"another name of " + path}},
			Chunks: []DocChunk{{Chunk: c, Hash: []byte(path + version), TextHash: th[:], Kind: ChunkSection}},
			Links:  links}
	}
	toC := DocLink{Raw: "[[c]]", Name: "c", Kind: LinkWikilink}
	supersedes := DocLink{Raw: "supersedes: b", Name: "b", Kind: "supersedes", Keys: []string{"b", "b.md"}}
	if err := put(doc("b.md", "1", toC)); err != nil { // the instrument: unrefused, a put lands
		t.Fatal(err)
	}
	var dangling int
	if err := scanOne(ctx, e.raw, &dangling, "SELECT count(*) FROM link WHERE dst_doc IS NULL"); err != nil || dangling != 1 {
		t.Fatalf("b.md's link to c: %d dangling, %v; want 1", dangling, err)
	}
	refuse := func(name string, fail func() func(), d Doc) {
		t.Helper()
		undo := fail()
		if err := put(d); err == nil {
			t.Errorf("%s: the put landed", name)
		}
		undo()
	}
	newC := doc("c.md", "1", supersedes)
	for _, c := range []struct{ table, op string }{{"doc_name", "INSERT"}, {"link", "INSERT"}, {"link_key", "INSERT"}, {"link", "UPDATE"}} {
		refuse(c.op+" on "+c.table, func() func() { return e.failWrites(c.table, c.op) }, newC)
	}
	for _, table := range []string{"doc_name", "link", "link_key"} {
		refuse(table+" unreadable", func() func() { return e.renamed(table) }, newC)
	}
	if err := put(newC); err != nil {
		t.Fatal(err)
	}
	if err := scanOne(ctx, e.raw, &dangling, "SELECT count(*) FROM link WHERE dst_doc IS NULL"); err != nil || dangling != 0 {
		t.Fatalf("b.md's link to c after c.md landed: %d dangling, %v; want 0", dangling, err)
	}
	// a put over what c.md keeps deletes its names and links first
	againC := doc("c.md", "2", supersedes)
	for _, table := range []string{"doc_name", "link"} {
		refuse("DELETE on "+table, func() func() { return e.failWrites(table, "DELETE") }, againC)
	}
	if err := put(againC); err != nil {
		t.Fatal(err)
	}
}
