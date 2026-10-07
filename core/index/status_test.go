package index

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// status is Indexer.Status, failing the test on an error.
func (e *env) status() Status {
	e.t.Helper()
	st, err := e.ix.Status(context.Background())
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

// freshStatus is Indexer.Status with nothing kept: what a status computed now says.
func (e *env) freshStatus() Status {
	e.t.Helper()
	e.ix.statusTurn <- struct{}{}
	e.ix.statusCache = nil
	<-e.ix.statusTurn
	return e.status()
}

// sameStatus compares two statuses' every field.
func sameStatus(a, b Status) bool { return reflect.DeepEqual(a, b) }

// TestStatusIsComputedOncePerCommit (ADR 1791329335 §2.5): a status at an unchanged commit_seq is
// the kept one; after each kind of write it is what a computation from scratch says, with the
// write's effect in it.
func TestStatusIsComputedOncePerCommit(t *testing.T) {
	var computed atomic.Int64
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a, onStatusCompute: func() { computed.Add(1) }})
	e.put("x.md", "zebra\n")
	e.ready()
	e.status()
	before := computed.Load()
	for i := 0; i < 5; i++ {
		e.status()
	}
	if got := computed.Load() - before; got != 0 {
		t.Errorf("an idle workspace's status was computed %d more times, want 0", got)
	}

	// each write's effect shows, and the answer is a fresh computation's
	matches := func(what string, effect func(Status) bool) {
		t.Helper()
		e.eventually(what, func() bool {
			got := e.status()
			return effect(got) && sameStatus(got, e.freshStatus())
		})
	}
	e.put("y.md", "hippo\n") // a document commit
	matches("a document commit", func(st Status) bool { return st.Docs == 2 })
	e.ready() // vector commits
	matches("a vector commit", func(st Status) bool {
		return st.Embeddings != nil && st.Embeddings.Pending == 0 && st.Embeddings.Texts == 2
	})
	e.put("y.md", "lion tiger\n") // a replaced text, then gc
	e.eventually("gc", func() bool {
		var dead int
		_ = scanOne(context.Background(), e.raw, &dead, "SELECT COUNT(*) FROM chunk WHERE gen_to IS NOT NULL")
		return dead == 0
	})
	matches("a gc", func(st Status) bool { return st.Chunks == 2 })
	if err := e.ix.do(context.Background(), func(context.Context, *store.Tx) error { return nil }); err != nil {
		t.Fatal(err)
	}
	matches("a writer op", func(Status) bool { return true })
	e.put("bad.md", "---\ntitle: [unclosed\n---\nbody\n") // a frontmatter diagnostic
	matches("unparsed frontmatter", func(st Status) bool { return reflect.DeepEqual(st.UnparsedFrontmatter, []string{"bad.md"}) })
	e.fault.failOpen("z.md", errors.New("injected read failure")) // a failed job
	e.write("z.md", "zebu\n")
	e.ix.Touch("z.md")
	matches("a failing job", func(st Status) bool { return len(st.Failing) == 1 && st.Failing[0].Path == "z.md" })
	e.fault.failOpen("z.md", nil)
}

// TestStatusIsOneSnapshot (Lector r0, required 1): a commit landing between the counts and the
// coverage is in neither: the status is the one at the seq it read.
func TestStatusIsOneSnapshot(t *testing.T) {
	var hook func()
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a, betweenStatusReads: func() {
		if hook != nil {
			hook()
		}
	}})
	e.put("x.md", "zebra\n")
	e.ready()
	before := e.status()
	if err := e.ix.do(context.Background(), func(context.Context, *store.Tx) error { return nil }); err != nil {
		t.Fatal(err) // the seq moves: the next status computes
	}
	hook = func() {
		hook = nil
		e.put("n.md", "newt\n") // committed while the computation's read transaction is open
	}
	got := e.status()
	if got.Docs != before.Docs || got.Chunks != before.Chunks || got.Embeddings.Texts != before.Embeddings.Texts {
		t.Errorf("a commit between the reads: docs %d, chunks %d, texts %d; want all as of the snapshot: %d, %d, %d",
			got.Docs, got.Chunks, got.Embeddings.Texts, before.Docs, before.Chunks, before.Embeddings.Texts)
	}
	if after := e.status(); after.Docs != before.Docs+1 || after.Chunks != before.Chunks+1 {
		t.Errorf("the next status: docs %d, chunks %d; want the commit in it", after.Docs, after.Chunks)
	}
}

