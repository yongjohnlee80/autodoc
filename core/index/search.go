package index

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// Search modes (QueryOpts.Mode) and semantic states (Result.Semantic).
const (
	ModeAuto     = "auto"
	ModeLexical  = "lexical"
	ModeSemantic = "semantic"

	SemanticOff = "off"
)

// ErrUnknownMode is a search mode that is none of ModeAuto, ModeLexical and ModeSemantic.
var ErrUnknownMode = errs.Sentinel(errs.ErrInvalidArgument, "index: unknown search mode")

// ErrNoProvider is semantic search asked of an indexer with no embedding provider: none is set up,
// or the daemon is still setting one up after it started. It is an ErrUnsupported.
var ErrNoProvider = errs.Sentinel(errs.ErrUnsupported, "index: semantic search: no embedding provider in use")

// HighlightStart and HighlightEnd mark the matched terms in a Hit's Snippet: control characters,
// which no note contains, so a client can render them as it likes.
const (
	HighlightStart = store.HighlightStart
	HighlightEnd   = store.HighlightEnd
)

// The search's constants (ADR 0204 §4.4).
const (
	defaultLimit = 20
	maxLimit     = 200
	retrieverTop = 50 // candidates each retriever contributes to fusion
	rrfK         = 60
	perDocument  = 3
	tagBoost     = 1.2
	linkBoost    = 0.1
)

// QueryOpts is search.query's options.
type QueryOpts struct {
	Limit int      // default 20, at most 200
	Mode  string   // ModeAuto (default), ModeLexical or ModeSemantic
	Tags  []string // every hit's document has all of these
	Paths []string // hits under these paths: a directory and what is below it, or one file
}

// Hit is one chunk a search found.
type Hit struct {
	Path, Breadcrumb, Snippet string
	Generation                int64 // the document's, as index.changes reports it
	ByteStart, ByteEnd        int
	Score                     float64 // fused and boosted; comparable only within one Result
	// Relevance is Score on a fixed scale, 0 to 1: 1 is first in every retriever the search ran
	// (1/(rrfK+1) each), before boosts; boosts past it stay at 1.
	Relevance float64
	Via       []string // the retrievers that found it
}

// Result is search.query's answer: the hits, and what the search could use (ADR 0204 §4.4).
type Result struct {
	Hits     []Hit
	ModeUsed string // ModeLexical, ModeSemantic or ModeHybrid
	Semantic string // SemanticOff, SemanticReady, SemanticPartial, SemanticSwitching or SemanticError
	// SemanticError is the constant message of SemanticError: a provider's own error text is not
	// passed to clients.
	SemanticError string
}

// Search answers a query lexically: the store alone has no embedding provider. Indexer.Search
// adds the semantic tier.
func (s *Store) Search(ctx context.Context, q string, opts QueryOpts) (Result, error) {
	return s.search(ctx, q, opts, nil)
}

// Search answers a query with the semantic tier when the indexer has a provider.
func (x *Indexer) Search(ctx context.Context, q string, opts QueryOpts) (Result, error) {
	return x.store.search(ctx, q, opts, x.sem)
}

// search answers a query from one read transaction, so every hit's text, path and generation come
// from the same snapshot. The query is embedded first, with the model active then; the transaction
// then reads the active model again, and a flip in between embeds again.
func (s *Store) search(ctx context.Context, q string, opts QueryOpts, sem *semantic) (Result, error) {
	mode := opts.Mode
	switch mode {
	case "":
		mode = ModeAuto
	case ModeAuto, ModeLexical, ModeSemantic:
	default:
		return Result{}, fmt.Errorf("%w: %q", ErrUnknownMode, opts.Mode)
	}
	if mode == ModeSemantic && sem == nil {
		return Result{}, ErrNoProvider
	}
	limit := opts.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	limit = min(limit, maxLimit)
	res := Result{Hits: []Hit{}, ModeUsed: ModeLexical, Semantic: SemanticOff}
	if mode == ModeSemantic {
		res.ModeUsed = ModeSemantic
	}
	match, words := ftsQuery(q)
	if match == "" {
		return res, nil
	}
	useSem := sem != nil && mode != ModeLexical
	for attempt := 0; ; attempt++ {
		var fp string
		var qvec []float32
		var embedErr error
		if useSem {
			fp, qvec, embedErr = s.embedQuery(ctx, sem, q)
		}
		out, err := s.searchIn(ctx, res, mode, useSem, match, words, opts, sem, fp, qvec, embedErr, attempt == 2, limit)
		if errors.Is(err, errModelMoved) {
			continue
		}
		return out, err
	}
}

// errModelMoved is the active model changing between a query's embedding and its snapshot.
var errModelMoved = errors.New("index: the active model changed")

