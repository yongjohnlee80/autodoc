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

// HighlightStart and HighlightEnd mark the matched terms in a Hit's Snippet: control characters,
// which no note contains, so a client can render them as it likes.
const (
	HighlightStart = "\x02"
	HighlightEnd   = "\x03"
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
	Score                     float64  // fused and boosted; comparable only within one Result
	Via                       []string // the retrievers that found it
}

// Result is search.query's answer: the hits, and what the search could use (ADR 0204 §4.4).
type Result struct {
	Hits     []Hit
	ModeUsed string // ModeLexical, ModeSemantic or ModeHybrid
	Semantic string // SemanticOff, SemanticReady, SemanticPartial or SemanticError
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
		return Result{}, fmt.Errorf("index: semantic search: no embedding provider: %w", errs.ErrUnsupported)
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
	var fp string
	var qvec []float32
	var embedErr error
	var tx dao.TxConn
	for attempt := 0; ; attempt++ {
		if useSem {
			fp, qvec, embedErr = s.embedQuery(ctx, sem, q)
		}
		var err error
		if tx, err = s.r.Begin(ctx); err != nil {
			return Result{}, err
		}
		if !useSem || embedErr != nil {
			break
		}
		var now string
		if err := scanOne(ctx, tx, &now, "SELECT fp FROM model WHERE active = 1"); err != nil && !errors.Is(err, errNoRow) {
			_ = tx.Rollback()
			return Result{}, err
		}
		if now == fp {
			break
		}
		_ = tx.Rollback()
		if attempt == 2 {
			embedErr = fmt.Errorf("index: the active model keeps changing")
			if tx, err = s.r.Begin(ctx); err != nil {
				return Result{}, err
			}
			break
		}
	}
	defer tx.Rollback()
	if useSem && embedErr != nil {
		if mode == ModeSemantic {
			return Result{}, fmt.Errorf("%w: %v", ErrEmbedFailed, embedErr)
		}
		useSem = false
		res.Semantic, res.SemanticError = SemanticError, ErrEmbedFailed.Error()
	} else if sem != nil {
		var unready int
		if err := scanOne(ctx, tx, &unready, "SELECT EXISTS (SELECT 1 FROM document WHERE semantic_ready = 0)"); err != nil {
			return Result{}, err
		}
		res.Semantic = SemanticReady
		if unready == 1 {
			res.Semantic = SemanticPartial
		}
	}
	var lexical, semanticC []candidate
	var err error
	if mode != ModeSemantic {
		if lexical, err = lexicalHits(ctx, tx, match, opts); err != nil {
			return Result{}, err
		}
	}
	if useSem {
		if semanticC, err = semanticHits(ctx, tx, sem, fp, qvec, opts); err != nil {
			return Result{}, err
		}
		if mode == ModeAuto {
			res.ModeUsed = ModeHybrid
		}
	}
	fused := fuse(lexical, semanticC)
	if err := boost(ctx, tx, fused, words); err != nil {
		return Result{}, err
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
	return res, nil
}

// embedQuery embeds q with the model active now.
func (s *Store) embedQuery(ctx context.Context, sem *semantic, q string) (string, []float32, error) {
	var fp, provider, name string
	var dims int
	if err := scanRow(ctx, s.r, []any{&fp, &provider, &name, &dims}, "SELECT fp, provider, name, dims FROM model WHERE active = 1"); err != nil {
		return "", nil, fmt.Errorf("index: reading the active model: %w", err)
	}
	p, err := sem.provider(fp, modelOf(fp, provider, name, dims))
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
// title 10, breadcrumb 5, tags 5, body 1), at most retrieverTop.
func lexicalHits(ctx context.Context, tx dao.Querier, match string, opts QueryOpts) ([]candidate, error) {
	q := `SELECT c.doc_id, c.ord, d.path, c.breadcrumb, c.byte_start, c.byte_end, d.active_gen,
			snippet(chunk_fts, 3, ?, ?, '…', 16)
		FROM chunk_fts JOIN chunk c ON c.id = chunk_fts.rowid JOIN document d ON d.id = c.doc_id
		WHERE chunk_fts MATCH ? AND c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen)`
	where, fargs := filterSQL(opts)
	q += where
	args := append([]any{HighlightStart, HighlightEnd, match}, fargs...)
	q += " ORDER BY bm25(chunk_fts, 10, 5, 5, 1), d.path, c.ord LIMIT ?"
	args = append(args, retrieverTop)
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("index: lexical search: %w", err)
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.docID, &c.ord, &c.hit.Path, &c.hit.Breadcrumb, &c.hit.ByteStart, &c.hit.ByteEnd,
			&c.hit.Generation, &c.hit.Snippet); err != nil {
			return nil, err
		}
		c.hit.Via = []string{ModeLexical}
		out = append(out, c)
	}
	return out, rows.Err()
}

// filterSQL is the query's filters as SQL over document d: every tag, and any of the paths.
func filterSQL(opts QueryOpts) (string, []any) {
	var q string
	var args []any
	for _, t := range opts.Tags {
		q += " AND EXISTS (SELECT 1 FROM doc_tag t WHERE t.doc_id = d.id AND t.tag = ?)"
		args = append(args, strings.ToLower(strings.TrimPrefix(strings.TrimSpace(t), "#")))
	}
	if len(opts.Paths) > 0 {
		var ors []string
		for _, p := range opts.Paths {
			p = strings.Trim(p, "/")
			if p == "" || p == "." {
				ors = []string{"1"}
				break
			}
			// p itself, and p/… : the range ["p/", "p0") holds exactly the paths below it
			ors = append(ors, "(d.path = ? OR (d.path >= ? AND d.path < ?))")
			args = append(args, p, p+"/", p+"0")
		}
		q += " AND (" + strings.Join(ors, " OR ") + ")"
	}
	return q, args
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
func boost(ctx context.Context, tx dao.Querier, cands []candidate, words []string) error {
	memo := map[int64]float64{}
	query := map[string]bool{}
	for _, w := range words {
		query[w] = true
	}
	for i := range cands {
		id := cands[i].docID
		f, ok := memo[id]
		if !ok {
			var inLinks int
			if err := scanOne(ctx, tx, &inLinks, "SELECT COUNT(DISTINCT src_doc) FROM link WHERE dst_doc = ? AND src_doc != ?", id, id); err != nil {
				return err
			}
			f = 1 + linkBoost*math.Log(1+float64(inLinks))
			tags, err := tagsOf(ctx, tx, id)
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

func tagsOf(ctx context.Context, q dao.Querier, docID int64) ([]string, error) {
	rows, err := q.QueryContext(ctx, "SELECT tag FROM doc_tag WHERE doc_id = ?", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
