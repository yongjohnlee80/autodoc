package rpc

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// relationRig writes a --supersedes--> b --wikilink--> c into kb and waits until the relation is
// indexed, as a protocol-14 session sees it.
func relationRig(t *testing.T) *rig {
	t.Helper()
	r := serve(t)
	for p, c := range map[string]string{
		"a.md": "---\nsupersedes: [b.md]\n---\n# A\n",
		"b.md": "# B\n\n[[c]]\n",
		"c.md": "---\ntags: [zeta, alpha]\n---\n# C\n",
	} {
		if _, err := r.fsys.WriteFile(context.Background(), p, strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	cur, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "a's relation and b's link indexed", func() bool {
		res, err := cur.Call(context.Background(), "graph.neighborhood", "kb", "a.md", int64(2))
		if err != nil {
			return false // a.md is not indexed yet
		}
		nodes, _ := res.(map[string]any)["nodes"].([]any)
		return len(nodes) == 3
	})
	return r
}

// TestAProtocol13SessionSeesTodaysGraph: below protocol 14 a session gets the body links alone, in
// every graph verb, and its neighbourhood neither holds nor passes through a document reached only
// by a relation; at 14 the relation is there, with its kind.
func TestAProtocol13SessionSeesTodaysGraph(t *testing.T) {
	r := relationRig(t)
	old, _, err := helloAs(r, 13, "an-old-client")
	if err != nil {
		t.Fatal(err)
	}
	cur, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}

	nb := call(t, old, "graph.neighborhood", "kb", "a.md", int64(2)).(map[string]any)
	if !reflect.DeepEqual(nb["nodes"], []any{"a.md"}) || len(nb["edges"].([]any)) != 0 {
		t.Errorf("protocol 13's neighbourhood of a.md: %v, want a.md alone, no edges", nb)
	}
	nb = call(t, cur, "graph.neighborhood", "kb", "a.md", int64(2)).(map[string]any)
	want := []any{
		map[string]any{"src": "a.md", "dst": "b.md", "kind": "supersedes"},
		map[string]any{"src": "b.md", "dst": "c.md", "kind": "wikilink"},
	}
	if !reflect.DeepEqual(nb["nodes"], []any{"a.md", "b.md", "c.md"}) || !reflect.DeepEqual(nb["edges"], want) {
		t.Errorf("protocol 14's neighbourhood of a.md: %v", nb)
	}

	if links := call(t, old, "graph.links", "kb", "a.md").([]any); len(links) != 0 {
		t.Errorf("protocol 13's links of a.md: %v, want none", links)
	}
	links := call(t, cur, "graph.links", "kb", "a.md").([]any)
	if len(links) != 1 || links[0].(map[string]any)["kind"] != "supersedes" || links[0].(map[string]any)["path"] != "b.md" {
		t.Errorf("protocol 14's links of a.md: %v", links)
	}
	if back := call(t, old, "graph.backlinks", "kb", "b.md").([]any); len(back) != 0 {
		t.Errorf("protocol 13's backlinks of b.md: %v, want none", back)
	}
	if back := call(t, cur, "graph.backlinks", "kb", "b.md").([]any); len(back) != 1 {
		t.Errorf("protocol 14's backlinks of b.md: %v, want a.md's", back)
	}
}

// TestKindsNeedsProtocol14: the kinds option narrows a protocol-14 answer, and a session below 14
// that sends it is refused, not answered as if it had not.
func TestKindsNeedsProtocol14(t *testing.T) {
	r := relationRig(t)
	old, _, err := helloAs(r, 13, "an-old-client")
	if err != nil {
		t.Fatal(err)
	}
	cur, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}
	only := map[string]any{"kinds": []any{"supersedes"}}
	nb := call(t, cur, "graph.neighborhood", "kb", "a.md", int64(2), only).(map[string]any)
	if !reflect.DeepEqual(nb["nodes"], []any{"a.md", "b.md"}) {
		t.Errorf("kinds [supersedes] at 14: %v, want a.md and b.md", nb)
	}
	for _, verb := range []struct {
		name string
		args []any
	}{
		{"graph.neighborhood", []any{"kb", "a.md", int64(2), only}},
		{"graph.links", []any{"kb", "a.md", only}},
		{"graph.backlinks", []any{"kb", "b.md", only}},
		{"graph.unresolved", []any{"kb", only}},
	} {
		_, err := old.Call(context.Background(), verb.name, verb.args...)
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.Contains(re.Message, "protocol 14") {
			t.Errorf("%s with kinds at 13: %v, want invalid params naming protocol 14", verb.name, err)
		}
	}
}

