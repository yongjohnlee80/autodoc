// Package ops is what `autodoc --server` tells an operator (ADR 1791762085 M4): its health, public
// and minimal (GET /v1/health); its status per tenant, for an operator's token (GET /v1/status);
// and its metrics lines in the log. One Collector serves them all, from the tenants' stores and the
// states their ingesters and the embedding worker publish. A failure is a code (opcode), never an
// error's text, which stays in the log.
package ops

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
	"github.com/yongjohnlee80/autodoc/enterprise/ingest"
	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// The collector's bounds.
//
// NOTE: deadlines and a cache, not waits. Health's ping, and each tenant's status reads, give up
// after Deadline, so a database that hangs makes health say so rather than hang it; status's
// database reads are kept CacheFor, so an operator polling status costs one round per tenant at
// most that often.
const (
	Deadline = 2 * time.Second
	CacheFor = 10 * time.Second
)

// Store is what the collector reads of a tenant's index: *pgstore.Store.
type Store interface {
	Cursor(ctx context.Context) (pgstore.Cursor, error)
	Refreshed(ctx context.Context) (pgstore.Refresh, error)
	Counts(ctx context.Context) (pgstore.Counts, error)
	Models(ctx context.Context) (active, target string, err error)
}

// Tenant is one tenant as the collector sees it: its store, and its ingester's published state.
type Tenant struct {
	Name  string
	Store Store
	State func() ingest.State
}

// Options are a Collector's sources.
type Options struct {
	Tenants []Tenant
	// Provider is the embedding worker's published health.
	Provider func() embedder.Health
	// Now is the database's clock, and health's ping (pgstore.Now on the server's pool).
	Now     func(ctx context.Context) (time.Time, error)
	Version string
	Started time.Time
	Clock   clock.Clock // the cache's clock; nil: time.Now
}

// Collector builds health and status. It is safe for concurrent use.
type Collector struct {
	o        Options
	stopping chan struct{}
	stopOnce sync.Once

	mu       sync.Mutex
	cached   *Snapshot
	cachedAt time.Time
}

// New is a Collector over o.
func New(o Options) *Collector {
	return &Collector{o: o, stopping: make(chan struct{})}
}

// Stop marks the server stopping: health says unavailable from now on.
func (c *Collector) Stop() { c.stopOnce.Do(func() { close(c.stopping) }) }

func (c *Collector) stopped() bool {
	select {
	case <-c.stopping:
		return true
	default:
		return false
	}
}

// The health statuses and check results.
const (
	StatusOK          = "ok"
	StatusDegraded    = "degraded"
	StatusUnavailable = "unavailable"
	CheckOK           = "ok"
	CheckDegraded     = "degraded"
	CheckFailing      = "failing"
)

// Health is GET /v1/health's answer: a status and its named checks, and nothing that names a
// tenant, a model, a count or the server's version.
type Health struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Health is the server's health now, never cached: the database pinged (within Deadline), every
// tenant's lock, the provider. The database failing, or the server stopping, is unavailable; a lock
// another server holds, or a provider down, is degraded (searches still answer, by words or for the
// other tenants).
func (c *Collector) Health(ctx context.Context) Health {
	h := Health{Status: StatusOK, Checks: map[string]string{"database": CheckOK, "tenants": CheckOK, "embedding": CheckOK}}
	pctx, cancel := context.WithTimeout(ctx, Deadline)
	_, err := c.o.Now(pctx)
	cancel()
	if err != nil {
		h.Checks["database"] = CheckFailing
	}
	for _, t := range c.o.Tenants {
		if !t.State().Locked {
			h.Checks["tenants"] = CheckDegraded
		}
	}
	if p := c.o.Provider(); p.State != embedder.ProviderUp || p.Error != opcode.None {
		h.Checks["embedding"] = CheckDegraded
	}
	switch {
	case err != nil || c.stopped():
		h.Status = StatusUnavailable
	case h.Checks["tenants"] != CheckOK || h.Checks["embedding"] != CheckOK:
		h.Status = StatusDegraded
	}
	return h
}

// Snapshot is GET /v1/status's answer, every tenant's: an operator's request is filtered to its
// tenants (Only).
type Snapshot struct {
	AsOf    time.Time      `json:"as_of"`
	Server  ServerState    `json:"server"`
	Tenants []TenantStatus `json:"tenants"`
}

