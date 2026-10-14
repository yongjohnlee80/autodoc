// Package search is AutoDoc's enterprise search, for web applications: hybrid search, by words and
// by meaning, over a tenant that `autodoc --server` indexes from a .view into PostgreSQL (ADR
// 1791762085). A Go web application opens a Searcher on its own pool and searches in-process;
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
	// Filter narrows by tags and path prefixes. Its Facets must be empty: a tenant keeps no facet
	// but its access fields, and those are the scope's alone (ErrProtectedFacet, ErrUnknownFacet).
	Filter gsearch.Filter
	Limit  int    // 0 is the default (20); above the most (200) is refused
	Group  string // "" or "chunk" (default), or "document": the best chunk per export path
}

// Result is a search's answer: golib's, each hit with the source row it was rendered from.
type Result struct {
	Hits          []Hit
	ModeUsed      gsearch.Mode
	Semantic      gsearch.State // "switching" while the tenant changes models: answered by words
	SemanticError string
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
	// ErrUnknownFacet is a filter facet on a field the tenant does not keep: it keeps no facet but
	// its access fields.
	ErrUnknownFacet = errs.Sentinel(errs.ErrInvalidArgument, "search: the tenant keeps no such facet")
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

type options struct{ def, max int }

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
	max    int
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
	st, err := pgstore.New(conn, tenant)
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
		docEng: gsearch.NewEngine[int64, *pgstore.View](scoped{st}, append(eopts, gsearch.WithPerDocument(1))...), max: o.max}, nil
}

// Search answers r within scope.
//
// The scope and the request are checked against the tenant's policy twice: first as the policy is
// now, so a refused request costs no query embedding; then inside the snapshot the search reads,
// where the policy and the documents are one, and the scope is applied apart from the query and
// the filter. The hits' source ids are read in that snapshot too.
func (s *Searcher) Search(ctx context.Context, scope Scope, r Request) (Result, error) {
	if r.Limit < 0 || r.Limit > s.max {
		return Result{}, fmt.Errorf("%w: %d (0 to %d)", ErrLimit, r.Limit, s.max)
	}
	if r.Group != "" && r.Group != "chunk" && r.Group != "document" {
		return Result{}, fmt.Errorf("%w: %q", ErrGroup, r.Group)
	}
	check := func(p pgstore.Policy, access map[string][]string) error { return checkRequest(p, access, r) }
	p, err := s.st.Policy(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := refusal(pgstore.CheckScope(p, scope)); err != nil {
		return Result{}, err
	}
	if err := check(p, scope); err != nil {
		return Result{}, err
	}
	c := &call{scope: scope, check: check}
	eng := s.eng
	if r.Group == "document" {
		eng = s.docEng
	}
	res, err := eng.Search(context.WithValue(ctx, callKey{}, c), gsearch.Query{Text: r.Query, Mode: r.Mode, Limit: r.Limit, Filter: r.Filter})
	if err != nil {
		return Result{}, refusal(err)
	}
	out := Result{Hits: make([]Hit, 0, len(res.Hits)), ModeUsed: res.ModeUsed, Semantic: res.Semantic, SemanticError: res.SemanticError}
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

// refusal is err with the store's refusal of a scope (pgstore.ErrNoAccess) said as ErrScope.
func refusal(err error) error {
	if err != nil && errors.Is(err, pgstore.ErrNoAccess) && !errors.Is(err, ErrScope) {
		return fmt.Errorf("%w: %w", ErrScope, err)
	}
	return err
}

// facetToken is a query word golib's engine could read as a facet: field:value.
var facetToken = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*):(\S+)$`)

// checkRequest is what the policy refuses beyond the scope's shape, which pgstore holds: a "*"
// among the scope's values (it belongs to a client's grants, not to a search), an access field
// named by the filter's facets or by a field:value word of the query, and any other facet, which
// the tenant does not keep. A field:value word of another field is a word: the engine declares no
// facet for an enterprise tenant. Field names are
// compared regardless of case, so no spelling of an access field passes.
func checkRequest(p pgstore.Policy, scope map[string][]string, r Request) error {
	for f, vals := range scope {
		if slices.Contains(vals, "*") {
			return fmt.Errorf("%w: %q is not a value of %s", ErrScope, "*", f)
		}
	}
	access := func(field string) bool {
		return slices.ContainsFunc(p.AccessFields, func(a string) bool { return strings.EqualFold(a, field) })
	}
	for f := range r.Filter.Facets {
		if access(f) {
			return fmt.Errorf("%w: the filter's facet %s", ErrProtectedFacet, f)
		}
		return fmt.Errorf("%w: %s (filter by tags or paths)", ErrUnknownFacet, f)
	}
	for _, w := range strings.Fields(r.Query) {
		if m := facetToken.FindStringSubmatch(w); m != nil && access(m[1]) {
			return fmt.Errorf("%w: the query's %s", ErrProtectedFacet, w)
		}
	}
	return nil
}

// callKey keys a Search's call on the context its engine runs with.
type callKey struct{}

// call is one Search, as its engine's store reads it: the scope and the check its views hold to,
// and the hits' source ids, read in the view the hits came from. Each Search has its own.
type call struct {
	scope Scope
	check func(pgstore.Policy, map[string][]string) error
	ids   map[string]string
}

// scoped is the engine's store: every view it gives is ViewPolicy's, scoped by the Search's call.
// It fails closed: a context without a call is ErrScope, never an unscoped view.
type scoped struct{ st *pgstore.Store }

func (s scoped) View(ctx context.Context, fn func(v *pgstore.View) error) error {
	c, _ := ctx.Value(callKey{}).(*call)
	if c == nil {
		return fmt.Errorf("%w: a view asked for without a search's scope", ErrScope)
	}
	return s.st.ViewPolicy(ctx, c.scope, c.check, func(v *pgstore.View) error {
		if err := fn(v); err != nil {
			return err
		}
		ids, err := v.CandidateSourceIDs(ctx)
		if err != nil {
			return err
		}
		// replaced only by a view that answered: the engine's retry after a model change asks
		// again, and the hits it returns are the last answering view's
		c.ids = ids
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
		vecs, err := p.Embed(ctx, []string{q})
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", gsearch.ErrEmbedFailed, err)
		}
		if len(vecs) != 1 {
			return "", nil, fmt.Errorf("%w: the provider answered %d vectors for one query", gsearch.ErrEmbedFailed, len(vecs))
		}
		return fp, vector.Normalize(vecs[0]), nil
	}
}
