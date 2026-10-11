package rpc

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
)

var update = flag.Bool("update", false, "rewrite testdata/contracts.json from the contracts")

// TestContractsAreGolden: every verb's contract, every code and the protocol range are pinned
// together. Changing any of them is a reviewed diff of testdata/contracts.json (go test ./rpc
// -run TestContractsAreGolden -update), and a protocol bump when it changes what a client sees.
func TestContractsAreGolden(t *testing.T) {
	got, err := json.MarshalIndent(New(Fixed(), "v").Catalog(Protocol, ""), "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	got = append(got, '\n')
	golden := filepath.Join("testdata", "contracts.json")
	if *update {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("the contracts differ from %s: review the change, bump Protocol if a client sees it, and rewrite it with -update", golden)
	}
	if Protocol != 20 || MinProtocol != 12 {
		t.Errorf("protocol %d to %d: the golden file is protocol 20's, served from 12", MinProtocol, Protocol)
	}
}

// TestTheGateFollowsSince: for every verb, a session at the protocol before its Since is told it
// is an unknown method, naming that protocol, and a session at its Since (or MinProtocol) is let
// through. Too many params make each call a harmless refusal past the gate.
func TestTheGateFollowsSince(t *testing.T) {
	r := serve(t)
	sessions := map[int64]*golibrpc.Client{}
	at := func(p int64) *golibrpc.Client {
		if sessions[p] == nil {
			cli, _, err := helloAs(r, p, "a-client")
			if err != nil {
				t.Fatalf("a protocol-%d hello: %v", p, err)
			}
			sessions[p] = cli
		}
		return sessions[p]
	}
	many := make([]any, 64)
	for _, v := range r.srv.Catalog(Protocol, "").Verbs {
		if v.Verb == "sys.hello" {
			continue
		}
		if v.Since > MinProtocol {
			_, err := at(v.Since-1).Call(context.Background(), v.Verb, many...)
			if c, msg := wireError(err); c != golibrpc.CodeMethodNotFound || !strings.Contains(msg, fmt.Sprintf("needs protocol %d", v.Since)) {
				t.Errorf("%s at %d: %d %q, want an unknown method", v.Verb, v.Since-1, c, msg)
			}
		}
		_, err := at(max(v.Since, MinProtocol)).Call(context.Background(), v.Verb, many...)
		if c, _ := wireError(err); c == golibrpc.CodeMethodNotFound || c == CodeHandshakeRequired || c == 0 {
			t.Errorf("%s at %d: %v, want it admitted (and refused its params)", v.Verb, max(v.Since, MinProtocol), err)
		}
	}
}

// TestCatalogFilters: a protocol lists the verbs it knew; a filter names all, a domain or a verb, and
// lists the codes they name.
func TestCatalogFilters(t *testing.T) {
	s := New(Fixed(), "v")
	names := func(c Catalog) []string {
		var out []string
		for _, v := range c.Verbs {
			out = append(out, v.Verb)
		}
		return out
	}
	all := s.Catalog(Protocol, "")
	if len(all.Verbs) != len(s.Verbs()) || !reflect.DeepEqual(s.Catalog(Protocol, "*"), all) {
		t.Errorf("the whole catalog lists %d verbs of %d", len(all.Verbs), len(s.Verbs()))
	}
	if got := names(s.Catalog(13, "")); slices.Contains(got, "graph.resolve") || !slices.Contains(got, "index.documents") {
		t.Errorf("at 13: %v", got)
	}
	if got := names(s.Catalog(Protocol, "graph.*")); !reflect.DeepEqual(got, []string{"graph.backlinks", "graph.links", "graph.neighborhood", "graph.resolve", "graph.unresolved"}) {
		t.Errorf("graph.*: %v", got)
	}
	one := s.Catalog(Protocol, "doc.read")
	var codes []int64
	for _, e := range one.Errors {
		codes = append(codes, e.Code)
	}
	if !reflect.DeepEqual(names(one), []string{"doc.read"}) || !reflect.DeepEqual(codes, []int64{CodeNoSuchWorkspace, CodeNotFound, CodeUnsupported, golibrpc.CodeInvalidParams}) {
		t.Errorf("doc.read: %v with codes %v", names(one), codes)
	}
	if none := s.Catalog(Protocol, "todos.*"); len(none.Verbs) != 0 || len(none.Errors) != 0 {
		t.Errorf("todos.* with no todos module: %+v", none)
	}
}

// TestEveryCodeIsDocumented: every code a contract names or publicErrs answers has a name and a
// next step, and the catalog carries every message publicErrs maps to its code.
func TestEveryCodeIsDocumented(t *testing.T) {
	c := New(Fixed(), "v").Catalog(Protocol, "")
	byCode := map[int64]CatalogError{}
	for _, e := range c.Errors {
		if e.Name == "" || e.Next == "" {
			t.Errorf("code %d has no name or no next step: %+v", e.Code, e)
		}
		byCode[e.Code] = e
	}
	for _, v := range c.Verbs {
		for _, code := range v.Errors {
			if _, ok := byCode[code]; !ok {
				t.Errorf("%s names code %d, which the catalog lacks", v.Verb, code)
			}
		}
	}
	for _, pe := range publicErrs {
		if !slices.Contains(byCode[pe.Code].Messages, pe.Message) {
			t.Errorf("code %d does not list %q", pe.Code, pe.Message)
		}
	}
}

// TestTheCatalogJSONIsTheContract: the catalog's JSON carries every field a contract's does, with
// its scope and audience by name, and a module's errors by their name, never their Go error.
func TestTheCatalogJSONIsTheContract(t *testing.T) {
	ct, jt := reflect.TypeFor[module.Contract](), reflect.TypeFor[contractJSON]()
	tags := map[string]bool{}
	for i := range jt.NumField() {
		tags[jt.Field(i).Tag.Get("json")] = true
	}
	for i := range ct.NumField() {
		if tag := ct.Field(i).Tag.Get("json"); tag != "-" && !tags[tag] {
			t.Errorf("Contract's %s (json %q) is not in the catalog", ct.Field(i).Name, tag)
		}
	}
	c := New(Fixed(), "v", WithModules(echoModule())).Catalog(Protocol, "todos.*")
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"scope":"workspace"`, `"audience":"agent"`, `"scope":"local-peer"`,
		`{"code":-32071,"name":"todos.stale","next":"todos.get","messages":["the task changed"]}`} {
		if !strings.Contains(string(b), want) {
			t.Errorf("the catalog's JSON lacks %s: %s", want, b)
		}
	}
	if strings.Contains(string(b), "/secret") {
		t.Errorf("the catalog carries a Go error: %s", b)
	}
}

// TestSysContracts: sys.contracts is refused below protocol 20, and answers the catalog at the
// session's protocol, as Catalog builds it.
func TestSysContracts(t *testing.T) {
	r := serve(t)
	old, _, err := helloAs(r, 19, "an-older-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Call(context.Background(), "sys.contracts"); code(err) != golibrpc.CodeMethodNotFound {
		t.Errorf("sys.contracts at 19: %v, want an unknown method", err)
	}
	cli := r.dial(true)
	for _, filter := range []string{"", "graph.*"} {
		got := call(t, cli, "sys.contracts", filter)
		if !sameJSON(t, got, r.srv.Catalog(Protocol, filter)) {
			t.Errorf("sys.contracts(%q) = %v, want the catalog", filter, got)
		}
	}
	if _, err := cli.Call(context.Background(), "sys.contracts", int64(1)); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("a filter that is not a string: %v", err)
	}
}

// sameJSON reports whether a and b read the same as JSON.
func sameJSON(t *testing.T, a, b any) bool {
	t.Helper()
	norm := func(v any) any {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var out any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	return reflect.DeepEqual(norm(a), norm(b))
}

// TestCatalogMarkdown: the tables name each domain once, mark the verbs for people's tools and a
// local peer's, show the events a verb logs and escape a pipe.
func TestCatalogMarkdown(t *testing.T) {
	md := New(Fixed(), "v").Catalog(Protocol, "").Markdown()
	for _, want := range []string{
		"Verbs at protocol 20; the daemon serves sessions from protocol 12.",
		"\n### `graph`\n\n| verb | params | result | since | audience |\n",
		"| `workspace.rename` | `name` string, `to` string | Logs `workspace.renamed`. Codes: -32060, -32062, -32065. | 2 | for people's tools |",
		"| `file.locate` | `path` string (absolute and clean) |",
		"| agent; unix socket only |",
		"`workspace.renamed \\| workspace.patterns",
		"| -32060 | `rpc.no_such_workspace` | call workspace.list and use a listed name | \"no such workspace\" |",
		"| `sys.shutdown` | — | Stops the daemon. Frozen across protocols. Codes: -32001. | 1 | internal; unix socket only |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("the tables lack %q", want)
		}
	}
	if n := strings.Count(md, "### `graph`"); n != 1 {
		t.Errorf("graph has %d tables", n)
	}
}
