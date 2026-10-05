package pgstore

import (
	"context"
	"errors"
	"slices"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/search/query"
)

// snippetTokens is the words a lexical snippet keeps around its first match.
const snippetTokens = 24

// View is one snapshot of a tenant's index: a REPEATABLE READ, READ ONLY transaction every method
// reads in. It is valid only inside Store.View's fn.
type View struct {
	s  *Store
	tx *dao.Transaction
}

var (
	_ search.Semantic[int64] = (*View)(nil)
	_ search.Signaler[int64] = (*View)(nil)
	_ search.Lister[int64]   = (*View)(nil)
)

func (v *View) chunks(ctx context.Context) dao.DAO[*chunkRow, chunkField, int64] {
	return v.s.ro.chunk.On(v.tx, dao.WithQueryContext(ctx)).With(cTenant, v.s.tenant)
}

func (v *View) docs(ctx context.Context) dao.DAO[*docRow, docField, int64] {
	return v.s.ro.doc.On(v.tx, dao.WithQueryContext(ctx)).With(dTenant, v.s.tenant)
}

func candidate(r *chunkRow, snippet string) search.Candidate[int64] {
	return search.Candidate[int64]{Doc: r.Doc, Ord: r.Ord, Path: r.Path, Breadcrumb: r.Breadcrumb, Snippet: snippet,
		Generation: r.Generation, ByteStart: r.ByteStart, ByteEnd: r.ByteEnd}
}

func hitFields() []chunkField {
	return []chunkField{cDoc, cOrd, cCrumb, cBody, cStart, cEnd, cPath, cGen}
}

// Lexical is the chunks matching every term, best first by ts_rank_cd over the breadcrumb (class
// A) and the body (class D), then path, then ordinal, with the filters in the same WHERE so they
// apply before the rank and the limit.
func (v *View) Lexical(ctx context.Context, terms []search.Term, f search.Filter, n int) ([]search.Candidate[int64], error) {
	if n <= 0 || len(terms) == 0 {
		return nil, nil
	}
	q, err := query.TSQuery(terms)
	if err != nil {
		return nil, err
	}
	d := v.chunks(ctx).Join(joinDoc).WithPredicate(dao.Match(tsv(), q))
	for _, p := range filters(f) {
		d = d.WithPredicate(p)
	}
	rows, err := d.OrderBy(dao.AscBy(cByRank, q), dao.Asc(cByPath), dao.Asc(cByOrd)).Limit(uint64(n)).Select(hitFields()...)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate[int64], 0, len(rows))
	for _, r := range rows {
		out = append(out, candidate(r, chunk.Highlight(r.Body, terms, snippetTokens)))
	}
	return out, nil
}

// activeModel is the tenant's active and target models, read in this View; none recorded is "".
func (v *View) models(ctx context.Context) (active, target string, err error) {
	m, err := v.s.ro.meta.On(v.tx, dao.WithQueryContext(ctx)).With(mTenant, v.s.tenant).Get(mActive, mTarget)
	if errors.Is(err, dao.ErrNoRows) {
		return "", "", nil
	}
	if err != nil {
		return "", "", err
	}
	return m.Active, m.Target, nil
}

// Semantic is the chunks nearest vec by cosine distance (vectors are normalized, so this is dot
// order), then path, then ordinal, scanned exactly: only chunks of documents ready under the
// active model, with the filters before the limit. Another model than the active one answers
// search.ErrModelChanged.
func (v *View) Semantic(ctx context.Context, model string, vec []float32, f search.Filter, n int) ([]search.Candidate[int64], error) {
	active, _, err := v.models(ctx)
	if err != nil {
		return nil, err
	}
	if active == "" || active != model {
		return nil, search.ErrModelChanged
	}
	if n <= 0 {
		return nil, nil
	}
	d := v.chunks(ctx).Join(joinDoc, joinEmb).
		WithPredicate(dao.Eq(qcol(tEmb, "model"), model)).
		WithPredicate(dao.Eq(qcol(tDoc, string(dReady)), true))
	for _, p := range filters(f) {
		d = d.WithPredicate(p)
	}
	rows, err := d.OrderBy(dao.AscBy(cByNearest, postgres.Vector(vec)), dao.Asc(cByPath), dao.Asc(cByOrd)).Limit(uint64(n)).Select(hitFields()...)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate[int64], 0, len(rows))
	for _, r := range rows {
		out = append(out, candidate(r, chunk.Snippet(r.Body)))
	}
	return out, nil
}

