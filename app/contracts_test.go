package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"reflect"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/rpc"
)

// contractsOut runs autodoc with args, with a config that does not exist and no daemon anywhere.
func contractsOut(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), append([]string{"--config", "/nonexistent/autodoc.toml"}, args...), testOptions, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// asJSON is v read back from JSON, so a msgpack answer and a printed one compare.
func asJSON(t *testing.T, v any) any {
	t.Helper()
	b, ok := v.([]byte)
	if !ok {
		var err error
		if b, err = json.Marshal(v); err != nil {
			t.Fatal(err)
		}
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("%v: %s", err, b)
	}
	return out
}

// TestContractsWithNoDaemon: --contracts needs no config and no socket, and prints what a live
// daemon's sys.contracts answers at the same protocol.
func TestContractsWithNoDaemon(t *testing.T) {
	code, out, errOut := contractsOut(t, "--contracts")
	if code != 0 || errOut != "" {
		t.Fatalf("--contracts: exit %d, %q", code, errOut)
	}
	sock := short(t) + "/c.sock"
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := rpc.New(rpc.Fixed(), "v", rpc.WithListener(ln), rpc.WithModules(modules(nil)...))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	defer func() { cancel(); <-done }()
	cli, err := golibrpc.Dial(ctx, sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
	if err != nil {
		t.Fatal(err)
	}
	defer cli.Close()
	if _, err := cli.Call(ctx, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": "test"}); err != nil {
		t.Fatal(err)
	}
	live, err := cli.Call(ctx, "sys.contracts")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(asJSON(t, []byte(out)), asJSON(t, live)) {
		t.Errorf("--contracts printed\n%s\nsys.contracts answered %v", out, live)
	}
}

// TestContractsFiltersAndFormats: a filter narrows the catalog (todos.* is nothing without the
// todos module), md prints AGENTS.md's tables, flags may follow the filter, and --format is
// --contracts' alone.
func TestContractsFiltersAndFormats(t *testing.T) {
	code, out, _ := contractsOut(t, "--contracts", "todos.*")
	if want := map[string]any{"protocol": float64(rpc.Protocol), "min_protocol": float64(rpc.MinProtocol), "verbs": []any{}, "errors": []any{}}; code != 0 || !reflect.DeepEqual(asJSON(t, []byte(out)), want) {
		t.Errorf("--contracts todos.*: exit %d, %s", code, out)
	}
	code, out, _ = contractsOut(t, "--contracts", "--format", "md")
	if code != 0 || out != Contracts("").Markdown() || !strings.Contains(out, "### `graph`") {
		t.Errorf("--contracts --format md: exit %d, %q", code, out)
	}
	code, out, _ = contractsOut(t, "--contracts", "graph.*", "--format", "md")
	if code != 0 || out != Contracts("graph.*").Markdown() || strings.Contains(out, "`doc.read`") {
		t.Errorf("--contracts graph.* --format md: exit %d, %q", code, out)
	}
	for _, c := range []struct {
		args []string
		says string
	}{
		{[]string{"--format", "md", "--version"}, "--format is for --contracts"},
		{[]string{"--contracts", "--format", "xml"}, `--format "xml": json or md`},
		{[]string{"--contracts", "graph.*", "doc.*"}, "unexpected arguments: [doc.*]"},
		{[]string{"--contracts", "graph.*", "--no-such-flag"}, "flag provided but not defined"},
	} {
		if code, _, errOut := contractsOut(t, c.args...); code != 2 || !strings.Contains(errOut, c.says) {
			t.Errorf("%v: exit %d, %q; want 2 saying %q", c.args, code, errOut, c.says)
		}
	}
}
