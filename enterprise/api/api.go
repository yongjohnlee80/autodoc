// Package api is `autodoc --server`'s HTTP API over enterprise search: POST /v1/search, with a
// bearer token per client (ADR 1791762085 §4.5). A request is answered, in order:
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
	Path       = "/v1/search"
	HealthPath = "/v1/health"
	StatusPath = "/v1/status"
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
	ops       *ops.Collector
	log       logger.Logger
	budget    Budget
	active    sync.WaitGroup // the requests being answered (Wait)

	// the requests answered since TakeCounts last took them, by status class, and the slowest
	ok, client4xx, server5xx, slowest atomic.Int64
}

// TakeCounts are the requests answered since it was last called, which it starts again from zero:
// the metrics lines' figures for their interval.
func (h *Handler) TakeCounts() ops.HTTPCounts {
	return ops.HTTPCounts{OK: h.ok.Swap(0), Client: h.client4xx.Swap(0), Server: h.server5xx.Swap(0),
		Slowest: time.Duration(h.slowest.Swap(0))}
}

// count adds one answered request to the counts.
func (h *Handler) count(status int, took time.Duration) {
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
	h := &Handler{clients: slices.Clone(clients), tenants: tenants, log: logger.New(logger.WithWriter(io.Discard)), budget: DefaultBudget}
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
		Filter filter              `json:"filter"`
		Limit  int                 `json:"limit"`
	}
	filter struct {
		Tags   []string            `json:"tags"`
		Paths  []string            `json:"paths"`
		Facets map[string][]string `json:"facets"`
	}
	response struct {
		Tenant        string `json:"tenant"`
		ModeUsed      string `json:"mode_used"`
		Semantic      string `json:"semantic"`
		SemanticError string `json:"semantic_error,omitempty"`
		Hits          []hit  `json:"hits"`
	}
	hit struct {
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
		h.count(h.serveHealth(ctx, w, r), time.Since(start))
		return
	case StatusPath:
		h.count(h.serveStatus(ctx, w, r, start), time.Since(start))
		return
	}
	var queryLen int
	a, res := h.answer(ctx, w, r, &queryLen)
	defer func() { h.count(a.status, time.Since(start)) }()
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
func (h *Handler) answer(ctx context.Context, w http.ResponseWriter, r *http.Request, queryLen *int) (answer, response) {
	if r.URL.Path != Path {
		return refuse(http.StatusNotFound, "not_found", "the API answers "+Path), response{}
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
	grants, granted := c.Grants[req.Tenant]
	s, served := h.tenants[req.Tenant]
	if !granted || !served {
		a.status, a.code, a.message = http.StatusForbidden, "forbidden", "the token may not search tenant "+req.Tenant
		return a, response{}
	}
	for field, vals := range req.Scope {
		g, ok := grants[field]
		if !ok {
			// a field the token has no grant for is refused, whatever the tenant's policy says now:
			// a field added to the policy since the grants were configured is not the token's
			a.status, a.code, a.message = http.StatusForbidden, "scope_forbidden", "the token has no grant for "+field
			return a, response{}
		}
		if slices.Equal(g, []string{"*"}) {
			continue
		}
		for _, v := range vals {
			if !slices.Contains(g, v) {
				a.status, a.code, a.message = http.StatusForbidden, "scope_forbidden", fmt.Sprintf("the token may not ask for %s %q", field, v)
				return a, response{}
			}
		}
	}
	res, err := s.Search(ctx, search.Scope(req.Scope), search.Request{Query: req.Query, Mode: gsearch.Mode(req.Mode), Limit: req.Limit,
		Filter: gsearch.Filter{Tags: req.Filter.Tags, Paths: req.Filter.Paths, Facets: req.Filter.Facets}})
	if err != nil {
		e := failed(ctx, err)
		e.client, e.tenant = a.client, a.tenant
		return e, response{}
	}
	out := response{Tenant: req.Tenant, ModeUsed: string(res.ModeUsed), Semantic: string(res.Semantic), SemanticError: res.SemanticError, Hits: make([]hit, len(res.Hits))}
	for i, x := range res.Hits {
		out.Hits[i] = hit{Path: x.Path, Breadcrumb: x.Breadcrumb, Snippet: x.Snippet, Generation: x.Generation, ByteStart: x.ByteStart,
			ByteEnd: x.ByteEnd, Score: x.Score, Relevance: x.Relevance, Via: x.Via, SourceID: x.SourceID}
	}
	a.status, a.hits = http.StatusOK, len(out.Hits)
	return a, out
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
	case errors.Is(err, search.ErrLimit):
		return refuse(http.StatusBadRequest, "bad_limit", err.Error())
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
