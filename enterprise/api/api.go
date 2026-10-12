// Package api is `autodoc --server`'s HTTP API over enterprise search: POST /v1/search, with a
// bearer token per client (ADR 1791762085 §4.5). A request is answered, in order:
//
//  1. the method and the path (405, 404);
//  2. the bearer token, its SHA-256 compared with every client's in constant time (401), so a
//     caller without a valid token learns nothing of tenants or bodies;
//  3. the body: JSON of at most 64 KiB, no unknown field, a query of at most 1 KiB (400, 413, 415);
//  4. the tenant, among the client's grants (403; a tenant that does not exist answers the same);
//  5. the scope, every value granted to the client (403);
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
	"time"

	"github.com/yongjohnlee80/golib/logger"
	gsearch "github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// Path is the one path the API answers.
const Path = "/v1/search"

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

// Searcher searches one tenant: *search.Searcher.
type Searcher interface {
	Search(ctx context.Context, scope search.Scope, r search.Request) (search.Result, error)
}

// Option configures a Handler.
type Option func(*Handler)

// WithLogger is where the Handler logs each request: the client, the tenant, the status, the hits,
// the query's length and the time taken. The default logs nothing.
func WithLogger(l logger.Logger) Option { return func(h *Handler) { h.log = l } }

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
	clients []Client
	tenants map[string]Searcher
	log     logger.Logger
	budget  Budget
	active  sync.WaitGroup // the requests being answered (Wait)
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
	var queryLen int
	a, res := h.answer(ctx, w, r, &queryLen)
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
		if !ok || slices.Equal(g, []string{"*"}) {
			continue // a field no grant names is the search's to refuse: it is no access field
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

// client is the client whose token the Authorization header bears, or nil. Every client's hash is
// compared, whichever matches, so the time taken does not tell which did, or how far one matched.
func (h *Handler) client(auth string) *Client {
	scheme, token, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return nil
	}
	sum := sha256.Sum256([]byte(token))
	var found *Client
	for i := range h.clients {
		if subtle.ConstantTimeCompare(sum[:], h.clients[i].Token[:]) == 1 && found == nil {
			found = &h.clients[i]
		}
	}
	return found
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
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		a := refuse(http.StatusServiceUnavailable, "unavailable", "the search did not finish in time")
		a.cause = err
		return a
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
