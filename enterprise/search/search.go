// Package search is AutoDoc's enterprise search, for web applications: hybrid search, by words and
// by meaning, over a tenant that `autodoc --server` indexes from a .view into PostgreSQL
// (docs/decisions/server.md#a-headless-server-mode). A Go web application opens a Searcher on its own pool and searches in-process;
// `autodoc --server` serves the same Searcher over HTTP (POST /v1/search).
//
// Every search carries a Scope: for each access field of the tenant's policy, the values the
// caller may see. It is required, held to the policy as it is when the search runs, and applied by
// the store apart from the query and the filter, which may only narrow it: a query or a filter
// that names an access field is refused.
package search

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/query"
	"github.com/yongjohnlee80/golib/search/vector"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// Scope is what a caller may see: for every access field of the tenant's policy, the values it is
// allowed. A record is visible when it holds one of a field's values, for every field. Required on
// every Search, and given whole: every field of the policy, no other, each with values.
type Scope map[string][]string

// Request is a caller's query: its words, its mode, and its own narrowing, never access.
type Request struct {
	Query string
	Mode  gsearch.Mode // "" is auto: hybrid when vectors can answer, else by words
	// Filter narrows by tags, path prefixes and keyword facets. Its Facets name the tenant's
	// declared keyword facets only: values ORed within a field, fields ANDed, each value matched
	// exactly, case included (at most MaxFacetValues per field). An access field is the scope's
	// alone (ErrProtectedFacet); an undeclared field is ErrUnknownFacet; a number or a date facet
	// is a range's (ErrFacetType). A query word name:value for a declared keyword facet is merged
	// into them.
	Filter gsearch.Filter
	// Ranges narrow by the tenant's declared number and date facets, by field, ANDed with each
	// other and with the filter (at most MaxRanges).
	Ranges map[string]Range
	Limit  int    // 0 is the default (20); above the most (200) is refused
	Group  string // "" or "chunk" (default), or "document": the best chunk per export path
	// NoRerank skips the Searcher's ranker (WithRanker): the answer is in recall order, its
	// Ranking off. Window is how many of the best candidates the ranker reorders, 0 the
	// Searcher's own; outside rank.MinWindow to rank.MaxWindow it is refused (ErrWindow).
	NoRerank bool
	Window   int

	// keepTexts asks the search's View to keep its candidates' texts (Result.texts), for a ranker
	// after a merge across tenants
	keepTexts bool
}

// Range is a filter on a number or a date facet: a document has a value within every bound given,
// Gt and Lt exclusive, Gte and Lte inclusive; "" is no bound, and at least one is required. A
// bound is read as the facet's values are: a number as a decimal (NaN and the infinities
// refused), a date as YYYY-MM-DD (midnight UTC) or RFC 3339. A list value matches when any of its
// values is within the bounds.
type Range struct {
	Gt, Gte, Lt, Lte string
}

// The bounds on a request's filter.
const (
	MaxRanges      = 16 // ranges per request
	MaxFacetValues = 64 // values per keyword facet
)

// Result is a search's answer: golib's, each hit with the source row it was rendered from.
type Result struct {
	Hits          []Hit
	ModeUsed      gsearch.Mode
	Semantic      gsearch.State // "switching" while the tenant changes models: answered by words
	SemanticError string
	// Rank is what the Searcher's ranker did: off without one, for a request that skips it, and
	// where nothing is ranked (no words, or a listing by facets alone)
	Rank Ranking

	// texts are the candidates' texts its View kept (Request.keepTexts)
	texts map[pgstore.TextKey]string
}

// Hit is golib's hit and its SourceID: the id of the row the .view rendered it from. It is "" for
// a document indexed before AutoDoc kept source ids, until the tenant's next refresh writes it.
type Hit struct {
	gsearch.Hit
	SourceID string
}

