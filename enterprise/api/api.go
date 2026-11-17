// Package api is `autodoc --server`'s HTTP API over enterprise search: POST /v1/search, with a
// bearer token per client (docs/decisions/server.md#an-http-api-with-a-token-per-client). A request
// is answered, in order:
//
//  1. the method and the path (405, 404);
//  2. the bearer token, its SHA-256 compared with every client's in constant time (401), so a
//     caller without a valid token learns nothing of tenants or bodies;
//  3. the body: JSON of at most 64 KiB, no unknown field, a query of at most 1 KiB (400, 413, 415);
//  4. the tenant, among the client's grants (403; a tenant that does not exist answers the same);
//  5. the scope, every field and value granted to the client (403);
//  6. the search itself, which holds the scope to the tenant's policy again (400 for a refusal).
//
// The token is never logged or kept; neither is the query's text, which may itself be private.
// Each request has one budget (Budget): its body read by the first deadline, its search ended by
// the second, and its whole answer written by the third.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	gsearch "github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/enterprise/ops"
	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// The paths the API answers: search (POST, a client's token), health (GET, public) and status
// (GET, an operator's token).
const (
	Path        = "/v1/search"
	HealthPath  = "/v1/health"
	StatusPath  = "/v1/status"
	MetricsPath = "/v1/metrics" // served only WithMetrics
)

// The request's bounds.
const (
	MaxBody  = 64 << 10 // bytes of JSON
	MaxQuery = 1 << 10  // bytes of the query's text
)

// Budget is one request's time, from the moment its handler starts: its body is read by Body, its
// search is ended by Search, and its whole answer is written by Write. Search + the answer's
// writing fit inside Write, so a valid request is never cut off before its answer.
type Budget struct {
	Body, Search, Write time.Duration
}

// DefaultBudget is the budget of every request unless WithBudget sets another.
//
// NOTE: deadlines, not waits. A client that sends its body slowly, or a search held up (the
// database, the provider), must not hold a connection and a handler for good: the body has 5s,
// the search 25s, and the answer is written by 30s from the start.
var DefaultBudget = Budget{Body: 5 * time.Second, Search: 25 * time.Second, Write: 30 * time.Second}

// Client is one caller of the API as the server resolved it: its name for the log, its bearer
// token's SHA-256, and its grants: by tenant, the values of each access field it may ask for,
// ["*"] for any value, which a request must still name.
type Client struct {
	Name   string
	Token  [sha256.Size]byte
	Grants map[string]map[string][]string
}

// Operator is one reader of the API's status as the server resolved it: its name for the log, its
// token's SHA-256, and the tenants whose status it reads (["*"]: every one). It cannot search.
type Operator struct {
	Name    string
	Token   [sha256.Size]byte
	Tenants []string
}

// Searcher searches one tenant: *search.Searcher.
type Searcher interface {
	Search(ctx context.Context, scope search.Scope, r search.Request) (search.Result, error)
}

// Option configures a Handler.
type Option func(*Handler)

// WithLogger is where the Handler logs each request: the client, the tenant, the status, the hits,
// the query's length and the time taken. The default logs nothing.
func WithLogger(l logger.Logger) Option { return func(h *Handler) { h.log = l } }

// WithOps serves health and status from c: without it, /v1/health and /v1/status are not found.
func WithOps(c *ops.Collector) Option { return func(h *Handler) { h.ops = c } }

// WithMetrics serves GET /v1/metrics, behind the operators' tokens as /v1/status
// is, from the same snapshot. Without it the path is not found, and is counted as any other.
func WithMetrics(on bool) Option { return func(h *Handler) { h.metrics = on } }

// DefaultTenantSearches is how many tenants' searches the requests naming several tenants run at
// once across every request, unless WithTenantSearches sets it.
const DefaultTenantSearches = 8

// WithTenantSearches sets how many tenants' searches the requests naming several tenants run at once,
// across every request ([http] max_tenant_searches): a request's tenants beyond it wait their turn
// under the request's own deadline. Below 1: DefaultTenantSearches.
func WithTenantSearches(n int) Option {
	return func(h *Handler) {
		if n > 0 {
			h.global = search.NewGlobal(n)
		}
	}
}

