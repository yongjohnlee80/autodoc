package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
	"github.com/yongjohnlee80/autodoc/enterprise/ingest"
	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
	"github.com/yongjohnlee80/autodoc/enterprise/ops"
)

// opsStore is a tenant's index with fixed answers.
type opsStore struct{ docs uint64 }

func (s opsStore) Cursor(context.Context) (pgstore.Cursor, error)     { return pgstore.Cursor{}, nil }
func (s opsStore) Refreshed(context.Context) (pgstore.Refresh, error) { return pgstore.Refresh{}, nil }
func (s opsStore) Counts(context.Context) (pgstore.Counts, error) {
	return pgstore.Counts{Documents: s.docs}, nil
}
func (s opsStore) Models(context.Context) (string, string, error) { return "m1", "", nil }

const (
	opsToken    = "an-operators-long-random-token-0123456789abcdef"
	otherOps    = "another-operators-long-random-token-0123456789"
	clientA     = "a-client-granted-label-A-long-token-0123456789"
	clientB     = "a-client-granted-label-B-long-token-0123456789"
	markerError = "relation track_marker_xyz does not exist"
)

// opsAPI is the handler with health and status over two tenants, two search clients of lm-catalog
// (label A, label B), an operator of lm-catalog and an operator of other; dbDown fails the database.
func opsAPI(t *testing.T, dbDown *atomic.Bool) *httptest.Server {
	t.Helper()
	state := func() ingest.State {
		return ingest.State{Locked: true, LastPass: ingest.Outcome{At: time.Now(), Error: opcode.View}}
	}
	c := ops.New(ops.Options{
		Tenants:  []ops.Tenant{{Name: "lm-catalog", Store: opsStore{docs: 7}, State: state}, {Name: "other", Store: opsStore{docs: 3}, State: state}},
		Provider: func() embedder.Health { return embedder.Health{State: embedder.ProviderUp, Model: "m1"} },
		Now: func(context.Context) (time.Time, error) {
			if dbDown != nil && dbDown.Load() {
				return time.Time{}, errors.New(markerError)
			}
			return time.Now(), nil
		},
		Version: "v-test",
	})
	clients := []Client{
		{Name: "web-a", Token: sha256.Sum256([]byte(clientA)), Grants: map[string]map[string][]string{"lm-catalog": {"label_id": {"A"}}}},
		{Name: "web-b", Token: sha256.Sum256([]byte(clientB)), Grants: map[string]map[string][]string{"lm-catalog": {"label_id": {"B"}}}},
	}
	operators := []Operator{
		{Name: "ops", Token: sha256.Sum256([]byte(opsToken)), Tenants: []string{"lm-catalog"}},
		{Name: "ops-other", Token: sha256.Sum256([]byte(otherOps)), Tenants: []string{"other"}},
	}
	srv := httptest.NewServer(New(clients, map[string]Searcher{"lm-catalog": &fake{}}, WithOps(c), WithOperators(operators)))
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path, token string) reply {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return do(t, req)
}

// GET /v1/health needs no token and answers its status and checks only: 200 while the database
// answers, 503 when it does not; it names no tenant, model, version or error. Another method is 405;
// a server without ops has no such endpoint.
func TestHealthIsPublicAndMinimal(t *testing.T) {
	var down atomic.Bool
	srv := opsAPI(t, &down)
	r := get(t, srv, HealthPath, "")
	var h ops.Health
	if err := json.Unmarshal(r.body, &h); err != nil || r.status != http.StatusOK || h.Status != ops.StatusOK {
		t.Fatalf("health: %d %s", r.status, r.body)
	}
	down.Store(true)
	r = get(t, srv, HealthPath, "")
	if r.status != http.StatusServiceUnavailable || !strings.Contains(string(r.body), `"unavailable"`) {
		t.Errorf("the database down: %d %s", r.status, r.body)
	}
	for _, not := range []string{"lm-catalog", "m1", "v-test", "track_marker"} {
		if strings.Contains(string(r.body), not) {
			t.Errorf("health holds %q: %s", not, r.body)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+HealthPath, nil)
	if r := do(t, req); r.status != http.StatusMethodNotAllowed || r.header.Get("Allow") != http.MethodGet {
		t.Errorf("POST health: %d", r.status)
	}
	bare := httptest.NewServer(New(nil, nil))
	t.Cleanup(bare.Close)
	if r := get(t, bare, HealthPath, ""); r.status != http.StatusNotFound {
		t.Errorf("health without ops: %d", r.status)
	}
}

// GET /v1/status is an operator's, for its tenants: two search clients of one tenant, granted
// different values, are 403 (their grants are per value; status is tenant-wide); no token is 401;
// each operator sees its own tenant and not the other's. An operator's token cannot search (401).
// Errors are codes. The mutants, named here: a client's token let into status; an operator seeing
// every tenant.
func TestStatusIsForOperatorsOnly(t *testing.T) {
	srv := opsAPI(t, nil)
	for _, tok := range []string{clientA, clientB} {
		if r := get(t, srv, StatusPath, tok); r.status != http.StatusForbidden || r.code() != "forbidden" {
			t.Errorf("a search client's token: %d %s", r.status, r.body)
		}
	}
	if r := get(t, srv, StatusPath, ""); r.status != http.StatusUnauthorized {
		t.Errorf("no token: %d", r.status)
	}
	for tok, want := range map[string]string{opsToken: "lm-catalog", otherOps: "other"} {
		r := get(t, srv, StatusPath, tok)
		var s ops.Snapshot
		if err := json.Unmarshal(r.body, &s); err != nil || r.status != http.StatusOK || len(s.Tenants) != 1 || s.Tenants[0].Tenant != want {
			t.Errorf("operator of %s: %d %s", want, r.status, r.body)
			continue
		}
		if s.Tenants[0].LastPass.Error != opcode.View || s.Server.Version != "v-test" {
			t.Errorf("operator of %s: %+v", want, s)
		}
	}
	if r := post(t, srv, "Bearer "+opsToken, "application/json", valid); r.status != http.StatusUnauthorized {
		t.Errorf("an operator searching: %d", r.status)
	}
}

// The counts are the requests answered since they were last taken, by status class, and the
// slowest; taking them starts them again from zero.
func TestTheCountsAreTakenPerInterval(t *testing.T) {
	f := &fake{}
	srv, h, _ := api(t, f)
	post(t, srv, bearer, "application/json", valid)
	post(t, srv, bearer, "application/json", valid)
	post(t, srv, "", "application/json", valid)
	c := h.TakeCounts()
	if c.OK != 2 || c.Client != 1 || c.Server != 0 || c.Slowest <= 0 {
		t.Errorf("counts %+v, want 2 ok, 1 4xx", c)
	}
	if c := h.TakeCounts(); c != (ops.HTTPCounts{}) {
		t.Errorf("taken again: %+v", c)
	}
}
