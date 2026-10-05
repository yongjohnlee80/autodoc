package pgstore

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/deploy"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/search"
)

//go:embed postgres/*.sql
var migrations embed.FS

// Ledger is the table dao/deploy records pgstore's applied migrations in: a destination database
// other products also deploy to keeps their ledgers apart. It is the same name the local store's
// destination scripts used, so a destination's existing ledger is still found.
const Ledger = "autodoc_schema"

// Migrate applies pgstore's pending migrations to conn, in one transaction, and records them.
func Migrate(ctx context.Context, conn dao.DataConn) error {
	if _, err := deploy.New(migrations, deploy.Ledger(Ledger)).Apply(ctx, conn); err != nil {
		return fmt.Errorf("pgstore: migrate: %w", err)
	}
	return nil
}

// ErrNoTenant is Open with an empty tenant.
var ErrNoTenant = errs.Sentinel(errs.ErrInvalidArgument, "pgstore: no tenant")

// Store is one tenant's index in PostgreSQL with pgvector, as golib's search port reads it: a
// search.Store[int64, *View] whose views are Semantic, Signaler and Lister. Every statement it
// runs, reads and writes alike, is filtered by its tenant. Its migrations (Migrate) must have run.
//
// A Store is safe for concurrent use.
type Store struct {
	tenant string
	rw     schemas // writes, on the connection as given
	ro     schemas // views: every transaction REPEATABLE READ, READ ONLY
}

// Open returns tenant's store on conn.
func Open(conn dao.DataConn, tenant string) (*Store, error) {
	if tenant == "" {
		return nil, ErrNoTenant
	}
	return &Store{tenant: tenant, rw: newSchemas(conn), ro: newSchemas(snapshotConn{conn})}, nil
}

// snapshotConn is a connection whose transactions are REPEATABLE READ and READ ONLY: dao's
// transaction begins with the connection's Begin, so schemas built on this one run every
// statement of a View in one snapshot that cannot write. Everything else is the connection's.
type snapshotConn struct{ dao.DataConn }

func (c snapshotConn) Begin(ctx context.Context) (dao.TxConn, error) {
	return dao.BeginConnTx(ctx, c.DataConn, dao.TxOptions{Access: dao.TxReadOnly, Isolation: dao.TxRepeatableRead})
}

// View runs fn in one View: one REPEATABLE READ, READ ONLY transaction, so every retriever and
// presentation field reads one snapshot. fn's error is returned as it is.
func (s *Store) View(ctx context.Context, fn func(v *View) error) error {
	return dao.RunTx(ctx, func(tx *dao.Transaction) error {
		return fn(&View{s: s, tx: tx})
	})
}

var _ search.Store[int64, *View] = (*Store)(nil)

// Document is one document as the writer takes it.
type Document struct {
	Path, Title string
	Version     string              // the source's version as the consumer compares it; stored and returned verbatim
	Indexer     string              // the identity the document was cut under; stored and returned verbatim, never parsed
	Tags        []string            // lowercased by the caller, as the search port's filters are
	Facets      map[string][]string // field → values
	Links       []string            // the paths it links to; a repeat is stored once
	Chunks      []search.Chunk      // in order; Ord is the position; Embed is the text its vector is made of
}

// DocInfo is one stored document as a consumer's revalidation reads it: the path with the source
// version and the cutting identity it was last written with, and the generation that write bumped.
type DocInfo struct {
	Path, Version, Indexer string
	Generation             int64
}

// Docs pages the tenant's documents in path order: at most limit after the path after ("" from the
// start), and more says whether any follow. Each path carries its own version and indexer, so a
// consumer comparing per-path state needs no other read.
func (s *Store) Docs(ctx context.Context, after string, limit int) (docs []DocInfo, more bool, err error) {
	if limit <= 0 {
		return nil, false, nil
	}
	d := s.rw.doc.DAO(dao.WithQueryContext(ctx)).With(dTenant, s.tenant)
	if after != "" {
		d = d.WithPredicate(dao.Gt(string(dByPath), after))
	}
	rows, err := d.OrderBy(dao.Asc(dByPath)).Limit(uint64(limit)+1).Select(dPath, dVersion, dIndexer, dGen)
	if err != nil {
		return nil, false, fmt.Errorf("pgstore: docs: %w", err)
	}
	for _, r := range rows {
		if len(docs) == limit {
			more = true
			break
		}
		docs = append(docs, DocInfo{Path: r.Path, Version: r.Version, Indexer: r.Indexer, Generation: r.Generation})
	}
	return docs, more, nil
}

