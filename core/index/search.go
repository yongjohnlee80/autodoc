package index

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/search/vector"

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
// which no file contains, so a client can render them as it likes.
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
	Limit int // default 20, at most 200
	// Stages are the stages to run, each of StageLexical, StageSemantic and StageRerank once, at
	// least one retriever among them; nil is Mode's.
	Stages []string `json:",omitempty"` // the goldens' queries have none
	Mode   string   // ModeAuto (default: every stage), ModeLexical or ModeSemantic; "" with Stages
	Tags   []string // every hit's document has all of these
	Paths  []string // hits under these paths: a directory and what is below it, or one file
	// Facets are exact filters on the workspace schema's fields: a hit's document has, for every
	// field, one of its values. A field the schema does not declare is refused (ErrUnknownFacet).
	// The query's own field:value words for declared fields join them.
	Facets map[string][]string
	// Retrieval overrides the workspace's retrieval settings for this query; nil keeps them.
	Retrieval *Retrieval `json:",omitempty"`

	sectionsOnly bool // the abstract chunks are left out, in the retrievers themselves
}

// Retrieval is a query's override of its workspace's retrieval settings; a nil field keeps the
// workspace's.
type Retrieval struct {
	// AbstractChunk false leaves the abstract chunks out of this search, inside both retrievers,
	// before either takes its best; true searches them, where the index holds them.
	AbstractChunk *bool `json:",omitempty"`
	// DemoteSuperseded moves a superseded document's hits below its successor's.
	DemoteSuperseded *bool `json:",omitempty"`
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
	// Hold is "" for a document this daemon interprets; for one it holds (ADR 0216 §1.4),
	// HoldCurrent, HoldStale (its file changed since: the span may have moved) or HoldUnchecked.
	Hold string `json:",omitempty"` // the goldens' hits are interpreted: their JSON never names it
	// RankScore is the ranker's score for the hit, nil when no ranker scored it (ADR 0215);
	// comparable only within one Result.
	RankScore *float64 `json:",omitempty"`
	// SupersededBy is a successor of the hit's document, when its frontmatter relations say it was
	// superseded and the search demoted superseded documents; "" otherwise.
	SupersededBy string `json:",omitempty"`
}

// Result is search.query's answer: the hits, and what the search could use (ADR 0204 §4.4).
type Result struct {
	Hits     []Hit
	ModeUsed string // ModeLexical, ModeSemantic or ModeHybrid
	Semantic string // SemanticOff, SemanticReady, SemanticPartial, SemanticSwitching or SemanticError
	// SemanticError is the constant message of SemanticError: a provider's own error text is not
	// passed to clients.
	SemanticError string
	// Rank is what the re-ranking stage did (ADR 0215): off, ready (the ranker's order) or error
	// (recall order), with the model that ranked and a constant message.
	Rank RankState `json:",omitzero"` // a search with no stage: the goldens never name it
	// Stages is how the answer was made: the stages asked for, those that ran, and why the others
	// did not. It follows from the rest, so the goldens never name it.
	Stages Stages `json:"-"`
}

// RankState is a Result's re-ranking state.
type RankState struct {
	State, Model, Error string `json:",omitempty"`
}

// Search answers a query lexically: the store alone has no embedding provider, and no schema, so
// a field filter is refused.
func (s *Store) Search(ctx context.Context, q string, opts QueryOpts) (Result, error) {
	return answer(ctx, s, searchers{plain: defaultSearcher(searchStore{s: s}, nil)}, q, opts, nil, false)
}

// Search answers a query through the indexer's searcher: with the semantic tier when it has a
// provider (and the tier is not paused), and with the workspace's schema now.
func (x *Indexer) Search(ctx context.Context, q string, opts QueryOpts) (Result, error) {
	h, err := x.holding(ctx) // read once: the hits it marks never wait for a scan
	if err != nil {
		return Result{}, err
	}
	sch, _ := x.schema()
	paused := x.semanticPaused.Load()
	s := x.words
	if x.hybrid.plain != nil && !paused {
		s = x.hybrid
	}
	res, err := answer(ctx, x.store, s, q, opts, sch, paused)
	if err != nil {
		return res, err
	}
	for i := range res.Hits {
		if d, ok := h.of(res.Hits[i].Path); ok {
			res.Hits[i].Hold = d.state
		}
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
	return fp, vector.Normalize(vecs[0]), nil
}

// filtered narrows a chunk query (joined to its document) to the query's filters: every facet
// field and every tag, and any of the paths. ok is false when no document has them all, so nothing
// can match. Every retriever filters here, before its rank and its limit.
func (s *Store) filtered(tx *store.Tx, d dao.DAO[*store.Chunk, store.ChunkField, int64], opts QueryOpts) (dao.DAO[*store.Chunk, store.ChunkField, int64], bool, error) {
	if opts.sectionsOnly {
		d = d.With(store.ChunkKind, ChunkSection)
	}
	var docs map[int64]bool
	for field, values := range opts.Facets {
		vals := make([]any, len(values))
		for i, v := range values {
			vals[i] = v
		}
		rows, err := s.sc.Facets(tx).With(store.FacetName, field).With(store.FacetValue, vals...).Select(store.FacetDoc)
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
