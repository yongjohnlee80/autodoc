package rpc

import (
	"context"
	"net"
	"path/filepath"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// eventsServer serves no workspaces, with the store's preferences and event log.
func eventsServer(t *testing.T) (dial func(name string) (*golibrpc.Client, map[string]any), db *store.Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Fixed(), "v-test", WithListener(ln), WithPreferences(db), WithEvents(db))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = db.Close() })
	return func(name string) (*golibrpc.Client, map[string]any) {
		cli, err := golibrpc.Dial(context.Background(), sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cli.Close() })
		res, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": Protocol, "name": name})
		if err != nil {
			t.Fatal(err)
		}
		return cli, res.(map[string]any)
	}, db
}

func TestEventsReachPeerClients(t *testing.T) {
	dial, _ := eventsServer(t)
	a, helloA := dial("tui")
	b, helloB := dial("tui")
	tokA, tokB := helloA["client"].(string), helloB["client"].(string)
	if tokA == "" || tokA == tokB {
		t.Fatalf("tokens %q and %q: each session needs its own", tokA, tokB)
	}
	since := helloB["events"].(int64)
	call(t, a, "preference.set", "theme", "light")
	res := call(t, b, "sys.events", since, int64(10)).(map[string]any)
	evs := res["events"].([]any)
	if len(evs) != 1 || res["more"] != false {
		t.Fatalf("events = %v", res)
	}
	e := evs[0].(map[string]any)
	if e["kind"] != "preference.changed" || e["client"] != tokA || e["detail"] != "theme" || e["seq"] != res["cursor"] {
		t.Fatalf("event = %v (A is %s)", e, tokA)
	}
	// a refused verb logs nothing
	if _, err := a.Call(context.Background(), "preference.set", "", "x"); err == nil {
		t.Fatal("a nameless preference was set")
	}
	if res := call(t, b, "sys.events", res["cursor"], int64(10)).(map[string]any); len(res["events"].([]any)) != 0 {
		t.Fatalf("a refusal logged %v", res["events"])
	}
	// a client from another store's cursor, or too far behind, is told to take a snapshot
	if _, err := b.Call(context.Background(), "sys.events", int64(99), int64(10)); code(err) != CodeCursorExpired {
		t.Fatalf("a cursor past the head: %v", err)
	}
	for _, bad := range [][]any{{int64(0), int64(0)}, {int64(0), int64(501)}, {"x", int64(1)}} {
		if _, err := b.Call(context.Background(), "sys.events", bad...); code(err) != golibrpc.CodeInvalidParams {
			t.Errorf("sys.events %v: %v", bad, err)
		}
	}
}
