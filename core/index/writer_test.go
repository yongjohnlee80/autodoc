package index

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search/chunk"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// The store's batch inside the indexer's commit leaves committing to that transaction: its Commit
// does nothing, a document put in it is gone when the transaction fails after the put, and kept
// when the transaction commits.
func TestTheStoresBatchCommitsWithItsTransaction(t *testing.T) {
	ctx := context.Background()
	db := openDB(t)
	w, err := db.AddWorkspace(ctx, "kb", "/kb", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s := NewStore(db, w.ID)
	d := Doc{Path: "a.md", Version: "v1", Indexer: "i", Meta: chunk.Meta{Title: "A"}}
	failed := errors.New("the commit failed after the put")
	err = db.Write(ctx, func(tx *store.Tx) error {
		b := storeWriter{s: s, tx: tx, now: time.Now()}.batch()
		if err := b.Put(ctx, d); err != nil {
			return err
		}
		if err := b.Commit(); err != nil {
			return err
		}
		return failed
	})
	if !errors.Is(err, failed) {
		t.Fatalf("the transaction: %v", err)
	}
	if _, ok, err := s.Identity(ctx, "a.md"); ok || err != nil {
		t.Errorf("a document its failed transaction put was kept: %v %v", ok, err)
	}
	err = db.Write(ctx, func(tx *store.Tx) error {
		b := storeWriter{s: s, tx: tx, now: time.Now()}.batch()
		defer func() { _ = b.Rollback() }() // does nothing either: the transaction commits
		return b.Put(ctx, d)
	})
	if err != nil {
		t.Fatalf("the committing transaction: %v", err)
	}
	if id, ok, err := s.Identity(ctx, "a.md"); err != nil || !ok || id != "i" {
		t.Errorf("after the transaction committed: %q %v %v", id, ok, err)
	}
}