// TestStatusIsComputedOnceForItsWaiters (§2.5): callers that wait on a computation at one seq share
// it; one that waited while a commit landed gets a computation at the newer seq.
func TestStatusIsComputedOnceForItsWaiters(t *testing.T) {
	var computed atomic.Int64
	gate := make(chan struct{})
	var gated atomic.Bool
	a := newFake("m", "a")
	e := newEnv(t, Options{Provider: a, onStatusCompute: func() { computed.Add(1) }, betweenStatusReads: func() {
		if gated.Load() {
			<-gate
		}
	}})
	e.put("x.md", "zebra\n")
	e.ready()
	bump := func() {
		if err := e.ix.do(context.Background(), func(context.Context, *store.Tx) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}

	// ten callers at one seq: one computation
	bump()
	gated.Store(true)
	before := computed.Load()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); e.status() }()
	}
	e.eventually("nine callers waiting on the first", func() bool { return e.ix.statusWaiting.Load() == 9 })
	gated.Store(false)
	close(gate)
	wg.Wait()
	if got := computed.Load() - before; got != 1 {
		t.Errorf("ten callers at one seq: %d computations, want 1", got)
	}

	// a waiter whose seq moved while it waited computes again
	gate = make(chan struct{})
	bump()
	gated.Store(true)
	first := make(chan Status, 1)
	go func() { first <- e.status() }()
	e.eventually("the first computation under way", func() bool { return len(e.ix.statusTurn) == 1 })
	second := make(chan Status, 1)
	go func() { second <- e.status() }()
	e.eventually("the second caller waiting", func() bool { return e.ix.statusWaiting.Load() == 1 })
	gated.Store(false)
	e.put("n.md", "newt\n") // a commit while the second waits
	close(gate)
	s1, s2 := <-first, <-second
	if s1.Docs != 1 || s2.Docs != 2 {
		t.Errorf("the first caller saw %d documents (want 1), the waiter %d (want 2: the commit it waited past)", s1.Docs, s2.Docs)
	}
}

// TestAStatusThatCannotReadSaysSo: a read of any count failing fails the status, rather than
// answering with a count missing, and the next status after the store is whole answers again. Each
// read is broken in turn: the documents, the jobs, the unparsed frontmatter, the diagnostics.
func TestAStatusThatCannotReadSaysSo(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("x.md", "zebra\n")
	for _, c := range []struct {
		name string
		brk  func() (undo func())
	}{
		{"the documents", func() func() { return e.renamed("document") }},
		{"the jobs", func() func() { return e.renamed("index_job") }},
		{"the unparsed frontmatter", func() func() {
			e.exec(`ALTER TABLE document RENAME COLUMN frontmatter_error TO frontmatter_error_aside`)
			return func() { e.exec(`ALTER TABLE document RENAME COLUMN frontmatter_error_aside TO frontmatter_error`) }
		}},
		{"the diagnostics", func() func() { return e.renamed("doc_diagnostic") }},
	} {
		undo := c.brk()
		e.ix.statusTurn <- struct{}{}
		e.ix.statusCache = nil // computed, not kept
		<-e.ix.statusTurn
		if _, err := e.ix.Status(context.Background()); err == nil {
			t.Errorf("%s unreadable: the status answered", c.name)
		}
		undo()
		if st := e.freshStatus(); st.Docs != 1 {
			t.Errorf("%s restored: %d documents, want 1", c.name, st.Docs)
		}
	}
}