// The refusals. Each is an errs sentinel, so errors.Is finds it through any wrapping.
var (
	// ErrScope is a scope that is not the tenant's access: a field of the policy missing or with
	// no value, a value empty, repeated or "*", or a field the policy does not have.
	ErrScope = errs.Sentinel(errs.ErrInvalidArgument, "search: the scope is not the tenant's access")
	// ErrProtectedFacet is a query or a filter that names an access field: access is the scope's
	// alone, and a facet could widen it.
	ErrProtectedFacet = errs.Sentinel(errs.ErrInvalidArgument, "search: the query or the filter names an access field")
	// ErrUnknownFacet is a filter facet or a range on a field the tenant does not declare.
	ErrUnknownFacet = errs.Sentinel(errs.ErrInvalidArgument, "search: the tenant keeps no such facet")
	// ErrFacetType is a filter on a facet of another type: a range on a keyword, equality on a
	// number or a date, or a bound that is not a value of its facet's type.
	ErrFacetType = errs.Sentinel(errs.ErrInvalidArgument, "search: the filter does not fit the facet's type")
	// ErrFilter is a filter past its bounds (MaxRanges, MaxFacetValues), a keyword facet with no
	// value, or a range with no bound or with a low bound above its high one.
	ErrFilter = errs.Sentinel(errs.ErrInvalidArgument, "search: the filter is out of bounds")
	// ErrLimit is a limit below zero or above the most.
	ErrLimit = errs.Sentinel(errs.ErrInvalidArgument, "search: the limit is out of range")
	// ErrGroup is a grouping mode other than the default chunks or one best chunk per document.
	ErrGroup = errs.Sentinel(errs.ErrInvalidArgument, "search: the group must be chunk or document")
	// ErrNoPolicy is a tenant the server has recorded no policy for: it is not searchable.
	ErrNoPolicy = pgstore.ErrNoPolicy
)

// The limits a Searcher answers with, unless WithLimits sets others.
const (
	DefaultLimit = 20
	MaxLimit     = 200
)

// Option configures a Searcher.
type Option func(*options)

type options struct {
	def, max int
	ann      *pgstore.ANN
	rank     *stage
}

// WithANN gives the Searcher's store the server's approximate search (pgstore.WithANN).
func WithANN(a *pgstore.ANN) Option { return func(o *options) { o.ann = a } }

// WithLimits sets the number of hits a Request with no Limit gets, and the most one may ask for.
// Values below 1 leave the defaults.
func WithLimits(def, most int) Option {
	return func(o *options) {
		if def > 0 {
			o.def = def
		}
		if most > 0 {
			o.max = most
		}
	}
}

// Searcher searches one tenant. It is safe for concurrent use.
type Searcher struct {
	st     *pgstore.Store
	eng    *gsearch.Engine[int64, *pgstore.View]
	docEng *gsearch.Engine[int64, *pgstore.View]
	def    int
	max    int
	rank   *stage // WithRanker's; nil is none
}

// Open binds a Searcher to tenant over conn: a pool whose connections have the server's schema on
// their search_path (pgstore.WithSchema). qe embeds the queries; it must embed with the model the
// server embeds the tenant with (QueryEmbedder over the same provider), and nil searches by words
// only. Open reads the tenant's policy once, to refuse a tenant with none (ErrNoPolicy) at once;
// every Search reads it again.
func Open(ctx context.Context, conn dao.DataConn, tenant string, qe gsearch.QueryEmbedder, opts ...Option) (*Searcher, error) {
	o := options{def: DefaultLimit, max: MaxLimit}
	for _, f := range opts {
		f(&o)
	}
	var sopts []pgstore.Option
	if o.ann != nil {
		sopts = append(sopts, pgstore.WithANN(o.ann))
	}
	st, err := pgstore.New(conn, tenant, sopts...)
	if err != nil {
		return nil, err
	}
	if _, err := st.Policy(ctx); err != nil {
		return nil, fmt.Errorf("search: tenant %s: %w", tenant, err)
	}
	eopts := []gsearch.Option{gsearch.WithLimits(o.def, o.max)}
	if qe != nil {
		eopts = append(eopts, gsearch.WithQueryEmbedder(qe))
	}
	return &Searcher{st: st, eng: gsearch.NewEngine[int64, *pgstore.View](scoped{st}, eopts...),
		docEng: gsearch.NewEngine[int64, *pgstore.View](scoped{st}, append(eopts, gsearch.WithPerDocument(1))...), def: o.def, max: o.max,
		rank: o.rank}, nil
}