// ServerState is the server's own part of a status.
type ServerState struct {
	Version   string         `json:"version"`
	Started   time.Time      `json:"started"`
	Embedding EmbeddingState `json:"embedding"`
}

// EmbeddingState is the provider's state.
type EmbeddingState struct {
	Model string      `json:"model"`
	State string      `json:"state"`
	Error opcode.Code `json:"error"`
}

// TenantStatus is one tenant's status.
type TenantStatus struct {
	Tenant      string      `json:"tenant"`
	Lock        string      `json:"lock"` // held, waiting, or none
	Cursor      CursorState `json:"cursor"`
	LastPass    RunState    `json:"last_pass"`
	LastRefresh RunState    `json:"last_refresh"`
	Quarantine  uint64      `json:"quarantine"`
	Documents   Documents   `json:"documents"`
	Models      ModelsState `json:"models"`
	Error       opcode.Code `json:"error"` // reading the tenant's state failed: its other figures are absent
}

// CursorState is where the tenant's incremental pass is, and how far behind the database's clock.
type CursorState struct {
	UpdatedAt  *time.Time `json:"updated_at"`
	ID         string     `json:"id"`
	LagSeconds *int64     `json:"lag_seconds"` // nil before the first page
}

// RunState is how a pass or a refresh last ended. For a refresh, Complete says whether it read every
// row (only a complete one deletes), and LastComplete is when the last complete one finished.
type RunState struct {
	At           *time.Time  `json:"at"`
	Read         int         `json:"read"`
	Written      int         `json:"written"`
	Quarantined  int         `json:"quarantined"`
	Complete     bool        `json:"complete,omitempty"`
	LastComplete *time.Time  `json:"last_complete,omitempty"`
	Error        opcode.Code `json:"error"`
}

// Documents are the tenant's documents and how many are ready for semantic search.
type Documents struct {
	Total uint64 `json:"total"`
	Ready uint64 `json:"ready"`
}

// ModelsState is the tenant's models, and its semantic state: off, switching, partial or ready.
type ModelsState struct {
	Active   string `json:"active"`
	Target   string `json:"target"`
	Semantic string `json:"semantic"`
}

// Only is s with the tenants named, in s's order; ["*"] is all of them.
func (s Snapshot) Only(tenants []string) Snapshot {
	if len(tenants) == 1 && tenants[0] == "*" {
		return s
	}
	out := s
	out.Tenants = nil
	for _, t := range s.Tenants {
		for _, name := range tenants {
			if t.Tenant == name {
				out.Tenants = append(out.Tenants, t)
			}
		}
	}
	return out
}

// Snapshot is the server's status: the database's part read at most every CacheFor (AsOf says
// when), the published states read now.
func (c *Collector) Snapshot(ctx context.Context) Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.o.Clock.Now()
	if c.cached == nil || now.Sub(c.cachedAt) >= CacheFor {
		s := c.read(ctx)
		c.cached, c.cachedAt = &s, now
	}
	s := *c.cached
	s.Tenants = make([]TenantStatus, len(c.cached.Tenants))
	copy(s.Tenants, c.cached.Tenants)
	p := c.o.Provider()
	s.Server = ServerState{Version: c.o.Version, Started: c.o.Started, Embedding: EmbeddingState{Model: p.Model, State: p.State, Error: p.Error}}
	for i, t := range c.o.Tenants {
		live(&s.Tenants[i], t.State())
	}
	return s
}

// read is the database's part of a snapshot, each tenant's within Deadline.
func (c *Collector) read(ctx context.Context) Snapshot {
	s := Snapshot{AsOf: c.o.Clock.Now(), Tenants: make([]TenantStatus, len(c.o.Tenants))}
	for i, t := range c.o.Tenants {
		s.Tenants[i] = c.tenant(ctx, t)
	}
	return s
}

