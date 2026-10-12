// Package pgwriter is core/index's writer port over core/pgstore: a PostgreSQL destination keeping
// the documents the indexer hands on. It joins the two, so neither imports the other.
//
// A batch is one PostgreSQL transaction (pgstore.Batch): a crash keeps none of it. pgstore keeps
// the columns ADR 0217 reconciled; the fields it does not keep yet are Deferred, and a workspace
// needing one may not be served by a PostgreSQL destination until it does (the destination ADR
// enforces that when a workspace is configured). Ordering a batch here against the local store's
// queue and change log is the destination wiring's, not this package's.
//
// Concurrency: a Writer is safe for concurrent use; a Batch belongs to one goroutine.
package pgwriter

import (
	"context"

	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// Deferred are the fields of an index.Doc pgstore does not keep yet, by writertest's names: the
// aliases (no doc_alias table), the diagnostics, the ADR number (no name table) and the relations
// (a link's keys). A body link is kept as the name it was written with, not resolved.
var Deferred = []string{"aliases", "diagnostics", "adr", "relations"}

// New is s as the indexer's writer.
func New(s *pgstore.Store) index.Writer { return writer{s: s} }

type writer struct{ s *pgstore.Store }

func (w writer) Begin(ctx context.Context) (index.Batch, error) { return batch{b: w.s.Begin(ctx)}, nil }

type batch struct{ b *pgstore.Batch }

func (b batch) Put(ctx context.Context, d index.Doc) error {
	_, err := b.b.Put(ctx, document(d))
	return err
}

func (b batch) Delete(ctx context.Context, path string) error { return b.b.Delete(ctx, path) }
func (b batch) Commit() error                                 { return b.b.Commit() }
func (b batch) Rollback() error                               { return b.b.Rollback() }

// document is d as pgstore keeps it.
func document(d index.Doc) pgstore.Document {
	out := pgstore.Document{Path: d.Path, Title: d.Meta.Title, Version: string(d.Version), Indexer: d.Indexer,
		Tags: d.Meta.Tags}
	for _, f := range d.Frontmatter.Facets {
		if out.Facets == nil {
			out.Facets = map[string][]string{}
		}
		out.Facets[f.Field] = append(out.Facets[f.Field], f.Value)
	}
	for _, l := range d.Links {
		if l.Keys == nil { // a relation's keys are deferred
			out.Links = append(out.Links, l.Name)
		}
	}
	out.Chunks = make([]search.Chunk, len(d.Chunks))
	out.Kinds = make([]string, len(d.Chunks))
	for i, c := range d.Chunks {
		out.Chunks[i], out.Kinds[i] = c.Chunk, c.Kind
	}
	return out
}