// Search answers r within scope.
//
// The scope and the request are checked against the tenant's policy twice: first as the policy is
// now, so a refused request costs no query embedding; then inside the snapshot the search reads,
// where the policy and the documents are one, and the scope is applied apart from the query and
// the filter. The hits' source ids are read in that snapshot too.
func (s *Searcher) Search(ctx context.Context, scope Scope, r Request) (Result, error) {
	p, err := s.precheck(ctx, scope, r)
	if err != nil {
		return Result{}, err
	}
	check := func(p pgstore.Policy, access map[string][]string) ([]pgstore.Range, error) {
		return checkRequest(p, access, r)
	}
	ranking := s.rank != nil && !r.NoRerank
	c := &call{scope: scope, check: check, keep: ranking || r.keepTexts}
	eng := s.eng
	if r.Group == "document" {
		eng = s.docEng
	}
	q := gsearch.Query{Text: r.Query, Mode: r.Mode, Limit: r.Limit, Filter: r.Filter}
	if kw := keywords(p.Facets); len(kw) > 0 {
		q.Fields = kw
	}
	ctx = context.WithValue(ctx, callKey{}, c)
	var res gsearch.Result
	rk := Ranking{State: gsearch.RankOff}
	if f, listed, err := rangesAlone(q, r); err != nil {
		return Result{}, refusal(err)
	} else if listed {
		res, err = s.list(ctx, f, r.Limit)
		if err != nil {
			return Result{}, refusal(err)
		}
	} else if ranking {
		if res, rk, err = s.ranked(ctx, eng, q, r.Window, c); err != nil {
			return Result{}, refusal(err)
		}
	} else if res, err = eng.Search(ctx, q); err != nil {
		return Result{}, refusal(err)
	}
	out := Result{Hits: make([]Hit, 0, len(res.Hits)), ModeUsed: res.ModeUsed, Semantic: res.Semantic, SemanticError: res.SemanticError,
		Rank: rk, texts: c.texts}
	for _, h := range res.Hits {
		id, ok := c.ids[h.Path]
		if !ok {
			// every hit is a candidate of the view it came from: one that is not was not read
			// under the scope this search checked, so nothing of it is answered
			return Result{}, fmt.Errorf("search: the hit %s is not among the candidates of its view", h.Path)
		}
		out.Hits = append(out.Hits, Hit{Hit: h, SourceID: id})
	}
	return out, nil
}

// precheck is a request's first check, against the tenant's policy as it is now, before anything
// is embedded or read: the limit and the grouping, the scope (ErrScope) and the request
// (checkRequest). It returns the policy it read. The search checks again inside its snapshot.
func (s *Searcher) precheck(ctx context.Context, scope Scope, r Request) (pgstore.Policy, error) {
	if r.Limit < 0 || r.Limit > s.max {
		return pgstore.Policy{}, fmt.Errorf("%w: %d (0 to %d)", ErrLimit, r.Limit, s.max)
	}
	if r.Group != "" && r.Group != "chunk" && r.Group != "document" {
		return pgstore.Policy{}, fmt.Errorf("%w: %q", ErrGroup, r.Group)
	}
	if err := checkWindow(r.Window); err != nil {
		return pgstore.Policy{}, err
	}
	p, err := s.st.Policy(ctx)
	if err != nil {
		return pgstore.Policy{}, err
	}
	if err := refusal(pgstore.CheckScope(p, scope)); err != nil {
		return pgstore.Policy{}, err
	}
	if _, err := checkRequest(p, scope, r); err != nil {
		return pgstore.Policy{}, err
	}
	return p, nil
}

