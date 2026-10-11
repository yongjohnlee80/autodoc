package rpc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// fakeModule is a Module of the verbs and errors it is given.
type fakeModule struct {
	name  string
	verbs []module.Verb
	errs  []module.PublicError
}

func (m fakeModule) Name() string                 { return m.name }
func (m fakeModule) Verbs() []module.Verb         { return m.verbs }
func (m fakeModule) Errors() []module.PublicError { return m.errs }

var errFakeStale = errors.New("fake: stale at /secret/path")

type echoParams struct {
	Workspace string   `param:"workspace"`
	N         int      `param:"n"`
	Tags      []string `param:"tags,optional"`
}

type echoResult struct {
	Workspace string   `json:"workspace"`
	Root      string   `json:"root"`
	N         int      `json:"n"`
	Tags      []string `json:"tags,omitempty"`
	Protocol  int64    `json:"protocol"`
	Client    string   `json:"client"`
}

// echoModule is a module named todos (whose code range exists) with a workspace verb that answers
// what its context and params held, logs, and fails as its n says; and a local-peer verb.
func echoModule() fakeModule {
	echo := module.Handle(module.Contract{Verb: "todos.echo", Since: 12, Scope: module.ScopeWorkspace,
		Result: "{workspace, root, n, tags, protocol, client}", Errors: []int64{-32071},
		Event: &module.EventSpec{Kind: "todos.changed", Workspace: "workspace"}},
		func(ctx context.Context, p echoParams) (echoResult, error) {
			switch {
			case p.N < 0:
				return echoResult{}, fmt.Errorf("echo: %w", errFakeStale)
			case p.N == 99:
				return echoResult{}, errors.New("disk full at /secret/path")
			}
			w, _ := module.WorkspaceFrom(ctx)
			return echoResult{Workspace: w.Name, Root: w.Root, N: p.N, Tags: p.Tags, Protocol: module.ProtocolFrom(ctx),
				Client: module.ClientFrom(ctx)}, nil
		})
	// its events come from its result alone
	echo.Events = func(_ context.Context, _ []any, result any) []module.Event {
		r := result.(echoResult)
		return []module.Event{{Kind: "todos.changed", Workspace: r.Workspace, Detail: fmt.Sprint(r.N)}}
	}
	local := module.Handle(module.Contract{Verb: "todos.here", Since: 12, Scope: module.ScopeLocalPeer},
		func(context.Context, struct{}) (bool, error) { return true, nil })
	bad := module.Verb{Contract: module.Contract{Verb: "todos.bad", Since: 12},
		Call: func(context.Context, []any) (any, error) { return make(chan int), nil }}
	return fakeModule{name: "todos", verbs: []module.Verb{echo, local, bad},
		errs: []module.PublicError{{Err: errFakeStale, Code: -32071, Name: "todos.stale", Message: "the task changed", Next: "todos.get"}}}
}

// moduleServer serves workspace kb and the modules, with an event log, on a unix socket.
func moduleServer(t *testing.T, ms ...module.Module) (*golibrpc.Client, *recordedEvents) {
	t.Helper()
	ev := &recordedEvents{}
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(Fixed(&Workspace{Name: "kb", Root: "/roots/kb"}), "v-test", WithListener(ln), WithEvents(ev), WithModules(ms...))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	r := &rig{t: t, sock: sock}
	return r.dial(true), ev
}

