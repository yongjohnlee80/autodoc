package pgstore

import (
	"context"
	"fmt"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
)

// Batch is documents written in one transaction: kept together, or none of them. A Put that loses
// its compare-and-set to another writer fails with ErrWriteRace and is not written again: the
// other writer's row is newer than the one the batch read, and versions are opaque, so writing
// over it would silently drop that writer's work. Any error, that one or another (a first insert
// two writers made, which PostgreSQL ends the transaction for), fails the batch: Rollback it.
// What a destination shared by two writers of one tenant should do instead is its ADR's to decide.
//
// With no retry, a lost race cannot spin, whatever the isolation. A batch is READ COMMITTED like
// every write (committedConn), so a lost race is ErrWriteRace, not a serialization failure.
type Batch struct {
	s  *Store
	tx *dao.Transaction
}

// Begin starts a batch. Nothing reaches the database before its first write.
func (s *Store) Begin(ctx context.Context) *Batch { return &Batch{s: s, tx: dao.Begin(ctx)} }

// Put writes d inside the batch, as Store.Put does alone. It returns the document's id.
func (b *Batch) Put(ctx context.Context, d Document) (int64, error) {
	if err := d.check(); err != nil {
		return 0, err
	}
	tags, facets := nonNil(d.Tags), nonNil(facetPairs(d.Facets))
	tagText := strings.Join(d.Tags, " ")
	id, err := b.s.putIn(ctx, b.tx, d, tags, facets, tagText)
	if err != nil {
		return 0, fmt.Errorf("pgstore: put %s: %w", d.Path, err)
	}
	return id, nil
}

// Delete removes the document at path inside the batch, with its chunks and links. A path not
// stored is no error.
func (b *Batch) Delete(ctx context.Context, path string) error {
	if err := b.s.rw.doc.On(b.tx, dao.WithQueryContext(ctx)).With(dTenant, b.s.tenant).With(dPath, path).Delete(); err != nil {
		return fmt.Errorf("pgstore: delete %s: %w", path, err)
	}
	return nil
}

// Commit keeps the batch's documents.
func (b *Batch) Commit() error { return b.tx.Commit() }

// Rollback keeps none of them.
func (b *Batch) Rollback() error { return b.tx.Rollback() }