// TestSearchDemotesOnRequest: search.query's retrieval option demotes a superseded document below its
// successor for one query, its hit naming the successor; an unknown retrieval option is refused.
func TestSearchDemotesOnRequest(t *testing.T) {
	r := serve(t)
	for p, c := range map[string]string{
		"old.md": "# Old\n\nkestrel kestrel kestrel\n",
		"new.md": "---\nsupersedes: [old.md]\n---\n# New\n\nkestrel\n",
	} {
		if _, err := r.fsys.WriteFile(context.Background(), p, strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	cli, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}
	search := func(opts map[string]any) []any {
		res, err := cli.Call(context.Background(), "search.query", "kb", "kestrel", opts)
		if err != nil {
			return nil
		}
		return res.(map[string]any)["hits"].([]any)
	}
	eventually(t, "both indexed, the relation resolved", func() bool {
		hits := search(map[string]any{"mode": "lexical", "retrieval": map[string]any{"demote_superseded": true}})
		return len(hits) == 2 && hits[0].(map[string]any)["path"] == "new.md"
	})
	hits := search(map[string]any{"mode": "lexical", "retrieval": map[string]any{"demote_superseded": true}})
	if hits[1].(map[string]any)["superseded_by"] != "new.md" {
		t.Errorf("the superseded hit: %v", hits[1])
	}
	if _, ok := hits[0].(map[string]any)["superseded_by"]; ok {
		t.Errorf("the successor's hit carries superseded_by: %v", hits[0])
	}
	plain := search(map[string]any{"mode": "lexical"})
	if len(plain) != 2 || plain[0].(map[string]any)["path"] != "old.md" {
		t.Errorf("without the option: %v, want old.md first", plain)
	}
	_, err = cli.Call(context.Background(), "search.query", "kb", "kestrel", map[string]any{"retrieval": map[string]any{"colour": true}})
	var re *golibrpc.Error
	if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams {
		t.Errorf("an unknown retrieval option: %v, want invalid params", err)
	}
}

// graph.resolve answers where a link as written reaches, one no file holds yet included; a session
// below protocol 18 does not have it.
func TestGraphResolveFollowsALinkAsWritten(t *testing.T) {
	r := relationRig(t)
	cur, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}
	for raw, want := range map[string]map[string]any{
		"[[c]]":                    {"path": "c.md", "reason": ""},
		"[[C|see c]]":              {"path": "c.md", "reason": ""}, // in no file: an editor's unsaved link
		"[[gone]]":                 {"path": "", "reason": "missing"},
		"[w](https://example.com)": {"path": "", "reason": ""},
	} {
		if got := call(t, cur, "graph.resolve", "kb", "a.md", raw); !reflect.DeepEqual(got, want) {
			t.Errorf("graph.resolve %s: %v, want %v", raw, got, want)
		}
	}
	old, _, err := helloAs(r, 17, "an-older-client")
	if err != nil {
		t.Fatal(err)
	}
	if err := callErr(old, "graph.resolve", "kb", "a.md", "[[c]]"); code(err) != golibrpc.CodeMethodNotFound {
		t.Errorf("graph.resolve at protocol 17: %v, want an unknown method", err)
	}
}

// TestAProtocol22SessionSeesNoTodoRelations (ADR 1792015212 §2.4): from protocol 23 a task's review
// and blocked relations, and its links to a file outside the workspace (external), are in the graph;
// a session below 23 sees the graph it knew, without them.
func TestAProtocol22SessionSeesNoTodoRelations(t *testing.T) {
	r := serve(t)
	if err := r.fsys.MkdirAll(context.Background(), "tasks"); err != nil {
		t.Fatal(err)
	}
	for p, c := range map[string]string{
		"tasks/t.md": "---\nreview: [b.md]\nadr: [$X/q.md]\nblocked: [u]\n---\n# T\n",
		"tasks/u.md": "# U\n",
		"b.md":       "# B\n",
	} {
		if _, err := r.fsys.WriteFile(context.Background(), p, strings.NewReader(c)); err != nil {
			t.Fatal(err)
		}
	}
	cur, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}
	old, _, err := helloAs(r, 22, "an-old-client")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"review": "b.md|", "adr": "|/elsewhere/q.md", "blocked": "tasks/u.md|"}
	got := map[string]string{}
	// the three files index in any order: t's links resolve as b and u arrive
	eventually(t, "t's relations indexed and resolved", func() bool {
		res, err := cur.Call(context.Background(), "graph.links", "kb", "tasks/t.md")
		if err != nil {
			return false
		}
		got = map[string]string{}
		for _, l := range res.([]any) {
			m := l.(map[string]any)
			got[m["kind"].(string)] = m["path"].(string) + "|" + str(m["external"])
		}
		return reflect.DeepEqual(got, want)
	})
	if ls := call(t, old, "graph.links", "kb", "tasks/t.md").([]any); len(ls) != 0 {
		t.Errorf("protocol 22's links of t: %v, want none (a review, a blocked and an external link)", ls)
	}
	if back := call(t, cur, "graph.backlinks", "kb", "b.md").([]any); len(back) != 1 {
		t.Errorf("protocol 23's backlinks of b: %v, want t's review", back)
	}
	if back := call(t, old, "graph.backlinks", "kb", "b.md").([]any); len(back) != 0 {
		t.Errorf("protocol 22's backlinks of b: %v, want none", back)
	}
	if un := call(t, cur, "graph.unresolved", "kb").([]any); len(un) != 0 {
		t.Errorf("an external link is listed unresolved: %v", un)
	}
	// asked for by name, a kind protocol 23 added is refused below it, in every graph verb
	for _, c := range []struct {
		verb   string
		params []any
	}{
		{"graph.links", []any{"kb", "tasks/t.md"}},
		{"graph.backlinks", []any{"kb", "b.md"}},
		{"graph.neighborhood", []any{"kb", "tasks/t.md", int64(1)}},
		{"graph.unresolved", []any{"kb"}},
	} {
		for _, kind := range []string{"review", "blocked"} {
			_, err := old.Call(context.Background(), c.verb, append(c.params, map[string]any{"kinds": []any{kind}})...)
			var re *golibrpc.Error
			if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.Contains(re.Message, "needs protocol 23") {
				t.Errorf("protocol 22's %s with kinds [%s]: %v, want invalid params naming protocol 23", c.verb, kind, err)
			}
		}
	}
	if ls := call(t, cur, "graph.links", "kb", "tasks/t.md", map[string]any{"kinds": []any{"review"}}).([]any); len(ls) != 1 {
		t.Errorf("protocol 23's review links of t: %v", ls)
	}
}

