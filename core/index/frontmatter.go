package index

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/parse/markdown"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// ValidateFrontmatter checks src's frontmatter as the index does at path (doc.validate): a task in
// a todo store by its todo format, every other file by kb, the workspace's schema (ADR 1792015212
// amendment 2). So the editor's diagnostics and index.documents' agree.
func (x *Indexer) ValidateFrontmatter(path string, src []byte, kb *schema.Schema) schema.Result {
	return frontmatterOf(kind.ParseMarkdown(src), src, kb, x.mode(path).Store)
}

// frontmatterOf is doc's frontmatter checked: by the store's todo format for a task, else by kb.
func frontmatterOf(doc *markdown.Document, src []byte, kb *schema.Schema, st *TodoStore) schema.Result {
	if st != nil && st.Check != nil {
		return st.Check(src)
	}
	if fm := doc.Root.FirstChild; fm != nil && fm.Kind == markdown.KindFrontmatter {
		return kb.Validate(fm.Literal, true)
	}
	return kb.Validate(nil, false)
}

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
// no embedding. A change of the workspace's variables queues the documents naming a changed one.
func (x *Indexer) Revalidate(ctx context.Context) error {
	h, err := x.holding(ctx)
	if err != nil {
		return err
	}
	outdated, err := x.outdated(ctx)
	if err != nil {
		return err
	}
	for _, p := range outdated {
		if _, held := h.of(p); held {
			continue // never reinterpreted (ADR 0216 §1.4)
		}
		x.touch(p, false) // the version check rebuilds each: no file changed, the indexer did
	}
	return nil
}

// outdated are the documents indexed under another version than they would get now, in path order.
func (x *Indexer) outdated(ctx context.Context) ([]string, error) {
	_, fp := x.schema()
	ret, err := x.store.retrieval(ctx)
	if err != nil {
		return nil, fmt.Errorf("index: listing outdated documents: reading the retrieval settings: %w", err)
	}
	out, err := x.store.outdated(ctx, func(p string, tokens int, recorded string) string {
		return x.versionOf(p, x.kinds.Of(p), tokens, fp, ret.AbstractChunk) + varsVersion(varsNamed(recorded), x.mode(p).Vars)
	})
	if err != nil {
		return nil, fmt.Errorf("index: listing outdated documents: %w", err)
	}
	return out, nil
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