// Text is one unembedded chunk text: the hash a vector is stored under, and the text to make it
// from — the chunk's Embed when it has one, else Breadcrumb + "\n" + Body, as the chunk's identity
// hash is defined over.
type Text struct {
	TextHash [32]byte
	Text     string
}

// Pending lists up to limit distinct text hashes of the tenant's chunks with no vector under
// model, except the ones skip names, each with its embed text, in (doc, ord) order: the input an
// embedding worker reads back after a restart. The limit counts distinct hashes, so a caller
// paging with the returned hashes in its skip set gets every hash in at most limit rounds.
func (s *Store) Pending(ctx context.Context, model string, skip map[[32]byte]bool, limit int) ([]Text, error) {
	if limit <= 0 {
		return nil, nil
	}
	// the tenant's hashes already embedded under this model: the set the page walks past
	embRows, err := s.rw.emb.DAO(dao.WithQueryContext(ctx)).With(eTenant, s.tenant).With(eModel, model).Select(eHash)
	if err != nil {
		return nil, fmt.Errorf("pgstore: pending embeddings: %w", err)
	}
	have := make(map[[32]byte]bool, len(embRows))
	for _, r := range embRows {
		var h [32]byte
		copy(h[:], r.TextHash)
		have[h] = true
	}
	// the tenant's chunks in (doc, ord) order; distinctness is folded in Go against the
	// embedded and skipped sets, so the limit counts distinct hashes
	rows, err := s.rw.chunk.DAO(dao.WithQueryContext(ctx)).With(cTenant, s.tenant).Join(joinDoc).
		OrderBy(dao.Asc(cByPath), dao.Asc(cByOrd)).Limit(uint64(limit)*2).Select(cHash, cEmb, cCrumb, cBody)
	if err != nil {
		return nil, fmt.Errorf("pgstore: pending chunks: %w", err)
	}
	out := make([]Text, 0, limit)
	seen := map[[32]byte]bool{}
	for _, r := range rows {
		var h [32]byte
		copy(h[:], r.TextHash)
		if seen[h] || have[h] || skip[h] {
			continue
		}
		seen[h] = true
		out = append(out, Text{TextHash: h, Text: r.EmbedText()})
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

// facetPairs stores facets as "field=value", which a facet filter matches exactly.
func facetPairs(f map[string][]string) []string {
	var out []string
	for field, vals := range f {
		for _, v := range vals {
			out = append(out, field+"="+v)
		}
	}
	return out
}

// Put writes a document: replaced whole when its path exists (its generation bumped, its chunks
// and links replaced), created otherwise, in one transaction. It is not ready for semantic search
// until MarkReady says so. It returns the document's id.
func (s *Store) Put(ctx context.Context, d Document) (int64, error) {
	if d.Path == "" {
		return 0, errs.Wrap(errs.ErrInvalidArgument, "pgstore: a document has no path")
	}
	tags, facets := nonNil(d.Tags), nonNil(facetPairs(d.Facets))
	tagText := strings.Join(d.Tags, " ")
	var id int64
	err := dao.RunTx(ctx, func(tx *dao.Transaction) error {
		cur, err := s.rw.doc.On(tx, dao.WithQueryContext(ctx)).With(dTenant, s.tenant).With(dPath, d.Path).Get(dID, dGen)
		switch {
		case errors.Is(err, dao.ErrNoRows):
			id, err = s.rw.doc.On(tx, dao.WithQueryContext(ctx)).Set(dTenant, s.tenant).Set(dPath, d.Path).Set(dTitle, d.Title).
				Set(dTags, tags).Set(dFacets, facets).Set(dVersion, d.Version).Set(dIndexer, d.Indexer).
				Set(dGen, int64(1)).Set(dReady, false).Insert()
			if err != nil {
				return err
			}
		case err != nil:
			return err
		default:
			id = cur.ID
			if err := s.rw.doc.On(tx, dao.WithQueryContext(ctx)).With(dTenant, s.tenant).With(dID, id).
				Set(dTitle, d.Title).Set(dTags, tags).Set(dFacets, facets).Set(dVersion, d.Version).Set(dIndexer, d.Indexer).
				Set(dGen, cur.Generation+1).Set(dReady, false).Update(); err != nil {
				return err
			}
			if err := s.rw.chunk.On(tx, dao.WithQueryContext(ctx)).With(cTenant, s.tenant).With(cDoc, id).Delete(); err != nil {
				return err
			}
			if err := s.rw.link.On(tx, dao.WithQueryContext(ctx)).With(lTenant, s.tenant).With(lSrc, id).Delete(); err != nil {
				return err
			}
		}
		if len(d.Chunks) > 0 {
			b := s.rw.chunk.On(tx, dao.WithQueryContext(ctx)).Batch().ForceInsert()
			for i, c := range d.Chunks {
				h := c.TextHash()
				b.Add(map[chunkField]any{cTenant: s.tenant, cDoc: id, cOrd: i, cCrumb: c.Breadcrumb, cBody: c.Body,
					cEmb: c.Embed, cTitle: d.Title, cTags: tagText,
					cStart: c.ByteStart, cEnd: c.ByteEnd, cHash: h[:]})
			}
			if err := b.Flush(); err != nil {
				return err
			}
		}
		if len(d.Links) > 0 {
			b := s.rw.link.On(tx, dao.WithQueryContext(ctx)).Batch().ForceInsert().SkipConflicts()
			for _, l := range d.Links {
				b.Add(map[linkField]any{lTenant: s.tenant, lSrc: id, lDst: l})
			}
			if err := b.Flush(); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("pgstore: put %s: %w", d.Path, err)
	}
	return id, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Embedding is a chunk text's vector under a model, keyed by the chunk's TextHash.
type Embedding struct {
	TextHash [32]byte
	Vec      []float32
}

// Embed stores vectors under model for this tenant. A hash already stored keeps its vector: the
// same text under the same model has the same vector.
func (s *Store) Embed(ctx context.Context, model string, vecs []Embedding) error {
	if len(vecs) == 0 {
		return nil
	}
	b := s.rw.emb.DAO(dao.WithQueryContext(ctx)).Batch().ForceInsert().SkipConflicts()
	for _, e := range vecs {
		h := e.TextHash
		b.AddRow(&embRow{Tenant: s.tenant, Model: model, TextHash: h[:], Vec: e.Vec})
	}
	if err := b.Flush(); err != nil {
		return fmt.Errorf("pgstore: embed: %w", err)
	}
	return nil
}

// MarkReady marks the document at path ready for semantic search under model when every one of its
// chunks has a vector under it, and not ready otherwise. It reports whether the document is ready.
func (s *Store) MarkReady(ctx context.Context, path, model string) (bool, error) {
	var ready bool
	err := dao.RunTx(ctx, func(tx *dao.Transaction) error {
		doc, err := s.rw.doc.On(tx, dao.WithQueryContext(ctx)).With(dTenant, s.tenant).With(dPath, path).Get(dID)
		if err != nil {
			return err
		}
		chunks, err := s.rw.chunk.On(tx, dao.WithQueryContext(ctx)).With(cTenant, s.tenant).With(cDoc, doc.ID).Select(cHash)
		if err != nil {
			return err
		}
		hashes := map[string]bool{}
		var in []any
		for _, c := range chunks {
			if !hashes[string(c.TextHash)] {
				hashes[string(c.TextHash)] = true
				in = append(in, c.TextHash)
			}
		}
		have := 0
		if len(in) > 0 {
			n, err := s.rw.emb.On(tx, dao.WithQueryContext(ctx)).With(eTenant, s.tenant).With(eModel, model).
				WithPredicate(dao.In(qcol(tEmb, string(eHash)), in)).Count()
			if err != nil {
				return err
			}
			have = int(n)
		}
		ready = have == len(in)
		return s.rw.doc.On(tx, dao.WithQueryContext(ctx)).With(dTenant, s.tenant).With(dID, doc.ID).Set(dReady, ready).Update()
	})
	if err != nil {
		return false, fmt.Errorf("pgstore: mark ready %s: %w", path, err)
	}
	return ready, nil
}

// Delete removes the document at path, with its chunks and links. A path not stored is no error.
func (s *Store) Delete(ctx context.Context, path string) error {
	if err := s.rw.doc.DAO(dao.WithQueryContext(ctx)).With(dTenant, s.tenant).With(dPath, path).Delete(); err != nil {
		return fmt.Errorf("pgstore: delete %s: %w", path, err)
	}
	return nil
}

// Drop deletes everything the tenant has, in one transaction: its documents (chunks and links
// cascade), its embeddings and its meta row. Another tenant's rows are untouched.
func (s *Store) Drop(ctx context.Context) error {
	err := dao.RunTx(ctx, func(tx *dao.Transaction) error {
		for _, del := range []func(*dao.Transaction) error{
			func(tx *dao.Transaction) error {
				return s.rw.doc.On(tx, dao.WithQueryContext(ctx)).With(dTenant, s.tenant).Delete()
			},
			func(tx *dao.Transaction) error {
				return s.rw.emb.On(tx, dao.WithQueryContext(ctx)).With(eTenant, s.tenant).Delete()
			},
			func(tx *dao.Transaction) error {
				return s.rw.meta.On(tx, dao.WithQueryContext(ctx)).With(mTenant, s.tenant).Delete()
			},
		} {
			if err := del(tx); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("pgstore: drop: %w", err)
	}
	return nil
}

// SetModels records the model searches answer under (active) and the model being filled while a
// switch is in progress (target, "" when none). Semantic answers ErrModelChanged for any other
// model, and SemanticState is StateSwitching while target is set and differs from active. Which
// documents are ready under the new active model is the caller's to mark again.
func (s *Store) SetModels(ctx context.Context, active, target string) error {
	err := s.rw.meta.DAO(dao.WithQueryContext(ctx)).Set(mTenant, s.tenant).Set(mActive, active).Set(mTarget, target).Upsert()
	if err != nil {
		return fmt.Errorf("pgstore: set models: %w", err)
	}
	return nil
}

// qcol is a table-qualified column for a raw-column predicate; pgstore's names need no quoting.
func qcol(table, col string) string { return table + "." + col }

// pathFilter is the search port's path filter on a document's path column: each path is the
// document itself or a directory holding it, ORed; "" or "." anywhere means no filter (nil).
func pathFilter(col string, paths []string) dao.Predicate {
	if len(paths) == 0 {
		return nil
	}
	var ors []dao.Predicate
	for _, p := range paths {
		p = strings.Trim(p, "/")
		if p == "" || p == "." {
			return nil
		}
		ors = append(ors, dao.Eq(col, p), dao.HasPrefix(col, p+"/"))
	}
	return dao.Or(ors...)
}

// filters are a search.Filter's predicates over the document table's columns: tags ANDed, paths
// ORed, facet values ORed within a field and fields ANDed.
func filters(f search.Filter) []dao.Predicate {
	var ps []dao.Predicate
	tags := dao.ArrayOp("tag", qcol(tDoc, string(dTags)))
	for _, t := range f.Tags {
		ps = append(ps, tags.Predicate(t))
	}
	if p := pathFilter(qcol(tDoc, string(dPath)), f.Paths); p != nil {
		ps = append(ps, p)
	}
	facets := dao.ArrayOp("facet", qcol(tDoc, string(dFacets)))
	for field, vals := range f.Facets {
		var ors []dao.Predicate
		for _, v := range vals {
			ors = append(ors, facets.Predicate(field+"="+v))
		}
		if len(ors) > 0 {
			ps = append(ps, dao.Or(ors...))
		}
	}
	return ps
}
