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
		"c.md": "# C\n",
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
