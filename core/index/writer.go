package index

import (
	"context"
	"time"

	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE WRITER PORT — where the indexer's documents are kept: the rows a search reads. The port is
// document persistence only: the queue, the change log, readiness and publication stay the local
// store's. A document is named by its path, which every implementation keys on; no store's own id
// crosses the port. The store's own implementation is a batch bound to the indexer's commit
// transaction; a destination's (core/pgstore/pgwriter) is a transaction of its own. Ordering the
// two is the destination's wiring, not this port's.

// Doc is one document as the indexer hands it on: what it read and cut of a file, whatever keeps
// it. It carries every field the indexer makes; an implementation that defers one declares so
// (core/index/writertest), and a workspace needing that field may not be served by it.
type Doc struct {
	Path        string
	Version     vfs.Version   // the file's version it was read at, kept verbatim
	Indexer     string        // the identity it was cut under, kept verbatim (ParseIdentity)
	Meta        chunk.Meta    // its title, tags, aliases, frontmatter (and its error) and abstract
	Chunks      []DocChunk    // in order
	Links       []DocLink     // its links and relations, in order
	Frontmatter schema.Result // its facets and diagnostics under the workspace's schema
	ADR         string        // the decision-record number a document of type adr carries; "" for none
	// TodoName is the name a todo store's task answers to, "#todo:<store>/<task id>"
	// (docs/decisions/todos.md#task-relations-and-external-links); "" for a document in no store
	TodoName string
}

// Writer is where documents are kept. A batch lives under Begin's ctx: once that is done, the
// batch keeps nothing (a Put, a Delete or the Commit says why).
type Writer interface {
	Begin(ctx context.Context) (Batch, error)
}

// Batch is one commit's documents, kept together: all of them, or none.
type Batch interface {
	Put(ctx context.Context, d Doc) error          // replaces what is kept at d.Path
	Delete(ctx context.Context, path string) error // a path not kept is no error
	Commit() error
	Rollback() error
}

// doc is what p hands on.
func (p *prepared) doc() Doc {
	return Doc{Path: p.path, Version: p.version, Indexer: p.indexer, Meta: p.meta, Chunks: p.chunks,
		Links: p.links, Frontmatter: p.frontmatter, ADR: p.adr, TodoName: p.todoName}
}

// storeWriter is the store's own Writer inside one write transaction: its batches are that
// transaction's, so the documents commit with the jobs, the change log and readiness, or not at all.
type storeWriter struct {
	s   *Store
	tx  *store.Tx
	now time.Time
}

func (w storeWriter) Begin(context.Context) (Batch, error) { return w.batch(), nil }

func (w storeWriter) batch() *storeBatch { return &storeBatch{w: w} }

// storeBatch is a batch in the store's write transaction. Commit and Rollback belong to that
// transaction's owner (the indexer's commit): here they do nothing, so a batch never commits half a
// transaction. changed are the documents written, by the store's ids, for the publication that
// follows the commit; they never leave the package.
type storeBatch struct {
	w       storeWriter
	changed []int64
}

func (b *storeBatch) Put(_ context.Context, d Doc) error {
	id, err := b.w.s.upsertDoc(b.w.tx, d, b.w.now)
	if err == nil {
		b.changed = append(b.changed, id)
	}
	return err
}

func (b *storeBatch) Delete(_ context.Context, path string) error {
	id, err := b.w.s.deleteDoc(b.w.tx, path, b.w.now)
	if err == nil {
		b.changed = append(b.changed, id)
	}
	return err
}

func (*storeBatch) Commit() error   { return nil }
func (*storeBatch) Rollback() error { return nil }

var _ Writer = storeWriter{}
