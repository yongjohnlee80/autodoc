package rpc

import (
	"context"
	"net"
	"reflect"
	"regexp"
	"strconv"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
)

var unixPeer = &net.UnixAddr{Name: "/tmp/s.sock", Net: "unix"}

// clientSession is a session after a hello as client.
func clientSession(client string) *golibrpc.Session {
	sess := &golibrpc.Session{}
	sess.SetValue(sessClient, client)
	return sess
}

// eventsBefore is what each verb logged before its contract said it: eventsOf's kind and the
// indexes of the params naming the workspace and the detail (-1 for none).
var eventsBefore = map[string]struct {
	kind              string
	workspace, detail int
}{
	"workspace.add":                 {"workspace.added", 0, -1},
	"workspace.rename":              {"workspace.renamed", 0, 1},
	"workspace.remove":              {"workspace.removed", 0, -1},
	"workspace.set_patterns":        {"workspace.patterns", 0, -1},
	"workspace.set_schema":          {"workspace.schema", 0, -1},
	"workspace.set_text_extensions": {"workspace.text_extensions", 0, -1},
	"workspace.section_size":        {"workspace.section_size", 0, -1},
	"workspace.embedding_policy":    {"workspace.embedding_policy", 0, 1},
	"workspace.set_provider":        {"workspace.provider", 0, 1},
	"preference.set":                {"preference.changed", -1, 0},
	"embedding.use":                 {"embedding.switched", -1, 0},
	"embedding.cancel_switch":       {"embedding.cancelled", -1, -1},
	"embedding.add":                 {"embedding.providers", -1, -1},
	"embedding.update":              {"embedding.providers", -1, 0},
	"embedding.remove":              {"embedding.providers", -1, 0},
	"ranker.use":                    {"ranker.switched", -1, 0},
	"ranker.window":                 {"ranker.switched", -1, 0},
	"ranker.add":                    {"ranker.providers", -1, -1},
	"ranker.update":                 {"ranker.providers", -1, 0},
	"ranker.remove":                 {"ranker.providers", -1, 0},
}

// paramsFor are params of the types c declares, each distinct: a string param i is "p<i>".
func paramsFor(c module.Contract) []any {
	out := make([]any, len(c.Params))
	for i, p := range c.Params {
		switch p.Type {
		case "string":
			out[i] = "p" + strconv.Itoa(i)
		case "int":
			out[i] = int64(i + 10)
		case "[]string":
			out[i] = []any{"x"}
		case "bytes":
			out[i] = []byte("x")
		default:
			out[i] = map[string]any{"k": "v"}
		}
	}
	return out
}

// TestEventsAreUnchanged: every verb that logged logs the same kind, workspace, detail and client
// token from its contract as it did from eventsOf; workspace.configure expands as before; no other
// verb logs.
func TestEventsAreUnchanged(t *testing.T) {
	ev := &recordedEvents{}
	s := New(Fixed(), "v", WithEvents(ev))
	ok := func(context.Context, *golibrpc.Request) (any, error) { return nil, nil }
	for name, v := range s.verbs {
		before, logged := eventsBefore[name]
		if name == "workspace.configure" {
			continue
		}
		if v.module().Logs() != logged {
			t.Errorf("%s: logs %v, logged %v", name, v.module().Logs(), logged)
			continue
		}
		if !logged {
			continue
		}
		params := paramsFor(v.Contract)
		want := store.Event{Kind: before.kind, Client: "test#1"}
		if before.workspace >= 0 {
			want.Workspace, _ = params[before.workspace].(string)
		}
		if before.detail >= 0 {
			want.Detail, _ = params[before.detail].(string)
		}
		n := len(ev.all())
		if _, err := s.logged(v.module(), ok)(context.Background(), &golibrpc.Request{Method: name, Params: params, Session: clientSession("test#1")}); err != nil {
			t.Fatal(err)
		}
		if got := ev.all()[n:]; !reflect.DeepEqual(got, []store.Event{want}) {
			t.Errorf("%s logged %+v, want %+v", name, got, want)
		}
	}
	cfg := s.verbs["workspace.configure"]
	n := len(ev.all())
	params := []any{"kb", map[string]any{"name": "docs", "schema": "s.yaml", "provider": "big", "view_args": map[string]any{}}}
	if _, err := s.logged(cfg.module(), ok)(context.Background(), &golibrpc.Request{Params: params, Session: clientSession("test#1")}); err != nil {
		t.Fatal(err)
	}
	want := []store.Event{{Kind: "workspace.renamed", Workspace: "kb", Detail: "docs", Client: "test#1"},
		{Kind: "workspace.schema", Workspace: "docs", Client: "test#1"},
		{Kind: "workspace.provider", Workspace: "docs", Detail: "big", Client: "test#1"},
		{Kind: "workspace.databases", Workspace: "docs", Client: "test#1"}}
	if got := ev.all()[n:]; !reflect.DeepEqual(got, want) {
		t.Errorf("workspace.configure logged %+v, want %+v", got, want)
	}
}

// TestEveryVerbThatLogsSaysSo: a verb whose events an expander makes still names an event in its
// contract, so the catalog shows that it logs.
func TestEveryVerbThatLogsSaysSo(t *testing.T) {
	for name, v := range New(Fixed(), "v").verbs {
		if v.module().Logs() && v.Event == nil {
			t.Errorf("%s logs and its contract names no event", name)
		}
	}
}

// TestArityIsTheContracts: the lo and hi each handler checks are its contract's required count and
// its params' (a call with too many params answers them).
func TestArityIsTheContracts(t *testing.T) {
	s := New(Fixed(), "v")
	want := regexp.MustCompile(`^want (\d+)(?: to (\d+))? parameters, got 64$`)
	for name, v := range s.verbs {
		req := &golibrpc.Request{Method: name, Params: make([]any, 64), Peer: unixPeer, Session: &golibrpc.Session{}}
		_, err := v.h(context.Background(), req)
		_, msg := wireError(err)
		lo, hi := -1, -1
		if m := want.FindStringSubmatch(msg); m != nil {
			lo, _ = strconv.Atoi(m[1])
			hi = lo
			if m[2] != "" {
				hi, _ = strconv.Atoi(m[2])
			}
		}
		if lo != v.Required() || hi != len(v.Params) {
			t.Errorf("%s checks %d to %d params (%q), its contract %d to %d", name, lo, hi, msg, v.Required(), len(v.Params))
		}
	}
}
