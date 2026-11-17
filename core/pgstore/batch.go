package pgstore

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
)

// Batch is documents written in one transaction, with the ingest state written beside them
// (SetCursor, SetRefreshed, Quarantine, Release, in ingest.go): kept together, or none of them. A Put that loses
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

// Delete removes the document at path inside the batch, with its chunks and links, and its texts'
// embeddings that no other chunk of the tenant has (deleteDocs). A path not stored is no error.
func (b *Batch) Delete(ctx context.Context, path string) error {
	if _, err := b.deleteDocs(ctx, dao.Eq(qcol(tDoc, string(dPath)), path)); err != nil {
		return fmt.Errorf("pgstore: delete %s: %w", path, err)
	}
	return nil
}

// DeleteSource removes inside the batch the tenant's document rendered from the source row
// sourceID, when the row's delete at at is no older than the document's last write (its
// source_at; a document with none is removed): a row deleted and written again before one pass
// keeps its new document, and an equal time is a delete. It reports whether a document went. Its
// chunks and links go by the foreign keys, its typed values with it, and its texts' embeddings
// under every model unless another chunk of the tenant still has the text (deleteDocs). Another
// tenant's document of the same source id is never touched.
func (b *Batch) DeleteSource(ctx context.Context, sourceID string, at time.Time) (bool, error) {
	if sourceID == "" {
		return false, errs.Wrap(errs.ErrInvalidArgument, "pgstore: a delete needs the source row's id")
	}
	n, err := b.deleteDocs(ctx, dao.Eq(qcol(tDoc, string(dSource)), sourceID),
		dao.Raw("("+qcol(tDoc, string(dSrcAt))+" IS NULL OR "+qcol(tDoc, string(dSrcAt))+" <= ?)", at))
	if err != nil {
		return false, fmt.Errorf("pgstore: delete source row %s: %w", sourceID, err)
	}
	return n > 0, nil
}

// deleteDocs deletes the tenant's documents where preds hold, and then the embeddings of their
// chunks' texts, under every model, that no chunk of the tenant still has: a deleted text's
// vectors go with it, and a text another document shares keeps its own. An index of a model's
// embeddings loses their entries with them. It returns how many documents went.
func (b *Batch) deleteDocs(ctx context.Context, preds ...dao.Predicate) (int, error) {
	q := dao.WithQueryContext(ctx)
	d := b.s.rw.doc.On(b.tx, q).With(dTenant, b.s.tenant)
	for _, p := range preds {
		d = d.WithPredicate(p)
	}
	docs, err := d.Select(dID)
	if err != nil || len(docs) == 0 {
		return 0, err
	}
	ids := make([]any, len(docs))
	for i, r := range docs {
		ids[i] = r.ID
	}
	chunks, err := b.s.rw.chunk.On(b.tx, q).With(cTenant, b.s.tenant).
		WithPredicate(dao.In(qcol(tChunk, string(cDoc)), ids)).Select(cHash)
	if err != nil {
		return 0, err
	}
	if err := b.s.rw.doc.On(b.tx, q).With(dTenant, b.s.tenant).WithPredicate(dao.In(qcol(tDoc, string(dID)), ids)).Delete(); err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	var hashes []any
	for _, c := range chunks {
		if k := string(c.TextHash); !seen[k] {
			seen[k] = true
			hashes = append(hashes, c.TextHash)
		}
	}
	for part := range slices.Chunk(hashes, textsPart) {
		err := b.s.rw.emb.On(b.tx, q).With(eTenant, b.s.tenant).
			WithPredicate(dao.In(qcol(tEmb, string(eHash)), part)).
			WithPredicate(dao.Raw("NOT EXISTS (SELECT 1 FROM " + tChunk + " k WHERE k.tenant = " + qcol(tEmb, string(eTenant)) +
				" AND k.text_hash = " + qcol(tEmb, string(eHash)) + ")")).Delete()
		if err != nil {
			return 0, err
		}
	}
	return len(docs), nil
}

// Commit keeps the batch's documents and ingest state.
func (b *Batch) Commit() error { return b.tx.Commit() }

// Rollback keeps none of them.
func (b *Batch) Rollback() error { return b.tx.Rollback() }
