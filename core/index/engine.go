package index

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/search/query"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// SEARCH THROUGH GOLIB'S ENGINE. The engine (golib search) owns the search's policy: modes, the
// query's facets, rank fusion, boosts, relevance and the per-document cap. This file is the store's
// side of it: a View over one read transaction, answering from AutoDoc's own tables.

// View is one search's read transaction of a workspace's index: every retriever, signal and
// presentation field reads the same snapshot.
type View struct {
	s   *Store
	tx  *store.Tx
	sem *semantic // nil: the store has no semantic tier in this search
}

// searchStore is a workspace's index as the engine reads it.
type searchStore struct {
	s   *Store
	sem *semantic
}

// View opens one read transaction; fn's error comes back as it is.
func (st searchStore) View(ctx context.Context, fn func(v *View) error) error {
	return st.s.read(ctx, func(tx *store.Tx) error { return fn(&View{s: st.s, tx: tx, sem: st.sem}) })
}

// NewSearcher builds the engine over a workspace's index: embed is nil for a search by words
// alone. index.Options.NewSearcher replaces it.
type NewSearcher func(st search.Store[int64, *View], embed search.QueryEmbedder) search.Searcher

// defaultSearcher is golib's engine with the search's constants.
func defaultSearcher(st search.Store[int64, *View], embed search.QueryEmbedder) search.Searcher {
	opts := []search.Option{search.WithFusionK(rrfK), search.WithRetrieverTop(retrieverTop), search.WithPerDocument(perDocument),
		search.WithLimits(defaultLimit, maxLimit), search.WithBoosts(linkBoost, tagBoost)}
	if embed != nil {
		opts = append(opts, search.WithQueryEmbedder(embed))
	}
	return search.NewEngine[int64, *View](st, opts...)
}

// optsOf is a retriever's filters: the query's, and whether the request leaves the abstract chunks
// out, which travels on the context the search was asked with.
func optsOf(ctx context.Context, f search.Filter) QueryOpts {
	return QueryOpts{Tags: f.Tags, Paths: f.Paths, Facets: f.Facets, sectionsOnly: sectionsOnly(ctx)}
}

type sectionsOnlyKey struct{}

func withSectionsOnly(ctx context.Context, on bool) context.Context {
	return context.WithValue(ctx, sectionsOnlyKey{}, on)
}

func sectionsOnly(ctx context.Context) bool {
	on, _ := ctx.Value(sectionsOnlyKey{}).(bool)
	return on
}

func candidateOf(r *store.Chunk, snippet string) search.Candidate[int64] {
	return search.Candidate[int64]{Doc: r.DocID, Ord: int(r.Ord), Path: r.DocPath, Breadcrumb: r.Breadcrumb, Snippet: snippet,
		Generation: r.DocActiveGen, ByteStart: int(r.ByteStart), ByteEnd: int(r.ByteEnd)}
}

// Lexical runs the FTS query over the alive chunks the filters admit, best first (BM25 weighs
// title 10, breadcrumb 5, tags 5, body 1), at most n. The workspace and the filters are in the same
// WHERE as the MATCH, so they apply before the rank and the limit.
func (v *View) Lexical(ctx context.Context, terms []search.Term, f search.Filter, n int) ([]search.Candidate[int64], error) {
	d, ok, err := v.s.filtered(v.tx, alive(v.s.sc.Chunks(v.tx)), optsOf(ctx, f))
	if err != nil || !ok {
		return nil, err
	}
	rows, err := d.Join(store.JoinFTS).WithPredicate(dao.Match(store.ChunkFTS, query.FTS5(terms))).
		OrderBy(dao.Asc(store.ChunkByRank), dao.Asc(store.ChunkByPath)).Limit(uint64(n)).
		Select(store.ChunkDoc, store.ChunkOrd, store.ChunkDocPath, store.ChunkBreadcrumb, store.ChunkByteStart,
			store.ChunkByteEnd, store.ChunkDocActiveGen, store.ChunkSnippet)
	if err != nil {
		return nil, fmt.Errorf("index: lexical search: %w", err)
	}
	out := make([]search.Candidate[int64], 0, len(rows))
	for _, r := range rows {
		out = append(out, candidateOf(r, r.Snippet))
	}
	return out, nil
}