// WithOperators are the tokens that may read /v1/status.
func WithOperators(o []Operator) Option { return func(h *Handler) { h.operators = slices.Clone(o) } }

// WithBudget sets each request's budget; a zero part keeps the default's.
func WithBudget(b Budget) Option {
	return func(h *Handler) {
		if b.Body > 0 {
			h.budget.Body = b.Body
		}
		if b.Search > 0 {
			h.budget.Search = b.Search
		}
		if b.Write > 0 {
			h.budget.Write = b.Write
		}
	}
}

// Handler is the API. It is safe for concurrent use.
type Handler struct {
	clients   []Client
	operators []Operator
	tenants   map[string]Searcher
	global    *search.Global // a search across tenants' slots (WithTenantSearches)
	ops       *ops.Collector
	log       logger.Logger
	budget    Budget
	active    sync.WaitGroup // the requests being answered (Wait)

	// the requests answered since TakeCounts last took them, by status class, and the slowest
	ok, client4xx, server5xx, slowest atomic.Int64

	metrics bool
	// the requests answered since the server started, by path: /v1/metrics's, never reset
	totalsMu sync.Mutex
	totals   ops.HTTPTotals
}

// TakeCounts are the requests answered since it was last called, which it starts again from zero:
// the metrics lines' figures for their interval.
func (h *Handler) TakeCounts() ops.HTTPCounts {
	return ops.HTTPCounts{OK: h.ok.Swap(0), Client: h.client4xx.Swap(0), Server: h.server5xx.Swap(0),
		Slowest: time.Duration(h.slowest.Swap(0))}
}

// Totals are the requests answered since the server started, by path: a copy.
func (h *Handler) Totals() ops.HTTPTotals {
	h.totalsMu.Lock()
	defer h.totalsMu.Unlock()
	out := make(ops.HTTPTotals, len(h.totals))
	for p, t := range h.totals {
		c := ops.PathTotals{ByClass: map[string]uint64{}, Buckets: slices.Clone(t.Buckets), Count: t.Count, Sum: t.Sum}
		for k, v := range t.ByClass {
			c.ByClass[k] = v
		}
		out[p] = c
	}
	return out
}

// total adds one answered request on path to the totals.
func (h *Handler) total(path string, status int, took time.Duration) {
	h.totalsMu.Lock()
	defer h.totalsMu.Unlock()
	if h.totals == nil {
		h.totals = ops.HTTPTotals{}
	}
	t := h.totals[path]
	if t.ByClass == nil {
		t.ByClass = map[string]uint64{}
		t.Buckets = make([]uint64, len(ops.DurationBuckets))
	}
	t.ByClass[ops.ClassOf(status)]++
	for i, le := range ops.DurationBuckets {
		if took.Seconds() <= le {
			t.Buckets[i]++
		}
	}
	t.Count++
	t.Sum += took
	h.totals[path] = t
}

// count adds one answered request on path to the counts and the totals.
func (h *Handler) count(path string, status int, took time.Duration) {
	h.total(path, status, took)
	switch {
	case status >= 500:
		h.server5xx.Add(1)
	case status >= 400:
		h.client4xx.Add(1)
	default:
		h.ok.Add(1)
	}
	for {
		cur := h.slowest.Load()
		if int64(took) <= cur || h.slowest.CompareAndSwap(cur, int64(took)) {
			return
		}
	}
}