func str(v any) string { s, _ := v.(string); return s }

// TestGraphMap: graph.map answers the workspace's documents with their fields, or a root's
// neighbourhood, refuses a bad option, and is an unknown method below its protocol.
func TestGraphMap(t *testing.T) {
	r := relationRig(t)
	cur, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}
	m := call(t, cur, "graph.map", "kb").(map[string]any)
	nodes := []any{
		map[string]any{"path": "a.md", "title": "A", "type": "", "superseded": false},
		map[string]any{"path": "b.md", "title": "B", "type": "", "superseded": true},
		map[string]any{"path": "c.md", "title": "C", "type": "", "superseded": false, "tags": []any{"alpha", "zeta"}},
	}
	edges := []any{
		map[string]any{"src": "a.md", "dst": "b.md", "kind": "supersedes"},
		map[string]any{"src": "b.md", "dst": "c.md", "kind": "wikilink"},
	}
	if !reflect.DeepEqual(m["nodes"], nodes) || !reflect.DeepEqual(m["edges"], edges) {
		t.Errorf("the whole map: %v", m)
	}
	m = call(t, cur, "graph.map", "kb", map[string]any{"root": "c.md", "depth": int64(1), "kinds": []any{"wikilink"}}).(map[string]any)
	if !reflect.DeepEqual(m["nodes"], nodes[1:]) || !reflect.DeepEqual(m["edges"], edges[1:]) {
		t.Errorf("c.md's wikilink neighbourhood: %v; want b.md still superseded", m)
	}
	for _, opts := range []map[string]any{{"root": int64(1)}, {"root": ""}, {"depth": int64(3)}, {"depth": "1"}, {"kinds": "body"}} {
		_, err := cur.Call(context.Background(), "graph.map", "kb", opts)
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams {
			t.Errorf("options %v: %v; want invalid params", opts, err)
		}
	}
	if _, err := cur.Call(context.Background(), "graph.map", "kb", map[string]any{"root": "none.md"}); code(err) != CodeNotFound {
		t.Errorf("an unknown root: %v; want not found", err)
	}
	// graph.neighborhood still always has a root: an empty or unknown path is not found, as it was
	for _, p := range []string{"", "none.md"} {
		if res, err := cur.Call(context.Background(), "graph.neighborhood", "kb", p, int64(2)); code(err) != CodeNotFound {
			t.Errorf("graph.neighborhood of %q: %v, %v; want not found", p, res, err)
		}
	}
	// before protocol 28 the nodes carry no tags
	at27, _, err := helloAs(r, mapTagsSince-1, "a-27-client")
	if err != nil {
		t.Fatal(err)
	}
	m = call(t, at27, "graph.map", "kb").(map[string]any)
	if c := m["nodes"].([]any)[2].(map[string]any); c["path"] != "c.md" || c["tags"] != nil {
		t.Errorf("protocol %d: c.md is %v; want no tags", mapTagsSince-1, c)
	}
	old, _, err := helloAs(r, mapSince-1, "an-old-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Call(context.Background(), "graph.map", "kb"); code(err) != golibrpc.CodeMethodNotFound {
		t.Errorf("below protocol %d: %v; want method not found", mapSince, err)
	}
}