// Semantic is the semantic retriever in this transaction (semanticHits). It answers
// search.ErrModelChanged when the active model is no longer the one the query was embedded with.
func (v *View) Semantic(ctx context.Context, model string, vec []float32, f search.Filter, n int) ([]search.Candidate[int64], error) {
	now, err := v.s.activeModel(v.tx)
	if err != nil {
		return nil, err
	}
	if now != model {
		return nil, search.ErrModelChanged
	}
	return v.s.semanticHits(v.tx, v.sem, model, vec, optsOf(ctx, f), n)
}

// SemanticState is switching while a new model fills (the active model, read in this transaction,
// is not the target), partial while a document is not semantic-ready, else ready.
func (v *View) SemanticState(ctx context.Context) (search.State, error) {
	if v.sem != nil {
		active, err := v.s.activeModel(v.tx)
		if err != nil {
			return "", err
		}
		if active != "" && active != v.sem.target.Model().Fingerprint() {
			return search.StateSwitching, nil
		}
	}
	unready, err := v.s.sc.Documents(v.tx).With(store.DocSemanticReady, int64(0)).Exists()
	if err != nil {
		return "", err
	}
	if unready {
		return search.StatePartial, nil
	}
	return search.StateReady, nil
}

// Signals are each document's in-links (the documents linking to it from their bodies, itself not
// counted) and tags. A frontmatter relation is not an in-link: whether relations should lift a
// document is a ranking change of its own, to be measured, so they leave the boost as it was.
func (v *View) Signals(ctx context.Context, docs []int64) (map[int64]search.Signals, error) {
	out := make(map[int64]search.Signals, len(docs))
	body := make([]any, len(BodyKinds))
	for i, k := range BodyKinds {
		body[i] = k
	}
	for _, id := range docs {
		inLinks, err := dao.CountDistinct(v.s.sc.LinksOut(v.tx).With(store.LinkDst, id).Excluding(store.LinkSrc, id).
			WithPredicate(dao.In(`"link"."kind"`, body)), store.LinkSrc)
		if err != nil {
			return nil, err
		}
		tags, err := v.s.tagsOf(v.tx, id)
		if err != nil {
			return nil, err
		}
		out[id] = search.Signals{InLinks: int(inLinks), Tags: tags}
	}
	return out, nil
}

// List answers a query of filters and no words: the documents they admit, in path order, each as
// its first section.
func (v *View) List(ctx context.Context, f search.Filter, n int) ([]search.Candidate[int64], error) {
	d, ok, err := v.s.filtered(v.tx, alive(v.s.sc.Chunks(v.tx)).With(store.ChunkOrd, int64(0)), optsOf(ctx, f))
	if err != nil || !ok {
		return nil, err
	}
	rows, err := d.OrderBy(dao.Asc(store.ChunkByPath)).Limit(uint64(n)).
		Select(store.ChunkDoc, store.ChunkOrd, store.ChunkDocPath, store.ChunkBreadcrumb, store.ChunkBody, store.ChunkByteStart,
			store.ChunkByteEnd, store.ChunkDocActiveGen)
	if err != nil {
		return nil, fmt.Errorf("index: facet search: %w", err)
	}
	out := make([]search.Candidate[int64], 0, len(rows))
	for _, r := range rows {
		out = append(out, candidateOf(r, chunk.Snippet(r.Body)))
	}
	return out, nil
}

var (
	_ search.Store[int64, *View] = searchStore{}
	_ search.Semantic[int64]     = (*View)(nil)
	_ search.Signaler[int64]     = (*View)(nil)
	_ search.Lister[int64]       = (*View)(nil)
)

// queryEmbedder embeds a query for the engine with the model active now. The model a filling
// target replaces is offline: that is search.ErrSwitching to the engine.
func (s *Store) queryEmbedder(sem *semantic) search.QueryEmbedder {
	return func(ctx context.Context, q string) (string, []float32, error) {
		fp, vec, err := s.embedQuery(ctx, sem, q)
		if errors.Is(err, errOffline) {
			return "", nil, fmt.Errorf("%w: %w", search.ErrSwitching, err)
		}
		return fp, vec, err
	}
}

