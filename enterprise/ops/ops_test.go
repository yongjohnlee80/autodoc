package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
	"github.com/yongjohnlee80/autodoc/enterprise/ingest"
	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// store is a tenant's index with fixed answers, counting its reads; with hold set, each read waits
// for ctx to end.
type store struct {
	cursor pgstore.Cursor
	ref    pgstore.Refresh
	counts pgstore.Counts
	active string
	target string
	hold   bool
	reads  atomic.Int64
}

func (s *store) wait(ctx context.Context) error {
	s.reads.Add(1)
	if s.hold {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}
func (s *store) Cursor(ctx context.Context) (pgstore.Cursor, error)     { return s.cursor, s.wait(ctx) }
func (s *store) Refreshed(ctx context.Context) (pgstore.Refresh, error) { return s.ref, s.wait(ctx) }
func (s *store) Counts(ctx context.Context) (pgstore.Counts, error)     { return s.counts, s.wait(ctx) }
func (s *store) Models(ctx context.Context) (string, string, error) {
	return s.active, s.target, s.wait(ctx)
}

var dbNow = time.Date(2026, 10, 13, 9, 41, 7, 0, time.UTC)

// fixture is a collector over one tenant, lm-catalog, its states and the database's clock all the
// test's to set.
type fixture struct {
	st       *store
	state    atomic.Pointer[ingest.State]
	provider atomic.Pointer[embedder.Health]
	dbErr    atomic.Pointer[error]
	nows     atomic.Int64
	clock    atomic.Pointer[time.Time]
	c        *Collector
}

func newFixture() *fixture {
	f := &fixture{st: &store{cursor: pgstore.Cursor{UpdatedAt: dbNow.Add(-4 * time.Second), ID: "8812"},
		counts: pgstore.Counts{Documents: 10, Unready: 2, Quarantined: 1}, active: "m1"}}
	f.state.Store(&ingest.State{Locked: true})
	f.provider.Store(&embedder.Health{State: embedder.ProviderUp, Model: "m1"})
	start := dbNow
	f.clock.Store(&start)
	f.c = New(Options{
		Tenants:  []Tenant{{Name: "lm-catalog", Store: f.st, State: func() ingest.State { return *f.state.Load() }}},
		Provider: func() embedder.Health { return *f.provider.Load() },
		Now: func(ctx context.Context) (time.Time, error) {
			f.nows.Add(1)
			if p := f.dbErr.Load(); p != nil {
				return time.Time{}, *p
			}
			return dbNow, ctx.Err()
		},
		Version: "v-test", Started: dbNow.Add(-time.Hour),
		Clock: func() time.Time { return *f.clock.Load() },
	})
	return f
}

func (f *fixture) advance(d time.Duration) {
	t := f.clock.Load().Add(d)
	f.clock.Store(&t)
}

// Health is ok with everything up; degraded with the provider down or a lock not held; unavailable
// with the database failing or the server stopping. It pings the database on every call, so a
// database lost between two calls shows on the second. Its body names no tenant, model, count or
// version. The mutants, named here: health cached; a lock not held left ok.
func TestHealthIsFreshAndMinimal(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	if h := f.c.Health(ctx); h.Status != StatusOK || h.Checks["database"] != CheckOK {
		t.Fatalf("all up: %+v", h)
	}
	f.provider.Store(&embedder.Health{State: embedder.ProviderDown, Error: opcode.ProviderUnreachable})
	if h := f.c.Health(ctx); h.Status != StatusDegraded || h.Checks["embedding"] != CheckDegraded {
		t.Errorf("the provider down: %+v", h)
	}
	f.provider.Store(&embedder.Health{State: embedder.ProviderUp, Model: "m1"})
	f.state.Store(&ingest.State{Waiting: true})
	if h := f.c.Health(ctx); h.Status != StatusDegraded || h.Checks["tenants"] != CheckDegraded {
		t.Errorf("a lock waited for: %+v", h)
	}
	f.state.Store(&ingest.State{Locked: true})
	before := f.nows.Load()
	f.c.Health(ctx)
	if f.nows.Load() != before+1 {
		t.Errorf("health did not ping the database: %d pings", f.nows.Load()-before)
	}
	lost := errors.New("connection refused")
	f.dbErr.Store(&lost)
	h := f.c.Health(ctx)
	if h.Status != StatusUnavailable || h.Checks["database"] != CheckFailing {
		t.Errorf("the database lost since the last call: %+v", h)
	}
	b, _ := json.Marshal(h)
	for _, not := range []string{"lm-catalog", "m1", "v-test", "connection refused"} {
		if strings.Contains(string(b), not) {
			t.Errorf("health's body holds %q: %s", not, b)
		}
	}
	f.dbErr.Store(nil)
	f.c.Stop()
	if h := f.c.Health(ctx); h.Status != StatusUnavailable {
		t.Errorf("stopping: %+v", h)
	}
}

// A snapshot's database part is read at most every CacheFor: a second snapshot within it reads no
// store, one after reads again, and as_of says when it was read; the published states are read
// every time. The mutant, named here: no cache (every snapshot reads every store).
func TestTheSnapshotsDatabasePartIsCached(t *testing.T) {
	f := newFixture()
	ctx := context.Background()
	first := f.c.Snapshot(ctx)
	reads := f.st.reads.Load()
	if reads == 0 || !first.AsOf.Equal(dbNow) {
		t.Fatalf("the first snapshot read %d times, as of %v", reads, first.AsOf)
	}
	f.advance(CacheFor - time.Second)
	f.state.Store(&ingest.State{Waiting: true})
	second := f.c.Snapshot(ctx)
	if f.st.reads.Load() != reads || !second.AsOf.Equal(first.AsOf) || second.Tenants[0].Lock != "waiting" {
		t.Errorf("within the cache: %d new reads, as of %v, lock %s", f.st.reads.Load()-reads, second.AsOf, second.Tenants[0].Lock)
	}
	f.advance(time.Second)
	if third := f.c.Snapshot(ctx); f.st.reads.Load() == reads || third.AsOf.Equal(first.AsOf) {
		t.Errorf("past the cache: no new read, as of %v", third.AsOf)
	}
}

// A tenant whose store does not answer within Deadline is reported with a database error, its other
// figures absent; the snapshot does not wait on it longer.
func TestASlowStoreIsCutAtTheDeadline(t *testing.T) {
	f := newFixture()
	f.st.hold = true
	start := time.Now()
	s := f.c.Snapshot(context.Background())
	if took := time.Since(start); took > Deadline+time.Second {
		t.Errorf("a store that hangs held the snapshot %v", took)
	}
	if s.Tenants[0].Error != opcode.Database || s.Tenants[0].Documents.Total != 0 {
		t.Errorf("a store that hangs: %+v", s.Tenants[0])
	}
}

// The status of a tenant: its lag on the database's clock (never negative; none before the first
// page), its documents ready, its semantic state, its lock and last runs as its ingester published
// them, with their codes. The mutant, named here: the lag by the server's clock.
func TestATenantsStatus(t *testing.T) {
	f := newFixture()
	ran := dbNow.Add(-time.Minute)
	f.state.Store(&ingest.State{Locked: true,
		LastPass:    ingest.Outcome{At: ran, Stats: ingest.Stats{Read: 12, Written: 11, Quarantined: 1}, Error: opcode.View},
		LastRefresh: ingest.Outcome{At: ran, Stats: ingest.Stats{Complete: true}}})
	f.c.o.Clock = func() time.Time { return dbNow.Add(time.Hour) } // the server's clock, an hour off
	ts := f.c.Snapshot(context.Background()).Tenants[0]
	if ts.Cursor.LagSeconds == nil || *ts.Cursor.LagSeconds != 4 || ts.Cursor.ID != "8812" {
		t.Errorf("the cursor %+v, want 4s behind the database's clock", ts.Cursor)
	}
	if ts.Documents != (Documents{Total: 10, Ready: 8}) || ts.Quarantine != 1 || ts.Models.Semantic != "partial" {
		t.Errorf("figures %+v %d %+v", ts.Documents, ts.Quarantine, ts.Models)
	}
	if ts.Lock != "held" || ts.LastPass.Error != opcode.View || ts.LastPass.Read != 12 || !ts.LastRefresh.Complete || ts.LastPass.At == nil {
		t.Errorf("published state %+v / %+v", ts.LastPass, ts.LastRefresh)
	}

	for _, c := range []struct {
		cursor pgstore.Cursor
		lag    *int64
	}{
		{pgstore.Cursor{UpdatedAt: dbNow.Add(time.Minute), ID: "1"}, new(int64)}, // a future row: 0
		{pgstore.Cursor{}, nil}, // no page yet
	} {
		g := newFixture()
		g.st.cursor = c.cursor
		got := g.c.Snapshot(context.Background()).Tenants[0].Cursor.LagSeconds
		if (got == nil) != (c.lag == nil) || (got != nil && *got != *c.lag) {
			t.Errorf("cursor %+v: lag %v", c.cursor, got)
		}
	}
	for _, c := range []struct {
		active, target string
		unready        uint64
		want           string
	}{{"", "", 0, "off"}, {"m1", "m2", 0, "switching"}, {"m1", "", 3, "partial"}, {"m1", "", 0, "ready"}, {"m1", "m1", 0, "ready"}} {
		if got := semantic(c.active, c.target, pgstore.Counts{Unready: c.unready}); got != c.want {
			t.Errorf("semantic(%q, %q, %d) = %s, want %s", c.active, c.target, c.unready, got, c.want)
		}
	}
}

// An operator's snapshot holds its tenants only; ["*"] holds them all.
func TestASnapshotForAnOperatorsTenants(t *testing.T) {
	s := Snapshot{Tenants: []TenantStatus{{Tenant: "a"}, {Tenant: "b"}, {Tenant: "c"}}}
	names := func(s Snapshot) string {
		var out []string
		for _, t := range s.Tenants {
			out = append(out, t.Tenant)
		}
		return strings.Join(out, ",")
	}
	if got := names(s.Only([]string{"c", "a"})); got != "a,c" {
		t.Errorf("a and c: %s", got)
	}
	if got := names(s.Only([]string{"*"})); got != "a,b,c" {
		t.Errorf("*: %s", got)
	}
	if got := names(s.Only([]string{"z"})); got != "" {
		t.Errorf("none: %s", got)
	}
}

// The metrics lines: one server line with the provider and the API's counts, then one per tenant
// with its own figures; Report logs them every interval, taking the counts each time. The mutant,
// named here: the API's counts on every tenant line.
func TestTheMetricsLines(t *testing.T) {
	f := newFixture()
	s := f.c.Snapshot(context.Background())
	lines := Lines(s, HTTPCounts{OK: 118, Client: 3, Server: 0, Slowest: 412 * time.Millisecond})
	if len(lines) != 2 {
		t.Fatalf("lines %q", lines)
	}
	if want := "metrics scope=server provider=up provider_error= http_ok=118 http_4xx=3 http_5xx=0 http_max_ms=412"; lines[0] != want {
		t.Errorf("the server line\n%s\nwant\n%s", lines[0], want)
	}
	if !strings.HasPrefix(lines[1], "metrics scope=tenant tenant=lm-catalog lock=held documents=10 ready=8 quarantined=1 semantic=partial cursor_lag_s=4") ||
		strings.Contains(lines[1], "http_") {
		t.Errorf("the tenant line %s", lines[1])
	}

	var mu sync.Mutex
	var logged []string
	log := logger.Adapt(func(_ logger.Severity, payload any) {
		mu.Lock()
		defer mu.Unlock()
		logged = append(logged, fmt.Sprint(payload))
	})
	var taken atomic.Int64
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.c.Report(ctx, 20*time.Millisecond, log, func() HTTPCounts { taken.Add(1); return HTTPCounts{} })
	}()
	deadline := time.Now().Add(5 * time.Second)
	for taken.Load() < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if taken.Load() < 2 || len(logged) < 4 || !strings.HasPrefix(logged[0], "metrics scope=server") {
		t.Errorf("after %d intervals the log holds %q", taken.Load(), logged)
	}
}
