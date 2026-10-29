package rpc

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/internal/profiling"
)

// profileDir is a directory short enough for a monitor's socket (t.TempDir's names a long test).
func profileDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "adp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return filepath.Join(dir, "p")
}

// profileOf is sys.profile's answer, on and dir.
func profileOf(t *testing.T, cli *golibrpc.Client, op, dir string) (bool, string) {
	t.Helper()
	res, err := cli.Call(context.Background(), "sys.profile", op, dir)
	if err != nil {
		t.Fatalf("sys.profile %s: %v", op, err)
	}
	m, _ := res.(map[string]any)
	on, _ := m["on"].(bool)
	d, _ := m["dir"].(string)
	return on, d
}

// TestSysProfileStartsReportsAndStops: the daemon's monitor starts in the directory asked, reports
// itself, keeps itself through a second start (one per process), stops with its samples written,
// and starts again; a stop with none running is no error.
func TestSysProfileStartsReportsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	prof := profiling.New(ctx, "serve")
	t.Cleanup(func() { _ = prof.Close() })
	r := serveWith(t, WithProfiler(prof))
	cli, _, err := helloAs(r, Protocol, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	if on, dir := profileOf(t, cli, "status", ""); on || dir != "" {
		t.Fatalf("before a start: on %v, dir %q", on, dir)
	}
	want := profileDir(t)
	if on, dir := profileOf(t, cli, "start", want); !on || dir != want {
		t.Fatalf("start: on %v, dir %q; want %s", on, dir, want)
	}
	if on, dir := profileOf(t, cli, "start", profileDir(t)); !on || dir != want {
		t.Errorf("a second start: on %v, dir %q; want the running one, %s", on, dir, want)
	}
	if on, dir := profileOf(t, cli, "status", ""); !on || dir != want || prof.Dir() != want {
		t.Errorf("status: on %v, dir %q, profiler %q", on, dir, prof.Dir())
	}
	if on, dir := profileOf(t, cli, "stop", ""); on || dir != "" {
		t.Errorf("stop: on %v, dir %q", on, dir)
	}
	if _, err := os.Stat(filepath.Join(want, "samples.jsonl")); err != nil {
		t.Errorf("no samples after the stop: %v", err)
	}
	if on, _ := profileOf(t, cli, "stop", ""); on {
		t.Error("a stop with none running turned it on")
	}
	again := profileDir(t)
	if on, dir := profileOf(t, cli, "start", again); !on || dir != again {
		t.Errorf("a start after a stop: on %v, dir %q; want %s", on, dir, again)
	}
}

// TestSysProfileRefuses: a server given no profiler answers unsupported, an op it does not know
// is an invalid parameter, and a directory another monitor owns refuses the start without saying
// the path.
func TestSysProfileRefuses(t *testing.T) {
	cli, _, err := helloAs(serve(t), Protocol, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Call(context.Background(), "sys.profile", "status", ""); code(err) != CodeUnsupported {
		t.Errorf("no profiler: %v, want unsupported", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	prof := profiling.New(ctx, "serve")
	t.Cleanup(func() { _ = prof.Close() })
	cli, _, err = helloAs(serveWith(t, WithProfiler(prof)), Protocol, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cli.Call(context.Background(), "sys.profile", "restart", ""); code(err) != golibrpc.CodeInvalidParams {
		t.Errorf("an unknown op: %v, want invalid params", err)
	}
	taken := profileDir(t)
	owner := profiling.New(ctx, "serve")
	if _, err := owner.Start(taken); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	_, err = cli.Call(context.Background(), "sys.profile", "start", taken)
	if c, msg := wireError(err); c != golibrpc.CodeInvalidParams || msg == "" || filepath.IsAbs(msg) {
		t.Errorf("a directory another monitor owns: %d %q", c, msg)
	}
	if prof.Dir() != "" {
		t.Errorf("the refused start left a monitor: %s", prof.Dir())
	}
}

// TestSysProfileIsForLocalPeersAlone: a peer that is not on the unix socket is refused, and the
// contract says why a script never sees it: local-peer scope, the protocol's own audience.
func TestSysProfileIsForLocalPeersAlone(t *testing.T) {
	s := New(Fixed(), "v", WithProfiler(profiling.New(context.Background(), "serve")))
	_, err := s.verbs["sys.profile"].h(context.Background(), &golibrpc.Request{Method: "sys.profile", Params: []any{"status", ""},
		Peer: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, Session: &golibrpc.Session{}})
	if c, msg := wireError(err); c != golibrpc.CodeAccessDenied || msg != "sys.profile is for local peers" {
		t.Errorf("over TCP: %d %q", c, msg)
	}
	cli, _, err := helloAs(serve(t), Protocol, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	res, err := cli.Call(context.Background(), "sys.contracts", "sys.profile")
	if err != nil {
		t.Fatal(err)
	}
	verbs, _ := res.(map[string]any)["verbs"].([]any)
	if len(verbs) != 1 {
		t.Fatalf("sys.contracts lists %d sys.profile", len(verbs))
	}
	v, _ := verbs[0].(map[string]any)
	if v["scope"] != "local-peer" || v["audience"] != "internal" {
		t.Errorf("sys.profile is %v, %v; want local-peer and internal (never bound to Lua)", v["scope"], v["audience"])
	}
}

// TestSysProfileNeedsProtocol32: a session at 31 is told sys.profile is an unknown method.
func TestSysProfileNeedsProtocol32(t *testing.T) {
	old, _, err := helloAs(serveWith(t, WithProfiler(profiling.New(context.Background(), "serve"))), 31, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Call(context.Background(), "sys.profile", "status", ""); code(err) != golibrpc.CodeMethodNotFound {
		t.Errorf("sys.profile at 31: %v, want an unknown method", err)
	}
}