// answer runs q on the face of s its stages ask for, in the mode they search in, and gives the
// answer in AutoDoc's own types and errors, with how it was made. paused is semantic search held
// off by the workspace's embedding policy.
func answer(ctx context.Context, st *Store, s searchers, q string, opts QueryOpts, fields search.Fields, paused bool) (Result, error) {
	p, err := planOf(opts)
	if err != nil {
		return Result{}, err
	}
	// the workspace's retrieval settings, as this query overrides them: the abstract chunks are
	// left out in the retrievers themselves, and a demotion asks for more hits than it returns
	set, err := st.retrieval(ctx)
	if err != nil {
		return Result{}, err
	}
	abstract, demoting := set.AbstractChunk, set.DemoteSuperseded
	if r := opts.Retrieval; r != nil {
		if r.AbstractChunk != nil {
			abstract = *r.AbstractChunk
		}
		if r.DemoteSuperseded != nil {
			demoting = *r.DemoteSuperseded
		}
	}
	ctx = withSectionsOnly(ctx, !abstract)
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	ask := opts.Limit
	if demoting {
		ask = min(max(2*limit, limit+20), maxLimit)
	}
	// paths as the engine takes them, root-relative with '/' trimmed: "/" and "./" are the root, so
	// no path filter, as filtered reads them
	var paths []string
	for _, p := range opts.Paths {
		paths = append(paths, strings.Trim(p, "/"))
	}
	res, err := s.of(p.rerank).Search(ctx, search.Query{Text: q, Mode: p.mode, Limit: ask, Fields: fields,
		Filter: search.Filter{Tags: opts.Tags, Paths: paths, Facets: opts.Facets}})
	if err != nil {
		return Result{}, ownError(err)
	}
	out := Result{Hits: make([]Hit, len(res.Hits)), ModeUsed: string(res.ModeUsed), Semantic: string(res.Semantic)}
	if res.SemanticError != "" {
		out.SemanticError = ErrEmbedFailed.Error()
	}
	for i, h := range res.Hits {
		out.Hits[i] = Hit{Path: h.Path, Breadcrumb: h.Breadcrumb, Snippet: h.Snippet, Generation: h.Generation,
			ByteStart: h.ByteStart, ByteEnd: h.ByteEnd, Score: h.Score, Relevance: h.Relevance, Via: h.Via,
			RankScore: h.RankScore}
	}
	if demoting {
		paths := make([]string, 0, len(out.Hits))
		for _, h := range out.Hits {
			paths = append(paths, h.Path)
		}
		successors, err := st.successorsOf(ctx, paths)
		if err != nil {
			return Result{}, err
		}
		out.Hits = demote(out.Hits, successors)
		if len(out.Hits) > limit {
			out.Hits = out.Hits[:limit]
		}
	}
	out.Rank = RankState{State: string(res.Rank.State), Model: res.Rank.Model, Error: res.Rank.Error}
	out.Stages = p.report(out, wordless(q, opts, fields), paused)
	return out, nil
}

// ownErrors are the engine's errors as AutoDoc names them. The engine and AutoDoc format the
// detail after a sentinel the same way, so AutoDoc's sentinel takes the engine's place in front of
// it.
var ownErrors = []struct{ engine, own error }{
	{search.ErrUnknownMode, ErrUnknownMode},
	{search.ErrNoProvider, ErrNoProvider},
	{search.ErrSwitching, ErrSwitching},
	{search.ErrEmbedFailed, ErrEmbedFailed},
	{query.ErrUnknownFacet, ErrUnknownFacet},
	{query.ErrFacetValue, ErrFacetValue},
}

func ownError(err error) error {
	for _, e := range ownErrors {
		if errors.Is(err, e.engine) {
			return fmt.Errorf("%w%s", e.own, strings.TrimPrefix(err.Error(), e.engine.Error()))
		}
	}
	return err
}
