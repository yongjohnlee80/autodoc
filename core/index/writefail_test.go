package index

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search"
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

// TestAFailedCommitWritesNothingAndIsRetried: when the writer's transaction fails (any table's
// write refused), nothing of the batch is written, and its jobs run again: once the failure lifts,
// every change lands.
func TestAFailedCommitWritesNothingAndIsRetried(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("keep.md", "---\ntags: [old]\n---\n# Keep\n\nfirst words [[other]]\n", "gone.md", "to be removed\n")
	version := func(p string) string { v, _ := e.store.Version(p); return string(v) }
	settle := func() { time.Sleep(150 * time.Millisecond) } // several batches try and fail
	for _, c := range []struct {
		name, table, op string
		act             func()
		unchanged       func() bool
		landed          func()
	}{
		{"a new document's row", "document", "INSERT",
			func() { e.write("new1.md", "fresh\n"); e.ix.Touch("new1.md") },
			func() bool { _, ok := e.store.Version("new1.md"); return !ok },
			func() { e.indexedAt("new1.md") }},
		{"a new document's chunks", "chunk", "INSERT",
			func() { e.write("new2.md", "fresh words\n"); e.ix.Touch("new2.md") },
			func() bool { _, ok := e.store.Version("new2.md"); return !ok },
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
		{"its links", "link", "INSERT",
			func() { e.write("keep.md", "# Keep\n\nfifth words [[gone]]\n"); e.ix.Touch("keep.md") },
			func() bool { return len(e.match("fifth")) == 0 },
			func() { e.indexedAt("keep.md") }},
		{"the change log", "change", "INSERT",
			func() { e.write("new3.md", "logged\n"); e.ix.Touch("new3.md") },
			func() bool { _, ok := e.store.Version("new3.md"); return !ok },
			func() { e.indexedAt("new3.md") }},
		{"the workspace's counters", "workspace", "UPDATE",
			func() { e.write("new4.md", "counted\n"); e.ix.Touch("new4.md") },
			func() bool { _, ok := e.store.Version("new4.md"); return !ok },
			func() { e.indexedAt("new4.md") }},
		{"a removed document's row", "document", "DELETE",
			func() { _ = e.fsys.Remove(context.Background(), "gone.md"); e.ix.Touch("gone.md") },
			func() bool { return version("gone.md") != "" },
			func() { e.eventually("gone.md gone", func() bool { return version("gone.md") == "" }) }},
	} {
		undo := e.failWrites(c.table, c.op)
		c.act()
		settle()
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
	p := newFake("m", "a")
	e := newEnv(t, Options{Provider: p})
	e.put("a.md", "zebra plains\n")
	e.ready()
	undo := e.failWrites("embedding", "INSERT")
	e.put("b.md", "hippo river\n")
	e.eventually("b.md's text asked for", func() bool {
		for _, s := range p.texts() {
			if strings.Contains(s, "hippo river") {
				return true
			}
		}
		return false
	})
	time.Sleep(100 * time.Millisecond)
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
	e := newEnv(t, Options{Provider: p})
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