// New is the API for clients over tenants.
func New(clients []Client, tenants map[string]Searcher, opts ...Option) *Handler {
	h := &Handler{clients: slices.Clone(clients), tenants: tenants, log: logger.New(logger.WithWriter(io.Discard)), budget: DefaultBudget,
		global: search.NewGlobal(DefaultTenantSearches)}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Wait returns once every request the Handler is answering has been answered: a server stopping
// waits for it, after its http.Server stopped taking requests, before it closes what the searches
// use.
func (h *Handler) Wait() { h.active.Wait() }

// The request and response bodies.
type (
	request struct {
		Tenant string              `json:"tenant"`
		Query  string              `json:"query"`
		Mode   string              `json:"mode"`
		Scope  map[string][]string `json:"scope"`
		Filter *filter             `json:"filter"`
		Limit  int                 `json:"limit"`
		Group  string              `json:"group"`
		// Tenants is a search across tenants, each with its own scope, filter and weight; it
		// excludes Tenant, Scope and Filter
		Tenants []tenantRequest `json:"tenants"`
	}
	tenantRequest struct {
		Tenant string              `json:"tenant"`
		Scope  map[string][]string `json:"scope"`
		Filter *filter             `json:"filter"`
		Weight float64             `json:"weight"`
	}
	// globalResponse is a search across tenants' answer: each hit names its tenant, and each
	// tenant's mode and semantic state are said by name
	globalResponse struct {
		Hits    []hit                  `json:"hits"`
		Tenants map[string]tenantState `json:"tenants"`
	}
	tenantState struct {
		ModeUsed      string `json:"mode_used"`
		Semantic      string `json:"semantic"`
		SemanticError string `json:"semantic_error,omitempty"`
	}
	filter struct {
		Tags   []string            `json:"tags"`
		Paths  []string            `json:"paths"`
		Facets map[string][]string `json:"facets"`
		Ranges map[string]bounds   `json:"ranges"`
	}
	// bounds are a range's: each a number or a string (a date, or a number written as text)
	bounds struct {
		Gt  bound `json:"gt"`
		Gte bound `json:"gte"`
		Lt  bound `json:"lt"`
		Lte bound `json:"lte"`
	}
	response struct {
		Tenant        string `json:"tenant"`
		ModeUsed      string `json:"mode_used"`
		Semantic      string `json:"semantic"`
		SemanticError string `json:"semantic_error,omitempty"`
		Hits          []hit  `json:"hits"`
	}
	hit struct {
		Tenant     string   `json:"tenant,omitempty"` // a search across tenants' only
		Path       string   `json:"path"`
		Breadcrumb string   `json:"breadcrumb"`
		Snippet    string   `json:"snippet"`
		Generation int64    `json:"generation"`
		ByteStart  int      `json:"byte_start"`
		ByteEnd    int      `json:"byte_end"`
		Score      float64  `json:"score"`
		Relevance  float64  `json:"relevance"`
		Via        []string `json:"via"`
		SourceID   string   `json:"source_id"`
	}
	failure struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
)

// answer is how a request ended, for its response and its log line.
type answer struct {
	status  int
	code    string // "" for 200
	message string
	client  string
	tenant  string
	hits    int
	cause   error // logged, never answered
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.active.Add(1)
	defer h.active.Done()
	start := time.Now()
	rc := http.NewResponseController(w)
	// the request's budget; a connection that cannot take deadlines (a test's recorder) has none
	_ = rc.SetReadDeadline(start.Add(h.budget.Body))
	_ = rc.SetWriteDeadline(start.Add(h.budget.Write))
	ctx, cancel := context.WithDeadline(r.Context(), start.Add(h.budget.Search))
	defer cancel()
	switch r.URL.Path {
	case HealthPath:
		h.count(ops.PathHealth, h.serveHealth(ctx, w, r), time.Since(start))
		return
	case StatusPath:
		h.count(ops.PathStatus, h.serveStatus(ctx, w, r, start), time.Since(start))
		return
	case MetricsPath:
		if h.metrics && h.ops != nil {
			h.count(ops.PathMetrics, h.serveMetrics(ctx, w, r, start), time.Since(start))
			return
		}
	}
	// a path the API does not answer is counted as "other": no raw URL path becomes a label
	counted := ops.PathOther
	if r.URL.Path == Path {
		counted = ops.PathSearch
	}
	var queryLen int
	a, res := h.answer(ctx, w, r, &queryLen)
	defer func() { h.count(counted, a.status, time.Since(start)) }()
	if a.status == http.StatusOK {
		writeJSON(w, http.StatusOK, res)
	} else {
		var f failure
		f.Error.Code, f.Error.Message = a.code, a.message
		if a.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", `Bearer realm="autodoc"`)
		}
		writeJSON(w, a.status, f)
	}
	line := fmt.Sprintf("search: client %s, tenant %s: %d", orDash(a.client), orDash(a.tenant), a.status)
	if a.code != "" {
		line += " " + a.code
	}
	line += fmt.Sprintf(", %d hits, a query of %d bytes, in %v", a.hits, queryLen, time.Since(start).Round(time.Millisecond))
	if a.cause != nil {
		logger.Warning(h.log, a.cause, line)
		return
	}
	logger.Info(h.log, line)
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func refuse(status int, code, message string) answer {
	return answer{status: status, code: code, message: message}
}

// answer runs the request's checks and its search, in order.
func (h *Handler) answer(ctx context.Context, w http.ResponseWriter, r *http.Request, queryLen *int) (answer, any) {
	if r.URL.Path != Path {
		return refuse(http.StatusNotFound, "not_found", "the API answers "+Path+", "+HealthPath+" and "+StatusPath), response{}
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		return refuse(http.StatusMethodNotAllowed, "method_not_allowed", Path+" takes POST"), response{}
	}
	c := h.client(r.Header.Get("Authorization"))
	if c == nil {
		return refuse(http.StatusUnauthorized, "unauthorized", "a valid bearer token is required"), response{}
	}
	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		a := refuse(http.StatusUnsupportedMediaType, "unsupported_media_type", "the body must be application/json")
		a.client = c.Name
		return a, response{}
	}
	req, a := readRequest(w, r)
	a.client, a.tenant = c.Name, req.Tenant
	*queryLen = len(req.Query)
	if a.status != 0 {
		return a, response{}
	}
	if len(req.Tenants) > 0 {
		return h.answerTenants(ctx, c, req, a)
	}
	s, refused := h.granted(c, req.Tenant, req.Scope)
	if refused.status != 0 {
		refused.client, refused.tenant = a.client, a.tenant
		return refused, response{}
	}
	res, err := s.Search(ctx, search.Scope(req.Scope), searchRequest(req, req.Filter))
	if err != nil {
		e := failed(ctx, err)
		e.client, e.tenant = a.client, a.tenant
		return e, response{}
	}
	out := response{Tenant: req.Tenant, ModeUsed: string(res.ModeUsed), Semantic: string(res.Semantic), SemanticError: res.SemanticError, Hits: make([]hit, len(res.Hits))}
	for i, x := range res.Hits {
		out.Hits[i] = hitOf(x, "")
	}
	a.status, a.hits = http.StatusOK, len(out.Hits)
	return a, out
}

