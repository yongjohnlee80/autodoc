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

// Kind is how the indexer reads path: by extension, with the build's registrations and the
// workspace's own text extensions.
func (x *Indexer) Kind(p string) kind.Kind { return x.kinds.Of(p) }

// versionOf is what document.indexer records for path, of kind k, indexed now (ADR 0216 §1.6): a
// registered chunker's extension and version; a derived format's frozen identity, then the
// built-in identity its Markdown is chunked under; or the built-in chunkers' version, with the
// schema's fingerprint for Markdown. A derived document has no frontmatter to check, so a schema
// change never derives it again.
func (x *Indexer) versionOf(p string, k kind.Kind, tokens int, schemaFP string, abstract bool) string {
	builtIn := docVersion(indexerVersion(tokens), k.Traits().Frontmatter, schemaFP, abstract)
	return identities.Of(k)(x, p, tokens, builtIn) + x.mode(p).version()
}

// identity is a kind's document.indexer for path: its own, or the built-in chunkers' when the build
// has nothing of its own for it.
type identity func(x *Indexer, p string, tokens int, builtIn string) string

// identities are each kind's (kind.Table: a kind added is a kind given its identity).
var identities = kind.NewTable(map[kind.Kind]identity{
	kind.Markdown: builtInIdentity,
	kind.Text:     builtInIdentity,
	kind.YAML:     builtInIdentity,
	kind.Registered: func(x *Indexer, p string, tokens int, builtIn string) string {
		if _, v, ok := x.opts.Registrations.Chunker(p); ok {
			return registeredVersion(kind.Ext(p), v, tokens)
		}
		return builtIn
	},
	kind.Derived: func(x *Indexer, p string, tokens int, builtIn string) string {
		if f, ok := x.opts.Registrations.Format(kind.Ext(p)); ok {
			return derivedVersion(kind.Ext(p), f.ID, f.Version, indexerVersion(tokens))
		}
		return builtIn
	},
})

func builtInIdentity(_ *Indexer, _ string, _ int, builtIn string) string { return builtIn }

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
	ret, err := x.store.retrieval(ctx)
	if err != nil {
		return fmt.Errorf("index: listing outdated documents: reading the retrieval settings: %w", err)
	}
	outdated, err := x.store.outdated(ctx, func(p string, tokens int) string {
		return x.versionOf(p, x.kinds.Of(p), tokens, fp, ret.AbstractChunk)
	})
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
	if err := s.facets(tx).With(FacetDoc, docID).Delete(); err != nil {
		return err
	}
	if err := s.diagnostics(tx).With(DiagDoc, docID).Delete(); err != nil {
		return err
	}
	if len(r.Facets) > 0 {
		b := s.facetBatch(tx).SkipConflicts() // a list may repeat an item
		for _, f := range r.Facets {
			b.Add(map[FacetField]any{FacetDoc: docID, FacetName: f.Field, FacetValue: f.Value})
		}
		if err := b.Flush(); err != nil {
			return err
		}
	}
	if len(r.Diagnostics) > 0 {
		b := s.diagnosticBatch(tx)
		for i, d := range r.Diagnostics {
			b.Add(map[DiagnosticField]any{DiagDoc: docID, DiagOrd: int64(i), DiagField: d.Field,
				DiagLine: int64(d.Line), DiagRule: d.Rule, DiagMessage: d.Message})
		}
		if err := b.Flush(); err != nil {
			return err
		}
	}
	return nil
}

func countDiagnosed(d dao.DAO[*Diagnostic, DiagnosticField, int64]) (int64, error) {
	n, err := dao.CountDistinct(d, DiagDoc)
	return int64(n), err
}

// Diagnostics is what is wrong with the frontmatter of the document at path, as last indexed.
func (s *Store) Diagnostics(ctx context.Context, path string) ([]schema.Diagnostic, error) {
	var out []schema.Diagnostic
	err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := s.diagnostics(tx).Join(JoinDocument).WithPredicate(dao.Eq(`"document"."path"`, path)).
			OrderBy(dao.Asc(DiagByPath)).Select(DiagField, DiagLine, DiagRule, DiagMessage)
		for _, r := range rows {
			out = append(out, schema.Diagnostic{Field: r.Field, Line: int(r.Line), Rule: r.Rule, Message: r.Message})
		}
		return err
	})
	return out, err
}