// SemanticState is StateSwitching while a target model fills, StatePartial while a document is not
// ready, and StateReady otherwise, read in this View.
func (v *View) SemanticState(ctx context.Context) (search.State, error) {
	active, target, err := v.models(ctx)
	if err != nil {
		return "", err
	}
	if target != "" && target != active {
		return search.StateSwitching, nil
	}
	unready, err := v.docs(ctx).With(dReady, false).Exists()
	if err != nil {
		return "", err
	}
	if unready {
		return search.StatePartial, nil
	}
	return search.StateReady, nil
}

// Signals are each document's in-links (distinct linking documents, itself not counted) and tags.
func (v *View) Signals(ctx context.Context, ids []int64) (map[int64]search.Signals, error) {
	out := map[int64]search.Signals{}
	if len(ids) == 0 {
		return out, nil
	}
	in := make([]any, len(ids))
	for i, id := range ids {
		in[i] = id
	}
	docs, err := v.docs(ctx).WithPredicate(dao.In(qcol(tDoc, string(dID)), in)).Select(dID, dPath, dTags)
	if err != nil {
		return nil, err
	}
	byPath := map[string]int64{}
	paths := make([]any, 0, len(docs))
	for _, d := range docs {
		byPath[d.Path] = d.ID
		paths = append(paths, d.Path)
		out[d.ID] = search.Signals{Tags: d.Tags}
	}
	if len(paths) == 0 {
		return out, nil
	}
	links, err := v.s.ro.link.On(v.tx, dao.WithQueryContext(ctx)).With(lTenant, v.s.tenant).
		WithPredicate(dao.In(qcol(tLink, string(lDst)), paths)).Select(lSrc, lDst)
	if err != nil {
		return nil, err
	}
	from := map[int64]map[int64]bool{}
	for _, l := range links {
		dst := byPath[l.DstPath]
		if l.Src == dst {
			continue // a link to itself is not a signal
		}
		if from[dst] == nil {
			from[dst] = map[int64]bool{}
		}
		from[dst][l.Src] = true
	}
	for id, srcs := range from {
		s := out[id]
		s.InLinks = len(srcs)
		out[id] = s
	}
	return out, nil
}

// List is the documents f admits, in path order, each as its first chunk with the start of its text
// as the snippet, at most n.
func (v *View) List(ctx context.Context, f search.Filter, n int) ([]search.Candidate[int64], error) {
	if n <= 0 {
		return nil, nil
	}
	d := v.docs(ctx)
	for _, p := range filters(f) {
		d = d.WithPredicate(p)
	}
	docs, err := d.OrderBy(dao.Asc(dByPath)).Limit(uint64(n)).Select(dID, dPath, dGen)
	if err != nil || len(docs) == 0 {
		return nil, err
	}
	in := make([]any, len(docs))
	for i, doc := range docs {
		in[i] = doc.ID
	}
	firsts, err := v.chunks(ctx).With(cOrd, 0).WithPredicate(dao.In(qcol(tChunk, string(cDoc)), in)).
		Select(cDoc, cOrd, cCrumb, cBody, cStart, cEnd)
	if err != nil {
		return nil, err
	}
	first := map[int64]*chunkRow{}
	for _, c := range firsts {
		first[c.Doc] = c
	}
	out := make([]search.Candidate[int64], 0, len(docs))
	for _, doc := range docs {
		c := first[doc.ID]
		if c == nil {
			c = &chunkRow{Doc: doc.ID}
		}
		c.Path, c.Generation = doc.Path, doc.Generation
		out = append(out, candidate(c, chunk.Snippet(c.Body)))
	}
	return slices.Clip(out), nil
}