// answerTenants answers a search across tenants (the request's tenants): each tenant is checked as
// a single search's is, its grants and its scope within them, before any search runs, a refusal
// naming the tenant; then the library's Global searches them all, and its own refusals name the
// tenant too. The request's tenant, scope and filter are each tenant's, so a request giving them
// beside tenants is refused.
func (h *Handler) answerTenants(ctx context.Context, c *Client, req request, a answer) (answer, any) {
	if req.Tenant != "" || req.Scope != nil || req.Filter != nil {
		return refuse(http.StatusBadRequest, "bad_request", "tenants excludes tenant, scope and filter: give each tenant its own"), nil
	}
	if len(req.Tenants) > search.MaxTenants {
		return refuse(http.StatusBadRequest, "bad_tenants", fmt.Sprintf("%d tenants (at most %d)", len(req.Tenants), search.MaxTenants)), nil
	}
	names := make([]string, len(req.Tenants))
	served := make([]Searcher, len(req.Tenants))
	for i, t := range req.Tenants { // every tenant's grants, before anything else of any
		names[i] = t.Tenant
		s, refused := h.granted(c, t.Tenant, t.Scope)
		if refused.status != 0 {
			refused.client, refused.tenant = a.client, t.Tenant
			if refused.code == "scope_forbidden" {
				refused.message = "tenant " + t.Tenant + ": " + refused.message
			}
			return refused, nil
		}
		served[i] = s
	}
	ts := make([]search.TenantSearch, len(req.Tenants))
	for i, t := range req.Tenants {
		sr, ok := served[i].(*search.Searcher)
		if !ok {
			e := refuse(http.StatusInternalServerError, "internal", "the search failed")
			e.cause = fmt.Errorf("api: tenant %s's searcher cannot search across tenants", t.Tenant)
			return e, nil
		}
		r := searchRequest(req, t.Filter)
		ts[i] = search.TenantSearch{Tenant: t.Tenant, Searcher: sr, Scope: search.Scope(t.Scope), Filter: r.Filter, Ranges: r.Ranges, Weight: t.Weight}
	}
	a.tenant = strings.Join(names, ",")
	res, err := h.global.Search(ctx, search.GlobalRequest{Query: req.Query, Mode: gsearch.Mode(req.Mode), Limit: req.Limit, Group: req.Group, Tenants: ts})
	if err != nil {
		e := failed(ctx, err)
		e.client, e.tenant = a.client, a.tenant
		var te *search.TenantError
		if errors.As(err, &te) && !strings.Contains(e.message, "tenant "+te.Tenant) {
			e.message = "tenant " + te.Tenant + ": " + e.message
		}
		return e, nil
	}
	out := globalResponse{Hits: make([]hit, len(res.Hits)), Tenants: make(map[string]tenantState, len(res.Tenants))}
	for i, x := range res.Hits {
		out.Hits[i] = hitOf(x.Hit, x.Tenant)
	}
	for name, t := range res.Tenants {
		out.Tenants[name] = tenantState{ModeUsed: string(t.ModeUsed), Semantic: string(t.Semantic), SemanticError: t.SemanticError}
	}
	a.status, a.hits = http.StatusOK, len(out.Hits)
	return a, out
}

