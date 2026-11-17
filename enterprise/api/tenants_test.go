package api

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

// A search across tenants is checked as a single search is, tenant by tenant, before any search:
// tenant and tenants are exclusive, and so are the request's own scope and filter with tenants
// (each tenant has its own); more than MaxTenants is bad_tenants; a tenant not granted is 403
// forbidden, answered as a tenant that does not exist is; a scope value not granted is 403
// scope_forbidden, naming the tenant. Nothing is searched for any of them.
func TestASearchAcrossTenantsIsCheckedTenantByTenant(t *testing.T) {
	f := &fake{}
	srv, _, _ := api(t, f)
	tenants := func(entries ...string) string {
		return `{"query": "x", "tenants": [` + strings.Join(entries, ", ") + `]}`
	}
	music := `{"tenant": "music", "scope": {"label_id": ["A"]}}`
	for name, c := range map[string]struct {
		body   string
		status int
		code   string
		says   string
	}{
		"tenant and tenants": {`{"tenant": "music", "query": "x", "tenants": [` + music + `]}`, http.StatusBadRequest, "bad_request", "excludes tenant"},
		"a top-level scope":  {`{"query": "x", "scope": {"label_id": ["A"]}, "tenants": [` + music + `]}`, http.StatusBadRequest, "bad_request", "excludes"},
		"a top-level filter": {`{"query": "x", "filter": {"tags": ["a"]}, "tenants": [` + music + `]}`, http.StatusBadRequest, "bad_request", "excludes"},
		"too many":           {tenants(strings.Repeat(music+", ", 16) + music), http.StatusBadRequest, "bad_tenants", "at most 16"},
		"a value not granted": {tenants(music, `{"tenant": "any", "scope": {"label_id": ["Z"]}}`, `{"tenant": "music", "scope": {"label_id": ["C"]}}`),
			http.StatusForbidden, "scope_forbidden", "tenant music"},
		"a tenant not granted": {tenants(music, `{"tenant": "secret", "scope": {"label_id": ["A"]}}`), http.StatusForbidden, "forbidden", "tenant secret"},
	} {
		r := post(t, srv, bearer, "application/json", c.body)
		if r.status != c.status || r.code() != c.code || !strings.Contains(string(r.body), c.says) {
			t.Errorf("%s: %d %s %s; want %d %s saying %q", name, r.status, r.code(), r.body, c.status, c.code, c.says)
		}
	}
	secret := post(t, srv, bearer, "application/json", tenants(music, `{"tenant": "secret", "scope": {"label_id": ["A"]}}`))
	nobody := post(t, srv, bearer, "application/json", tenants(music, `{"tenant": "nobody", "scope": {"label_id": ["A"]}}`))
	if !bytes.Equal(nobody.body, bytes.ReplaceAll(secret.body, []byte("secret"), []byte("nobody"))) {
		t.Errorf("a tenant that does not exist answers otherwise than one not granted: %s / %s", nobody.body, secret.body)
	}
	if n := f.calls(); n != 0 {
		t.Errorf("the search was asked %d times for requests refused", n)
	}
}
