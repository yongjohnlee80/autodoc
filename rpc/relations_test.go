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