// granted is the Searcher of tenant, when the client may search it with scope: the tenant granted
// and served, every field of scope granted, and each of its values ("*" grants any). Otherwise the
// refusal: 403 forbidden for the tenant (one that doesn't exist answers the same), 403
// scope_forbidden for a field or a value.
func (h *Handler) granted(c *Client, tenant string, scope map[string][]string) (Searcher, answer) {
	grants, granted := c.Grants[tenant]
	s, served := h.tenants[tenant]
	if !granted || !served {
		return nil, refuse(http.StatusForbidden, "forbidden", "the token may not search tenant "+tenant)
	}
	for field, vals := range scope {
		g, ok := grants[field]
		if !ok {
			// a field the token has no grant for is refused, whatever the tenant's policy says now:
			// a field added to the policy since the grants were configured is not the token's
			return nil, refuse(http.StatusForbidden, "scope_forbidden", "the token has no grant for "+field)
		}
		if slices.Equal(g, []string{"*"}) {
			continue
		}
		for _, v := range vals {
			if !slices.Contains(g, v) {
				return nil, refuse(http.StatusForbidden, "scope_forbidden", fmt.Sprintf("the token may not ask for %s %q", field, v))
			}
		}
	}
	return s, answer{}
}

// searchRequest is the library's request of the body's query, mode, limit and grouping, with f
// (nil: none) as its filter.
func searchRequest(req request, f *filter) search.Request {
	r := search.Request{Query: req.Query, Mode: gsearch.Mode(req.Mode), Limit: req.Limit, Group: req.Group}
	if f != nil {
		r.Filter = gsearch.Filter{Tags: f.Tags, Paths: f.Paths, Facets: f.Facets}
		r.Ranges = f.ranges()
	}
	return r
}

// hitOf is a hit as the body carries it, its tenant named in a search across tenants.
func hitOf(x search.Hit, tenant string) hit {
	return hit{Tenant: tenant, Path: x.Path, Breadcrumb: x.Breadcrumb, Snippet: x.Snippet, Generation: x.Generation, ByteStart: x.ByteStart,
		ByteEnd: x.ByteEnd, Score: x.Score, Relevance: x.Relevance, Via: x.Via, SourceID: x.SourceID}
}

// client is the client whose token the Authorization header bears, or nil.
func (h *Handler) client(auth string) *Client {
	c, _ := h.holder(auth)
	return c
}

// holder is the client or the operator whose token the Authorization header bears, or neither.
// Every client's and operator's hash is compared, whichever matches, so the time taken does not tell
// which did, of which role, or how far one matched.
func (h *Handler) holder(auth string) (*Client, *Operator) {
	scheme, token, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return nil, nil
	}
	sum := sha256.Sum256([]byte(token))
	var client *Client
	var operator *Operator
	for i := range h.clients {
		if subtle.ConstantTimeCompare(sum[:], h.clients[i].Token[:]) == 1 && client == nil {
			client = &h.clients[i]
		}
	}
	for i := range h.operators {
		if subtle.ConstantTimeCompare(sum[:], h.operators[i].Token[:]) == 1 && operator == nil {
			operator = &h.operators[i]
		}
	}
	return client, operator
}

