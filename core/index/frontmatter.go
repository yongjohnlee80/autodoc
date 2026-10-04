package index

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// schema is the workspace's frontmatter schema now, and its fingerprint.
func (x *Indexer) schema() (*schema.Schema, string) {
	if x.opts.Schema == nil {
		return nil, ""
	}
	return x.opts.Schema()
}

// kindOf is how the indexer reads path, with the build's registrations and the workspace's own
// text extensions.
func (x *Indexer) kindOf(p string) kind.Kind {
	var text []string
	if x.opts.TextExtensions != nil {
		text = x.opts.TextExtensions()
	}
	return x.kinds.Of(p, text)
}

// Kind is how the indexer reads path: by extension, with the build's registrations and the
// workspace's own text extensions.
func (x *Indexer) Kind(p string) kind.Kind { return x.kindOf(p) }

// versionOf is what document.indexer records for path, of kind k, indexed now (ADR 0216 §1.6): a
// registered chunker's extension and version; a derived format's frozen identity, then the
// built-in identity its Markdown is chunked under; or the built-in chunkers' version, with the
// schema's fingerprint for Markdown. A derived document has no frontmatter to check, so a schema
// change never derives it again.
func (x *Indexer) versionOf(p string, k kind.Kind, tokens int, schemaFP string) string {
	switch k {
	case kind.Registered:
		if _, v, ok := x.opts.Registrations.Chunker(p); ok {
			return registeredVersion(kind.Ext(p), v, tokens)
		}
	case kind.Pro:
		if f, ok := x.opts.Registrations.Format(kind.Ext(p)); ok {
			return derivedVersion(kind.Ext(p), f.ID, f.Version, indexerVersion(tokens))
		}
	}
	return docVersion(indexerVersion(tokens), k == kind.Markdown, schemaFP)
}

// Revalidate queues every document indexed under another version than it would get now: after a
// chunker or section-size change all of them, after a schema change the Markdown files. Each is
// rebuilt from its file; an unchanged chunk keeps its row and its vector, so a schema change costs
// no embedding.
func (x *Indexer) Revalidate(ctx context.Context) error {
	h, err := x.holding(ctx)
	if err != nil {
		return err
	}
	_, fp := x.schema()
	outdated, err := x.store.outdated(ctx, func(p string, tokens int) string { return x.versionOf(p, x.kindOf(p), tokens, fp) })
	if err != nil {
		return fmt.Errorf("index: listing outdated documents: %w", err)
	}
	for _, p := range outdated {
		if _, held := h.of(p); held {
			continue // never reinterpreted (ADR 0216 §1.4)
		}
		x.touch(p, false) // the version check rebuilds each: no file changed, the indexer did
	}
	return nil
}

// replaceFrontmatter replaces a document's facets and diagnostics with r's.
func (s *Store) replaceFrontmatter(tx *store.Tx, docID int64, r schema.Result) error {
	if err := s.sc.Facets(tx).With(store.FacetDoc, docID).Delete(); err != nil {
		return err
	}
	if err := s.sc.Diagnostics(tx).With(store.DiagDoc, docID).Delete(); err != nil {
		return err
	}
	if len(r.Facets) > 0 {
		b := s.sc.FacetBatch(tx).SkipConflicts() // a list may repeat an item
		for _, f := range r.Facets {
			b.Add(map[store.FacetField]any{store.FacetDoc: docID, store.FacetName: f.Field, store.FacetValue: f.Value})
		}
		if err := b.Flush(); err != nil {
			return err
		}
	}
	if len(r.Diagnostics) > 0 {
		b := s.sc.DiagnosticBatch(tx)
		for i, d := range r.Diagnostics {
			b.Add(map[store.DiagnosticField]any{store.DiagDoc: docID, store.DiagOrd: int64(i), store.DiagField: d.Field,
				store.DiagLine: int64(d.Line), store.DiagRule: d.Rule, store.DiagMessage: d.Message})
		}
		if err := b.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func countDiagnosed(d dao.DAO[*store.Diagnostic, store.DiagnosticField, int64]) (int64, error) {
	n, err := dao.CountDistinct(d, store.DiagDoc)
	return int64(n), err
}

// Diagnostics is what is wrong with the frontmatter of the document at path, as last indexed.
func (s *Store) Diagnostics(ctx context.Context, path string) ([]schema.Diagnostic, error) {
	var out []schema.Diagnostic
	err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := s.sc.Diagnostics(tx).Join(store.JoinDocument).WithPredicate(dao.Eq(`"document"."path"`, path)).
			OrderBy(dao.Asc(store.DiagByPath)).Select(store.DiagField, store.DiagLine, store.DiagRule, store.DiagMessage)
		for _, r := range rows {
			out = append(out, schema.Diagnostic{Field: r.Field, Line: int(r.Line), Rule: r.Rule, Message: r.Message})
		}
		return err
	})
	return out, err
}