func (c *Collector) tenant(ctx context.Context, t Tenant) TenantStatus {
	ts := TenantStatus{Tenant: t.Name}
	ctx, cancel := context.WithTimeout(ctx, Deadline)
	defer cancel()
	dbNow, err := c.o.Now(ctx)
	if err != nil {
		ts.Error = opcode.Database
		return ts
	}
	cur, err := t.Store.Cursor(ctx)
	if err != nil {
		ts.Error = opcode.Database
		return ts
	}
	if !cur.IsZero() {
		at := cur.UpdatedAt
		lag := int64(max(dbNow.Sub(at), 0) / time.Second)
		ts.Cursor = CursorState{UpdatedAt: &at, ID: cur.ID, LagSeconds: &lag}
	}
	ref, err := t.Store.Refreshed(ctx)
	if err != nil {
		ts.Error = opcode.Database
		return ts
	}
	if !ref.Finished.IsZero() {
		f := ref.Finished
		ts.LastRefresh.LastComplete = &f
	}
	counts, err := t.Store.Counts(ctx)
	if err != nil {
		ts.Error = opcode.Database
		return ts
	}
	ts.Quarantine = counts.Quarantined
	ts.Documents = Documents{Total: counts.Documents, Ready: counts.Documents - counts.Unready}
	active, target, err := t.Store.Models(ctx)
	if err != nil {
		ts.Error = opcode.Database
		return ts
	}
	ts.Models = ModelsState{Active: active, Target: target, Semantic: semantic(active, target, counts)}
	return ts
}

// semantic is a tenant's semantic state, as the search's own (pgstore's SemanticState) reads it.
func semantic(active, target string, c pgstore.Counts) string {
	switch {
	case active == "":
		return "off"
	case target != "" && target != active:
		return "switching"
	case c.Unready > 0:
		return "partial"
	}
	return "ready"
}

// live fills ts with what the ingester published: its lock, its last pass and refresh.
func live(ts *TenantStatus, st ingest.State) {
	switch {
	case st.Locked:
		ts.Lock = "held"
	case st.Waiting:
		ts.Lock = "waiting"
	default:
		ts.Lock = "none"
	}
	ts.LastPass = runState(st.LastPass, ts.LastPass.LastComplete)
	ts.LastRefresh = runState(st.LastRefresh, ts.LastRefresh.LastComplete)
	ts.LastPass.LastComplete = nil
	ts.LastRefresh.Complete = st.LastRefresh.Stats.Complete
}

func runState(o ingest.Outcome, lastComplete *time.Time) RunState {
	r := RunState{Read: o.Stats.Read, Written: o.Stats.Written, Quarantined: o.Stats.Quarantined, Error: o.Error, LastComplete: lastComplete}
	if !o.At.IsZero() {
		at := o.At
		r.At = &at
	}
	return r
}

// HTTPCounts are the API's requests over an interval, by status class, and the slowest one.
type HTTPCounts struct {
	OK, Client, Server int64
	Slowest            time.Duration
}

// Lines are the metrics lines of snapshot s, in logfmt: one for the server (the provider and the
// API's counts), then one per tenant, its own figures only. No token, query text or client name is
// in any.
func Lines(s Snapshot, http HTTPCounts) []string {
	out := []string{fmt.Sprintf("metrics scope=server provider=%s provider_error=%s http_ok=%d http_4xx=%d http_5xx=%d http_max_ms=%d",
		s.Server.Embedding.State, s.Server.Embedding.Error, http.OK, http.Client, http.Server, http.Slowest.Milliseconds())}
	for _, t := range s.Tenants {
		var b strings.Builder
		fmt.Fprintf(&b, "metrics scope=tenant tenant=%s lock=%s documents=%d ready=%d quarantined=%d semantic=%s",
			t.Tenant, t.Lock, t.Documents.Total, t.Documents.Ready, t.Quarantine, t.Models.Semantic)
		if t.Cursor.LagSeconds != nil {
			fmt.Fprintf(&b, " cursor_lag_s=%d", *t.Cursor.LagSeconds)
		}
		fmt.Fprintf(&b, " pass_error=%s refresh_error=%s error=%s", t.LastPass.Error, t.LastRefresh.Error, t.Error)
		out = append(out, b.String())
	}
	return out
}

// Report logs the metrics lines every interval until ctx ends; counts takes the API's counts for
// the interval (resetting them), or nil with no API.
func (c *Collector) Report(ctx context.Context, interval time.Duration, log logger.Logger, counts func() HTTPCounts) {
	// NOTE: a ticker, because a periodic report is what it is: no event announces that a minute
	// has passed, and the lines are a heartbeat an operator reads the trend of.
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var h HTTPCounts
			if counts != nil {
				h = counts()
			}
			for _, line := range Lines(c.Snapshot(ctx), h) {
				logger.Info(log, line)
			}
		}
	}
}
