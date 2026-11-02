package rpc

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/golib/logger"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// callerSecret is the one credential callerModule verifies: its text must appear nowhere the
// daemon writes or answers.
const callerSecret = "sess-1.the-secret-text-of-the-credential"

// callerModule is a module named todos that verifies callerSecret for the store store-1, as crew
// does, and whose todos.who answers the caller its verb's context carries.
type callerModule struct{ fakeModule }

func (m callerModule) Caller(_ context.Context, credential, store string) (string, bool) {
	if credential == callerSecret && store == "store-1" {
		return "gold@autodoc:aaaa", true
	}
	return "", false
}

func newCallerModule() callerModule {
	who := module.Handle(module.Contract{Verb: "todos.who", Since: 12, Result: "caller"},
		func(ctx context.Context, _ struct{}) (string, error) { return module.CallerFrom(ctx), nil })
	refused := module.Handle(module.Contract{Verb: "todos.refused", Since: 12, Errors: []int64{-32071}},
		func(ctx context.Context, _ struct{}) (string, error) {
			return "", fmt.Errorf("echo: %w", errFakeStale)
		})
	return callerModule{fakeModule: fakeModule{name: "todos", verbs: []module.Verb{who, refused},
		errs: []module.PublicError{{Err: errFakeStale, Code: -32071, Name: "todos.stale", Message: "the task changed", Next: "todos.get"}}}}
}

// syncBuffer is a log's writer, read once the server stops.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *syncBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}

func (w *syncBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// A hello that names a crew session proves who the caller is
// (docs/decisions/todos.md#an-assignment-says-who-made-it): the module that knows the crew verifies
// the credential once, at the hello, and every verb of the session sees the caller as
// codename@scope. A credential that doesn't verify, or a context the CLI couldn't read (a crew key
// with no credential), is an unverified agent, never the user. No crew key (an older client, a
// window, Neovim, a shell with no crew context) is the user's surface, as before, and a hello again
// without one is that too. The credential's text is in no answer, error, token or log line.
func TestAHelloProvesItsCrewCaller(t *testing.T) {
	mod := newCallerModule()
	var log syncBuffer
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := New(Fixed(), "v-test", WithListener(ln), WithModules(mod),
		WithLogger(logger.New(logger.WithWriter(&log), logger.WithMinLevel(logger.SeverityDebug))))
	done := make(chan struct{})
	go func() { _ = srv.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	var seen []string // every answer and error the client got
	hello := func(proto int64, crew any) string {
		t.Helper()
		cli, err := golibrpc.Dial(context.Background(), sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cli.Close() })
		info := map[string]any{"protocol": proto, "name": CLIName}
		if crew != nil {
			info["crew"] = crew
		}
		reply, err := cli.Call(context.Background(), "sys.hello", info)
		if err != nil {
			t.Fatalf("hello at %d with %v: %v", proto, crew, err)
		}
		seen = append(seen, fmt.Sprint(reply))
		who, err := cli.Call(context.Background(), "todos.who")
		if err != nil {
			t.Fatal(err)
		}
		_, rerr := cli.Call(context.Background(), "todos.refused")
		seen = append(seen, fmt.Sprint(who), fmt.Sprint(rerr))
		got, _ := who.(string)
		return got
	}
	for _, c := range []struct {
		name  string
		proto int64
		crew  any
		want  string
	}{
		{"a verified crew session", Protocol, map[string]any{"credential": callerSecret, "store": "store-1"}, "gold@autodoc:aaaa"},
		{"a credential that doesn't verify", Protocol, map[string]any{"credential": "sess-1.wrong", "store": "store-1"}, module.CallerUnverified},
		{"another store's context", Protocol, map[string]any{"credential": callerSecret, "store": "store-2"}, module.CallerUnverified},
		{"a context the CLI couldn't read", Protocol, map[string]any{}, module.CallerUnverified},
		{"a crew key of the wrong shape", Protocol, "x", module.CallerUnverified},
		{"no crew key", Protocol, nil, ""},
		{"an older client", Protocol - 1, nil, ""},
	} {
		if got := hello(c.proto, c.crew); got != c.want {
			t.Errorf("%s: the caller is %q, want %q", c.name, got, c.want)
		}
	}

	// a hello again on the same connection, without the crew key, is the user's surface again
	cli, err := golibrpc.Dial(context.Background(), sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cli.Close() }()
	for i, info := range []map[string]any{
		{"protocol": Protocol, "name": CLIName, "crew": map[string]any{"credential": callerSecret, "store": "store-1"}},
		{"protocol": Protocol, "name": CLIName},
	} {
		if _, err := cli.Call(context.Background(), "sys.hello", info); err != nil {
			t.Fatal(err)
		}
		who, _ := cli.Call(context.Background(), "todos.who")
		if want := []string{"gold@autodoc:aaaa", ""}[i]; who != want {
			t.Errorf("hello %d on one connection: the caller is %v, want %q", i+1, who, want)
		}
	}

	cancel()
	<-done
	for _, s := range append(seen, log.String()) {
		if strings.Contains(s, "the-secret-text") {
			t.Errorf("the credential's text is out: %q", s)
		}
	}
}

// TestACallWithNoSessionIsTheUsers: a verb dispatched with no session at all (an in-process call,
// never a hello) carries no caller: it is the user's surface, as a hello without the crew key is
// (the mutant: the session's caller read without checking there is one).
func TestACallWithNoSessionIsTheUsers(t *testing.T) {
	s := New(Fixed(), "v", WithModules(newCallerModule()))
	out, err := s.verbs["todos.who"].h(context.Background(), &golibrpc.Request{Method: "todos.who"})
	if err != nil || out != "" {
		t.Fatalf("todos.who with no session: %v, %v; want the user's surface (no caller)", out, err)
	}
}