// serveHealth answers GET /v1/health, to anyone: 200 for ok and degraded, 503 for unavailable. It
// is not logged: a supervisor asks every few seconds. It returns the status answered.
func (h *Handler) serveHealth(ctx context.Context, w http.ResponseWriter, r *http.Request) int {
	switch {
	case h.ops == nil:
		return refuseNow(w, http.StatusNotFound, "not_found", "this server has no health endpoint")
	case r.Method != http.MethodGet:
		w.Header().Set("Allow", http.MethodGet)
		return refuseNow(w, http.StatusMethodNotAllowed, "method_not_allowed", HealthPath+" takes GET")
	}
	health := h.ops.Health(ctx)
	status := http.StatusOK
	if health.Status == ops.StatusUnavailable {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, health)
	return status
}

// serveStatus answers GET /v1/status, to an operator's token, for its tenants: a search client's
// token is 403 (its grants are per access value; status is tenant-wide), any other 401. It returns
// the status answered.
func (h *Handler) serveStatus(ctx context.Context, w http.ResponseWriter, r *http.Request, start time.Time) int {
	if h.ops == nil {
		return refuseNow(w, http.StatusNotFound, "not_found", "this server has no status endpoint")
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		return refuseNow(w, http.StatusMethodNotAllowed, "method_not_allowed", StatusPath+" takes GET")
	}
	client, op := h.holder(r.Header.Get("Authorization"))
	name, status := "-", http.StatusOK
	switch {
	case op != nil:
		name = op.Name
		snap := h.ops.Snapshot(ctx).Only(op.Tenants)
		writeJSON(w, status, snap)
	case client != nil:
		name, status = client.Name, refuseNow(w, http.StatusForbidden, "forbidden", "status is for an operator's token: a search client's grants do not cover it")
	default:
		w.Header().Set("WWW-Authenticate", `Bearer realm="autodoc"`)
		status = refuseNow(w, http.StatusUnauthorized, "unauthorized", "an operator's bearer token is required")
	}
	logger.Info(h.log, fmt.Sprintf("status: %s: %d, in %v", name, status, time.Since(start).Round(time.Millisecond)))
	return status
}

// serveMetrics is GET /v1/metrics: the exposition of the snapshot, filtered to the
// operator's tenants, and of the API's totals. Its auth is /v1/status's: an operator's token, a
// search client refused, none unauthorized.
func (h *Handler) serveMetrics(ctx context.Context, w http.ResponseWriter, r *http.Request, start time.Time) int {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		return refuseNow(w, http.StatusMethodNotAllowed, "method_not_allowed", MetricsPath+" takes GET")
	}
	client, op := h.holder(r.Header.Get("Authorization"))
	name, status := "-", http.StatusOK
	switch {
	case op != nil:
		name = op.Name
		var b strings.Builder
		if err := ops.WriteMetrics(&b, h.ops.Snapshot(ctx).Only(op.Tenants), h.Totals()); err != nil {
			status = refuseNow(w, http.StatusInternalServerError, "internal", "the metrics could not be written")
			break
		}
		w.Header().Set("Content-Type", ops.MetricsContentType)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, b.String())
	case client != nil:
		name, status = client.Name, refuseNow(w, http.StatusForbidden, "forbidden", "metrics are for an operator's token: a search client's grants do not cover it")
	default:
		w.Header().Set("WWW-Authenticate", `Bearer realm="autodoc"`)
		status = refuseNow(w, http.StatusUnauthorized, "unauthorized", "an operator's bearer token is required")
	}
	logger.Info(h.log, fmt.Sprintf("metrics: %s: %d, in %v", name, status, time.Since(start).Round(time.Millisecond)))
	return status
}

// refuseNow writes a refusal and returns its status.
func refuseNow(w http.ResponseWriter, status int, code, message string) int {
	var f failure
	f.Error.Code, f.Error.Message = code, message
	writeJSON(w, status, f)
	return status
}

