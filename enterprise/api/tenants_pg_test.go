package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	gsearch "github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// A search across tenants over HTTP answers each tenant's hits under its own scope, each hit naming
// its tenant, with each tenant's mode and semantic state by name; a single search's answer is as
// it was, its hits naming no tenant.
func TestASearchAcrossTenantsOverHTTP(t *testing.T) {
	ctx := context.Background()
	conn := pgtest.Scratch(t, pgstore.Migrate)
	open := func(tenant, field string, docs map[string]string) *search.Searcher {
		t.Helper()
		st, err := pgstore.New(conn, tenant)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.SetPolicy(ctx, pgstore.Policy{AccessFields: []string{field}, View: tenant}); err != nil {
			t.Fatal(err)
		}
		for path, value := range docs {
			body := "crimson " + path
			c := gsearch.Chunk{Ord: 0, Breadcrumb: path, Body: body, ByteEnd: len(body)}
			if _, err := st.Put(ctx, pgstore.Document{Path: path, Facets: map[string][]string{field: {value}}, Chunks: []gsearch.Chunk{c}, SourceID: path}); err != nil {
				t.Fatal(err)
			}
		}
		s, err := search.Open(ctx, conn, tenant, nil)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	releases := open("releases", "label_id", map[string]string{"release/1.md": "A", "release/2.md": "B"})
	payees := open("payees", "org_id", map[string]string{"payee/7.md": "7", "payee/8.md": "8"})
	clients := []Client{{Name: "web-app", Token: sha256.Sum256([]byte(token)), Grants: map[string]map[string][]string{
		"releases": {"label_id": {"*"}}, "payees": {"org_id": {"*"}}}}}
	srv := httptest.NewServer(New(clients, map[string]Searcher{"releases": releases, "payees": payees}, WithTenantSearches(2)))
	t.Cleanup(srv.Close)

	r := post(t, srv, bearer, "application/json", `{"query": "crimson", "tenants": [
		{"tenant": "releases", "scope": {"label_id": ["A"]}},
		{"tenant": "payees", "scope": {"org_id": ["7"]}, "weight": 2}]}`)
	if r.status != http.StatusOK {
		t.Fatalf("%d %s", r.status, r.body)
	}
	var got struct {
		Hits []struct {
			Tenant, Path string
		} `json:"hits"`
		Tenants map[string]struct {
			ModeUsed string `json:"mode_used"`
			Semantic string `json:"semantic"`
		} `json:"tenants"`
	}
	if err := json.Unmarshal(r.body, &got); err != nil {
		t.Fatal(err)
	}
	var hits []string
	for _, h := range got.Hits {
		hits = append(hits, h.Tenant+":"+h.Path)
	}
	if !slices.Equal(hits, []string{"payees:payee/7.md", "releases:release/1.md"}) {
		t.Errorf("hits %v; want each tenant's scoped record, the weighted one first", hits)
	}
	if len(got.Tenants) != 2 || got.Tenants["payees"].ModeUsed != "lexical" || got.Tenants["releases"].Semantic == "" {
		t.Errorf("tenants %+v; want both, by words", got.Tenants)
	}
	one := post(t, srv, bearer, "application/json", `{"tenant": "releases", "query": "crimson", "scope": {"label_id": ["A"]}}`)
	var single map[string]any
	if err := json.Unmarshal(one.body, &single); err != nil || one.status != http.StatusOK {
		t.Fatalf("a single search: %d %s %v", one.status, one.body, err)
	}
	if hits, _ := single["hits"].([]any); len(hits) != 1 || hits[0].(map[string]any)["tenant"] != nil || single["tenant"] != "releases" {
		t.Errorf("a single search's answer: %s; want it as before, its hits naming no tenant", one.body)
	}
}