// rangesAlone reports whether r is answered by its filters alone, ranges among them: it has
// ranges and no word to search for. golib's engine lists only the documents its own filter
// narrows to, and never sees the ranges, which narrow the view. f is the filter to list by: q's,
// with the query's name:value words for declared keyword facets joined to its facets.
func rangesAlone(q gsearch.Query, r Request) (f gsearch.Filter, listed bool, err error) {
	if len(r.Ranges) == 0 {
		return gsearch.Filter{}, false, nil
	}
	text, facets, err := query.Facets(q.Text, q.Filter.Facets, q.Fields)
	if err != nil {
		return gsearch.Filter{}, false, err
	}
	f = q.Filter
	f.Facets = facets
	terms, _ := query.Terms(text)
	return f, len(terms) == 0, nil
}

// list answers a request of filters alone, as golib's engine lists one (ModeFacet): the documents
// the filter, the ranges and the scope admit, in path order, each as its first chunk, at most
// limit (0 is the Searcher's default). A query's name:value words for declared keyword facets
// join the filter, as the engine reads them.
func (s *Searcher) list(ctx context.Context, f gsearch.Filter, limit int) (gsearch.Result, error) {
	if limit <= 0 {
		limit = s.def
	}
	res := gsearch.Result{Hits: []gsearch.Hit{}, ModeUsed: gsearch.ModeFacet, Semantic: gsearch.StateOff}
	err := scoped{s.st}.View(ctx, func(v *pgstore.View) error {
		cands, err := v.List(ctx, f, limit)
		for _, c := range cands {
			res.Hits = append(res.Hits, gsearch.Hit{Path: c.Path, Breadcrumb: c.Breadcrumb, Snippet: c.Snippet, Generation: c.Generation,
				ByteStart: c.ByteStart, ByteEnd: c.ByteEnd, Via: []string{"facet"}, Score: 1, Relevance: 1})
		}
		return err
	})
	return res, err
}

// refusal is err with the store's and the engine's refusals said as this package's: a scope
// (pgstore.ErrNoAccess) as ErrScope, a range (pgstore.ErrRange) as ErrFilter, and the engine's
// facet refusals (a query's facets read against a declaration that changed since the request was
// checked) as ErrUnknownFacet and ErrFacetType.
func refusal(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, pgstore.ErrNoAccess) && !errors.Is(err, ErrScope):
		return fmt.Errorf("%w: %w", ErrScope, err)
	case errors.Is(err, pgstore.ErrRange) && !errors.Is(err, ErrFilter):
		return fmt.Errorf("%w: %w", ErrFilter, err)
	case errors.Is(err, query.ErrUnknownFacet) && !errors.Is(err, ErrUnknownFacet):
		return fmt.Errorf("%w: %w", ErrUnknownFacet, err)
	case errors.Is(err, query.ErrFacetValue) && !errors.Is(err, ErrFacetType):
		return fmt.Errorf("%w: %w", ErrFacetType, err)
	}
	return err
}

// keywordFields declares a tenant's keyword facets to golib's engine: a query's name:value word for
// one is a filter on it, its value as typed.
type keywordFields map[string]bool

func (k keywordFields) Declared(field string) bool { return k[field] }

func (k keywordFields) FacetValue(_, text string) (string, error) { return text, nil }

// keywords are facets' keyword fields; nil when there are none.
func keywords(facets map[string]string) keywordFields {
	var out keywordFields
	for name, typ := range facets {
		if typ == pgstore.FacetKeyword {
			if out == nil {
				out = keywordFields{}
			}
			out[name] = true
		}
	}
	return out
}