// readRequest reads the body: at most MaxBody bytes of one JSON object with no unknown field, read
// within the body's deadline, its query at most MaxQuery bytes.
func readRequest(w http.ResponseWriter, r *http.Request) (request, answer) {
	var req request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBody))
	dec.DisallowUnknownFields()
	err := dec.Decode(&req)
	if err == nil && dec.More() {
		err = errors.New("more than one JSON value")
	}
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return req, refuse(http.StatusRequestEntityTooLarge, "too_large", fmt.Sprintf("the body is over %d bytes", MaxBody))
	case isTimeout(err):
		return req, refuse(http.StatusRequestTimeout, "request_timeout", "the body did not arrive in time")
	case err != nil:
		return req, refuse(http.StatusBadRequest, "bad_request", "the body is not a search request: "+err.Error())
	case len(req.Query) > MaxQuery:
		return req, refuse(http.StatusBadRequest, "bad_request", fmt.Sprintf("the query is over %d bytes", MaxQuery))
	}
	return req, answer{}
}

func isTimeout(err error) bool {
	var t interface{ Timeout() bool }
	return errors.As(err, &t) && t.Timeout()
}

// failed is a search's error as the API answers it: the library's refusals are the caller's to
// mend (400), a switch in semantic mode is for later (503), a provider that failed is upstream
// (502), a search past its deadline is unavailable (503); anything else is internal (500), its
// cause logged, not answered.
func failed(ctx context.Context, err error) answer {
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		// first: a provider or a database that ran out the request's budget says so in its own
		// words (an embedding failure, say), and the answer is still the budget's
		a := refuse(http.StatusServiceUnavailable, "unavailable", "the search did not finish in time")
		a.cause = err
		return a
	case errors.Is(err, search.ErrScope):
		return refuse(http.StatusBadRequest, "scope_required", err.Error())
	case errors.Is(err, search.ErrProtectedFacet):
		return refuse(http.StatusBadRequest, "protected_facet", err.Error())
	case errors.Is(err, search.ErrUnknownFacet):
		return refuse(http.StatusBadRequest, "unknown_facet", err.Error())
	case errors.Is(err, search.ErrFacetType):
		return refuse(http.StatusBadRequest, "facet_type", err.Error())
	case errors.Is(err, search.ErrFilter):
		return refuse(http.StatusBadRequest, "bad_filter", err.Error())
	case errors.Is(err, search.ErrTenants):
		return refuse(http.StatusBadRequest, "bad_tenants", err.Error())
	case errors.Is(err, search.ErrLimit):
		return refuse(http.StatusBadRequest, "bad_limit", err.Error())
	case errors.Is(err, search.ErrGroup):
		return refuse(http.StatusBadRequest, "bad_group", err.Error())
	case errors.Is(err, gsearch.ErrUnknownMode):
		return refuse(http.StatusBadRequest, "bad_mode", err.Error())
	case errors.Is(err, gsearch.ErrSwitching):
		return refuse(http.StatusServiceUnavailable, "switching", "the tenant is switching models: search by words, or again later")
	case errors.Is(err, gsearch.ErrEmbedFailed), errors.Is(err, gsearch.ErrNoProvider):
		return refuse(http.StatusBadGateway, "embed_failed", "the query could not be embedded")
	}
	a := refuse(http.StatusInternalServerError, "internal", "the search failed")
	a.cause = err
	return a
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	if status == http.StatusServiceUnavailable {
		w.Header().Set("Retry-After", "30")
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// bound is one of a range's bounds as the body gives it: a JSON number (kept as its digits) or a
// string, as text; absent or null is "".
type bound string

func (b *bound) UnmarshalJSON(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return err
	}
	switch v := v.(type) {
	case nil:
		*b = ""
	case string:
		*b = bound(v)
	case json.Number:
		*b = bound(v.String())
	default:
		return errors.New("a range's bound is a number or a string")
	}
	return nil
}

// ranges are the body's ranges as the library takes them; nil for none.
func (f filter) ranges() map[string]search.Range {
	if len(f.Ranges) == 0 {
		return nil
	}
	out := make(map[string]search.Range, len(f.Ranges))
	for field, b := range f.Ranges {
		out[field] = search.Range{Gt: string(b.Gt), Gte: string(b.Gte), Lt: string(b.Lt), Lte: string(b.Lte)}
	}
	return out
}