// TestAModuleVerbOverTheSocket: a module's verb is served from its contract: its params decoded,
// its workspace resolved, the session's protocol and token in its context, its result carried as
// the wire does, its events logged, and its errors mapped by its module first.
func TestAModuleVerbOverTheSocket(t *testing.T) {
	cli, ev := moduleServer(t, echoModule())
	ctx := context.Background()
	got := call(t, cli, "todos.echo", "kb", int64(3), []any{"a", "b"})
	want := map[string]any{"workspace": "kb", "root": "/roots/kb", "n": int64(3), "tags": []any{"a", "b"}, "protocol": Protocol}
	m := got.(map[string]any)
	client, _ := m["client"].(string)
	delete(m, "client")
	if !reflect.DeepEqual(m, want) || !strings.HasPrefix(client, "test#") {
		t.Errorf("todos.echo = %v (client %q), want %v", m, client, want)
	}
	if evs := ev.all(); !reflect.DeepEqual(evs, []store.Event{{Kind: "todos.changed", Workspace: "kb", Detail: "3", Client: client}}) {
		t.Errorf("events %+v", evs)
	}
	for _, c := range []struct {
		name   string
		params []any
		code   int64
		msg    string
	}{
		{"its own error", []any{"kb", int64(-1)}, -32071, "the task changed"},
		{"a param of the wrong type", []any{"kb", "three"}, golibrpc.CodeInvalidParams, "todos.echo: n must be int"},
		{"a list of the wrong type", []any{"kb", int64(1), "a"}, golibrpc.CodeInvalidParams, "todos.echo: tags must be []string"},
		{"too few params", []any{"kb"}, golibrpc.CodeInvalidParams, "want 2 to 3 parameters, got 1"},
		{"too many params", []any{"kb", int64(1), []any{}, 4}, golibrpc.CodeInvalidParams, "want 2 to 3 parameters, got 4"},
		{"no such workspace", []any{"nope", int64(1)}, CodeNoSuchWorkspace, "no such workspace"},
		{"an unmapped error", []any{"kb", int64(99)}, golibrpc.CodeInternal, ""},
	} {
		_, err := cli.Call(ctx, "todos.echo", c.params...)
		code, msg := wireError(err)
		if code != c.code || (c.msg != "" && msg != c.msg) || strings.Contains(msg, "/secret") {
			t.Errorf("%s: %d %q, want %d %q", c.name, code, msg, c.code, c.msg)
		}
	}
	if n := len(ev.all()); n != 1 {
		t.Errorf("the refusals logged %d events", n-1)
	}
	if got := call(t, cli, "todos.here"); got != true {
		t.Errorf("todos.here on the unix socket = %v", got)
	}
	if _, err := cli.Call(ctx, "todos.bad"); code(err) != golibrpc.CodeInternal {
		t.Errorf("a result the wire cannot carry: %v, want an internal error", err)
	}
}

// TestAModuleLocalVerbOverTCP: a ScopeLocalPeer verb refuses a peer that is not on the unix socket.
func TestAModuleLocalVerbOverTCP(t *testing.T) {
	s := New(Fixed(), "v", WithModules(echoModule()))
	_, err := s.verbs["todos.here"].h(context.Background(), &golibrpc.Request{Method: "todos.here",
		Peer: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, Session: &golibrpc.Session{}})
	if c, msg := wireError(err); c != golibrpc.CodeAccessDenied || msg != "todos.here is for local peers" {
		t.Errorf("over TCP: %d %q", c, msg)
	}
}

// TestModuleRegistrationRefuses: a verb registered twice (by two modules, or over one of rpc's), a
// code two modules answer, a code outside the module's range, and a contract that does not check
// panic at start.
func TestModuleRegistrationRefuses(t *testing.T) {
	plain := func(name string) module.Verb {
		return module.Verb{Contract: module.Contract{Verb: name, Since: 12}, Call: func(context.Context, []any) (any, error) { return nil, nil }}
	}
	stale := module.PublicError{Err: errFakeStale, Code: -32071, Name: "todos.stale", Message: "stale"}
	for _, c := range []struct {
		name string
		ms   []module.Module
		want string
	}{
		{"one verb in two modules", []module.Module{fakeModule{name: "todos", verbs: []module.Verb{plain("todos.a")}},
			fakeModule{name: "todos", verbs: []module.Verb{plain("todos.a")}}}, "duplicate method registration: todos.a"},
		{"one of rpc's verbs", []module.Module{fakeModule{name: "todos", verbs: []module.Verb{plain("sys.hello")}}}, "duplicate method registration: sys.hello"},
		{"one code in two modules", []module.Module{fakeModule{name: "todos", errs: []module.PublicError{stale}},
			fakeModule{name: "todos", errs: []module.PublicError{stale}}}, "registered by two modules"},
		{"a code outside its range", []module.Module{fakeModule{name: "todos",
			errs: []module.PublicError{{Err: errFakeStale, Code: -32050, Name: "todos.x"}}}}, "outside"},
		{"a module with no range", []module.Module{fakeModule{name: "fake", errs: []module.PublicError{stale}}}, "no code range"},
		{"an event naming no param", []module.Module{fakeModule{name: "todos", verbs: []module.Verb{{
			Contract: module.Contract{Verb: "todos.b", Event: &module.EventSpec{Kind: "k", Workspace: "workspace"}}}}}}, "does not have"},
	} {
		func() {
			defer func() {
				r := recover()
				if r == nil || !strings.Contains(fmt.Sprint(r), c.want) {
					t.Errorf("%s: %v, want a panic saying %q", c.name, r, c.want)
				}
			}()
			New(Fixed(), "v", WithModules(c.ms...))
		}()
	}
	// one module answering a code from several errors is its own business
	twice := fakeModule{name: "todos", errs: []module.PublicError{stale, {Err: errFixed, Code: -32071, Name: "todos.stale", Message: "fixed"}}}
	New(Fixed(), "v", WithModules(twice))
}