// facetToken is a query word golib's engine could read as a facet: field:value.
var facetToken = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*):(\S+)$`)

// checkRequest is what the policy refuses beyond the scope's shape, which pgstore holds, and the
// request's ranges as the store takes them. It refuses:
//
//   - a "*" among the scope's values: it belongs to a client's grants, not to a search;
//   - an access field named by the filter's facets, by a range, or by a field:value word of the
//     query (ErrProtectedFacet). Field names are compared regardless of case, so no spelling of an
//     access field passes;
//   - a facet or a range on a field the tenant does not declare (ErrUnknownFacet, naming the ones
//     it does);
//   - equality on a number or a date facet, a range on a keyword one, or a bound that is not a
//     value of its facet's type (ErrFacetType);
//   - more than MaxRanges ranges, more than MaxFacetValues values of one facet, a facet with no
//     value, a range with no bound or a low bound above its high one (ErrFilter).
//
// A field:value word of a field that is not declared is a word.
func checkRequest(p pgstore.Policy, scope map[string][]string, r Request) ([]pgstore.Range, error) {
	for f, vals := range scope {
		if slices.Contains(vals, "*") {
			return nil, fmt.Errorf("%w: %q is not a value of %s", ErrScope, "*", f)
		}
	}
	access := func(field string) bool {
		return slices.ContainsFunc(p.AccessFields, func(a string) bool { return strings.EqualFold(a, field) })
	}
	unknown := func(field string) error {
		if len(p.Facets) == 0 {
			return fmt.Errorf("%w: %s (the tenant declares no facet: filter by tags or paths)", ErrUnknownFacet, field)
		}
		return fmt.Errorf("%w: %s (the tenant's facets: %s)", ErrUnknownFacet, field, declared(p.Facets))
	}
	for _, f := range sortedKeys(r.Filter.Facets) {
		typ, ok := p.Facets[f]
		switch {
		case access(f):
			return nil, fmt.Errorf("%w: the filter's facet %s", ErrProtectedFacet, f)
		case !ok:
			return nil, unknown(f)
		case typ != pgstore.FacetKeyword:
			return nil, fmt.Errorf("%w: %s is a %s facet: filter it by a range", ErrFacetType, f, typ)
		case len(r.Filter.Facets[f]) == 0:
			return nil, fmt.Errorf("%w: the facet %s has no value", ErrFilter, f)
		}
	}
	// a keyword facet's values are the filter's and the query's name:value words for it together,
	// as the engine merges them: the bound holds for both
	values := map[string]int{}
	for f, vals := range r.Filter.Facets {
		values[f] = len(vals)
	}
	for _, w := range strings.Fields(r.Query) {
		if m := facetToken.FindStringSubmatch(w); m != nil && p.Facets[m[1]] == pgstore.FacetKeyword {
			values[m[1]]++
		}
	}
	for _, f := range sortedKeys(values) { // the first over the bound in name order, whatever the map's
		if n := values[f]; n > MaxFacetValues {
			return nil, fmt.Errorf("%w: the facet %s has %d values, the filter's and the query's (at most %d)", ErrFilter, f, n, MaxFacetValues)
		}
	}
	if len(r.Ranges) > MaxRanges {
		return nil, fmt.Errorf("%w: %d ranges (at most %d)", ErrFilter, len(r.Ranges), MaxRanges)
	}
	ranges := make([]pgstore.Range, 0, len(r.Ranges))
	for _, f := range sortedKeys(r.Ranges) {
		typ, ok := p.Facets[f]
		switch {
		case access(f):
			return nil, fmt.Errorf("%w: the range on %s", ErrProtectedFacet, f)
		case !ok:
			return nil, unknown(f)
		case typ == pgstore.FacetKeyword:
			return nil, fmt.Errorf("%w: %s is a keyword facet: filter it by its values", ErrFacetType, f)
		}
		pr, err := storeRange(f, typ, r.Ranges[f])
		if err != nil {
			return nil, err
		}
		ranges = append(ranges, pr)
	}
	for _, w := range strings.Fields(r.Query) {
		if m := facetToken.FindStringSubmatch(w); m != nil && access(m[1]) {
			return nil, fmt.Errorf("%w: the query's %s", ErrProtectedFacet, w)
		}
	}
	return ranges, nil
}

// storeRange is the range on field, a facet of typ, as the store takes it: each bound read as the
// facet's values are.
func storeRange(field, typ string, r Range) (pgstore.Range, error) {
	out := pgstore.Range{Field: field}
	for _, b := range []struct {
		text string
		to   **float64
	}{{r.Gt, &out.Gt}, {r.Gte, &out.Gte}, {r.Lt, &out.Lt}, {r.Lte, &out.Lte}} {
		if b.text == "" {
			continue
		}
		v, err := pgstore.TypedValue(typ, b.text)
		if err != nil {
			return pgstore.Range{}, fmt.Errorf("%w: the range on %s: %w", ErrFacetType, field, err)
		}
		*b.to = &v
	}
	if out.Gt == nil && out.Gte == nil && out.Lt == nil && out.Lte == nil {
		return pgstore.Range{}, fmt.Errorf("%w: the range on %s has no bound (gt, gte, lt or lte)", ErrFilter, field)
	}
	for _, lo := range []*float64{out.Gt, out.Gte} {
		for _, hi := range []*float64{out.Lt, out.Lte} {
			if lo != nil && hi != nil && *lo > *hi {
				return pgstore.Range{}, fmt.Errorf("%w: the range on %s has its low bound above its high one", ErrFilter, field)
			}
		}
	}
	return out, nil
}

// declared is facets' names and types, for a refusal: "bpm (number), status (keyword)".
func declared(facets map[string]string) string {
	parts := make([]string, 0, len(facets))
	for _, name := range sortedKeys(facets) {
		parts = append(parts, name+" ("+facets[name]+")")
	}
	return strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// callKey keys a Search's call on the context its engine runs with.
type callKey struct{}

// call is one Search, as its engine's store reads it: the scope and the check its views hold to,
// and the hits' source ids, read in the view the hits came from. Each Search has its own.
type call struct {
	scope Scope
	check func(pgstore.Policy, map[string][]string) ([]pgstore.Range, error)
	ids   map[string]string
	// keep asks the views to keep their candidates' texts, and texts are the answering view's
	keep  bool
	texts map[pgstore.TextKey]string
}

// scoped is the engine's store: every view it gives is ViewPolicy's, scoped by the Search's call.
// It fails closed: a context without a call is ErrScope, never an unscoped view.
type scoped struct{ st *pgstore.Store }

func (s scoped) View(ctx context.Context, fn func(v *pgstore.View) error) error {
	c, _ := ctx.Value(callKey{}).(*call)
	if c == nil {
		return fmt.Errorf("%w: a view asked for without a search's scope", ErrScope)
	}
	// the ranges are read against the policy of the view's own snapshot, where the request is
	// checked again, and narrow the view before anything of it is read
	var ranges []pgstore.Range
	check := func(p pgstore.Policy, access map[string][]string) error {
		var err error
		ranges, err = c.check(p, access)
		return err
	}
	return s.st.ViewPolicy(ctx, c.scope, check, func(v *pgstore.View) error {
		if err := v.Within(ranges); err != nil {
			return refusal(err)
		}
		if c.keep {
			v.KeepTexts()
		}
		if err := fn(v); err != nil {
			return err
		}
		ids, err := v.CandidateSourceIDs(ctx)
		if err != nil {
			return err
		}
		// replaced only by a view that answered: the engine's retry after a model change asks
		// again, and the hits it returns are the last answering view's
		c.ids, c.texts = ids, v.CandidateTexts()
		return nil
	})
}

// QueryEmbedder embeds queries with p for a Searcher: p's model fingerprint and the normalized
// vector, as `autodoc --server` stores the tenant's texts. p must be the server's model: the same
// provider, name and dimensions as its [embedding]; another model's vector answers by words, with
// semantic "error".
func QueryEmbedder(p embed.Provider) gsearch.QueryEmbedder {
	fp := p.Model().Fingerprint()
	return func(ctx context.Context, q string) (string, []float32, error) {
		// once per model and text in a search across tenants (WithQueryCache)
		vec, err := EmbedQuery(ctx, fp, q, func() ([]float32, error) {
			vecs, err := p.Embed(ctx, []string{q})
			if err != nil {
				return nil, fmt.Errorf("%w: %w", gsearch.ErrEmbedFailed, err)
			}
			if len(vecs) != 1 {
				return nil, fmt.Errorf("%w: the provider answered %d vectors for one query", gsearch.ErrEmbedFailed, len(vecs))
			}
			return vector.Normalize(vecs[0]), nil
		})
		if err != nil {
			return "", nil, err
		}
		return fp, vec, nil
	}
}