// searchIn is one attempt of search, in one read transaction.
func (s *Store) searchIn(ctx context.Context, res Result, mode string, useSem bool, match string, words []string, opts QueryOpts,
	sem *semantic, fp string, qvec []float32, embedErr error, last bool, limit int) (Result, error) {
	err := s.read(ctx, func(tx *store.Tx) error {
		if useSem && embedErr == nil {
			now, err := s.activeModel(tx)
			if err != nil {
				return err
			}
			if now != fp {
				if !last {
					return errModelMoved
				}
				embedErr = fmt.Errorf("index: the active model keeps changing")
			}
		}
		switch {
		case useSem && errors.Is(embedErr, errOffline):
			// the active model is offline while a new one fills: by words, and saying so
			if mode == ModeSemantic {
				return ErrSwitching
			}
			useSem = false
			res.Semantic = SemanticSwitching
		case useSem && embedErr != nil:
			if mode == ModeSemantic {
				return fmt.Errorf("%w: %v", ErrEmbedFailed, embedErr)
			}
			useSem = false
			res.Semantic, res.SemanticError = SemanticError, ErrEmbedFailed.Error()
		case sem != nil && sem.switching():
			res.Semantic = SemanticSwitching // a lexical query while a new model fills
		case sem != nil:
			unready, err := s.sc.Documents(tx).With(store.DocSemanticReady, int64(0)).Exists()
			if err != nil {
				return err
			}
			res.Semantic = SemanticReady
			if unready {
				res.Semantic = SemanticPartial
			}
		}
		var lexical, semanticC []candidate
		var err error
		if mode != ModeSemantic {
			if lexical, err = s.lexicalHits(tx, match, opts); err != nil {
				return err
			}
		}
		if useSem {
			if semanticC, err = s.semanticHits(tx, sem, fp, qvec, opts); err != nil {
				return err
			}
			if mode == ModeAuto {
				res.ModeUsed = ModeHybrid
			}
		}
		fused := fuse(lexical, semanticC)
		if err := s.boost(tx, fused, words); err != nil {
			return err
		}
		ran := 0
		if mode != ModeSemantic {
			ran++
		}
		if useSem {
			ran++
		}
		for i := range fused {
			fused[i].hit.Relevance = min(1, fused[i].hit.Score*float64(rrfK+1)/float64(max(ran, 1)))
		}
		sort.Slice(fused, func(i, j int) bool { return fused[i].before(fused[j]) })
		perDoc := map[int64]int{}
		for _, c := range fused {
			if perDoc[c.docID] == perDocument {
				continue
			}
			perDoc[c.docID]++
			res.Hits = append(res.Hits, c.hit)
			if len(res.Hits) == limit {
				break
			}
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	return res, nil
}

// activeModel is the fingerprint of the active model, "" for none.
func (s *Store) activeModel(tx *store.Tx) (string, error) {
	m, err := s.sc.Models(tx).With(store.ModelActive, int64(1)).Get(store.ModelFP)
	if errors.Is(err, dao.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return m.FP, nil
}

// embedQuery embeds q with the model active now.
func (s *Store) embedQuery(ctx context.Context, sem *semantic, q string) (string, []float32, error) {
	var m *store.Model
	err := s.read(ctx, func(tx *store.Tx) error {
		var err error
		m, err = s.sc.Models(tx).With(store.ModelActive, int64(1)).Get()
		return err
	})
	if err != nil {
		return "", nil, fmt.Errorf("index: reading the active model: %w", err)
	}
	fp, dims := m.FP, int(derefInt(m.Dims))
	p, err := sem.provider(fp)
	if err != nil {
		return "", nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, queryTimeout)
	defer cancel()
	vecs, err := p.Embed(cctx, []string{q})
	if err != nil {
		return "", nil, err
	}
	if len(vecs) != 1 || len(vecs[0]) != dims {
		return "", nil, fmt.Errorf("index: the query's vector does not fit model %s", fp)
	}
	return fp, normalized(vecs[0]), nil
}

// ftsQuery makes an FTS5 query of the user's words: each one quoted, so no character of FTS5's
// syntax (AND, OR, NOT, NEAR, a column filter, parentheses, '-', '^') means anything but itself; the
// quoted words must all match. A '*' ending the last word keeps its meaning, a prefix. words are the
// query's words lowercased, for the tag boost.
func ftsQuery(q string) (match string, words []string) {
	fields := strings.Fields(q)
	var parts []string
	for i, f := range fields {
		prefix := i == len(fields)-1 && strings.HasSuffix(f, "*")
		f = strings.TrimRight(f, "*")
		// a word the tokenizer drops entirely (only punctuation) would make an empty phrase
		if !strings.ContainsFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) }) {
			continue
		}
		p := `"` + strings.ReplaceAll(f, `"`, `""`) + `"`
		if prefix {
			p += "*"
		}
		parts = append(parts, p)
		words = append(words, strings.ToLower(strings.TrimPrefix(f, "#")))
	}
	return strings.Join(parts, " "), words
}

// candidate is one chunk on its way to a hit.
type candidate struct {
	hit   Hit
	docID int64
	ord   int
}

// before orders hits: by score, then path, then position, so equal scores come out the same way
// every time.
func (a candidate) before(b candidate) bool {
	if a.hit.Score != b.hit.Score {
		return a.hit.Score > b.hit.Score
	}
	if a.hit.Path != b.hit.Path {
		return a.hit.Path < b.hit.Path
	}
	return a.ord < b.ord
}

// lexicalHits runs the FTS query over the alive chunks the filters admit, best first (BM25 weighs
// title 10, breadcrumb 5, tags 5, body 1), at most retrieverTop. The workspace and the filters are
// in the same WHERE as the MATCH, so they apply before the rank and the limit.
func (s *Store) lexicalHits(tx *store.Tx, match string, opts QueryOpts) ([]candidate, error) {
	d, ok, err := s.filtered(tx, alive(s.sc.Chunks(tx)), opts)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := d.Join(store.JoinFTS).WithPredicate(dao.Match(store.ChunkFTS, match)).
		OrderBy(dao.Asc(store.ChunkByRank), dao.Asc(store.ChunkByPath)).Limit(retrieverTop).
		Select(store.ChunkDoc, store.ChunkOrd, store.ChunkDocPath, store.ChunkBreadcrumb, store.ChunkByteStart,
			store.ChunkByteEnd, store.ChunkDocActiveGen, store.ChunkSnippet)
	if err != nil {
		return nil, fmt.Errorf("index: lexical search: %w", err)
	}
	out := make([]candidate, 0, len(rows))
	for _, r := range rows {
		out = append(out, candidate{docID: r.DocID, ord: int(r.Ord), hit: Hit{Path: r.DocPath, Breadcrumb: r.Breadcrumb,
			ByteStart: int(r.ByteStart), ByteEnd: int(r.ByteEnd), Generation: r.DocActiveGen, Snippet: r.Snippet, Via: []string{ModeLexical}}})
	}
	return out, nil
}

// filtered narrows a chunk query (joined to its document) to the query's filters: every tag, and
// any of the paths. ok is false when no document has every tag, so nothing can match.
func (s *Store) filtered(tx *store.Tx, d dao.DAO[*store.Chunk, store.ChunkField, int64], opts QueryOpts) (dao.DAO[*store.Chunk, store.ChunkField, int64], bool, error) {
	var docs map[int64]bool
	for _, t := range opts.Tags {
		tag := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		rows, err := s.sc.Tags(tx).With(store.DocValueValue, tag).Select(store.DocValueDoc)
		if err != nil {
			return nil, false, err
		}
		have := map[int64]bool{}
		for _, r := range rows {
			if docs == nil || docs[r.DocID] {
				have[r.DocID] = true
			}
		}
		docs = have
		if len(docs) == 0 {
			return nil, false, nil
		}
	}
	if docs != nil {
		ids := make([]any, 0, len(docs))
		for id := range docs {
			ids = append(ids, id)
		}
		d = d.With(store.ChunkDoc, ids...)
	}
	if len(opts.Paths) > 0 {
		var ors []dao.Predicate
		for _, p := range opts.Paths {
			p = strings.Trim(p, "/")
			if p == "" || p == "." {
				ors = nil
				break
			}
			ors = append(ors, under(`"document"."path"`, p))
		}
		if ors != nil {
			d = d.WithPredicate(dao.Or(ors...))
		}
	}
	return d, true, nil
}

// fuse scores candidates by reciprocal rank fusion: Σ over the retrievers that found a chunk of
// 1/(rrfK + its rank there). With one retriever the order is its own.
func fuse(lists ...[]candidate) []candidate {
	type key struct {
		doc int64
		ord int
	}
	at := map[key]int{}
	var out []candidate
	for _, list := range lists {
		for rank, c := range list {
			k := key{c.docID, c.ord}
			i, ok := at[k]
			if !ok {
				i = len(out)
				at[k] = i
				c.hit.Score = 0
				out = append(out, c)
			} else {
				out[i].hit.Via = append(out[i].hit.Via, c.hit.Via...)
			}
			out[i].hit.Score += 1 / float64(rrfK+rank+1)
		}
	}
	return out
}

// boost scales the fused scores: × (1 + 0.1·ln(1 + in-links)), counting the documents that link to
// the hit's document, and × 1.2 when a query word is one of its tags.
func (s *Store) boost(tx *store.Tx, cands []candidate, words []string) error {
	memo := map[int64]float64{}
	query := map[string]bool{}
	for _, w := range words {
		query[w] = true
	}
	for i := range cands {
		id := cands[i].docID
		f, ok := memo[id]
		if !ok {
			inLinks, err := dao.CountDistinct(s.sc.LinksOut(tx).With(store.LinkDst, id).Excluding(store.LinkSrc, id), store.LinkSrc)
			if err != nil {
				return err
			}
			f = 1 + linkBoost*math.Log(1+float64(inLinks))
			tags, err := s.tagsOf(tx, id)
			if err != nil {
				return err
			}
			for _, t := range tags {
				if query[t] {
					f *= tagBoost
					break
				}
			}
			memo[id] = f
		}
		cands[i].hit.Score *= f
	}
	return nil
}

func (s *Store) tagsOf(tx *store.Tx, docID int64) ([]string, error) {
	rows, err := s.sc.Tags(tx).With(store.DocValueDoc, docID).OrderBy(dao.Asc(store.ByKey)).Select(store.DocValueValue)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Value
	}
	return out, nil
}

func derefInt(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}
