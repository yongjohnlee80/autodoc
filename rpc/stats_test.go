package rpc

import (
	"context"
	"os"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// TestSysStats: sys.stats answers the daemon's own process in every key, with the right kinds, its
// pid this one's and its CPU seconds never falling between two calls (ADR 1792256101 §4.2).
func TestSysStats(t *testing.T) {
	r := serve(t)
	cli := r.dial(true)
	first, ok := call(t, cli, "sys.stats").(map[string]any)
	if !ok {
		t.Fatalf("sys.stats: %T, want a map", call(t, cli, "sys.stats"))
	}
	ints := []string{"pid", "started_at", "rss", "peak_rss", "go_memory", "go_heap", "goroutines", "cpus"}
	for _, k := range ints {
		if _, ok := first[k].(int64); !ok {
			if _, ok := first[k].(uint64); !ok {
				t.Errorf("%s: %T %v, want an integer", k, first[k], first[k])
			}
		}
	}
	for _, k := range []string{"uptime", "cpu_seconds"} {
		if _, ok := first[k].(float64); !ok {
			t.Errorf("%s: %T %v, want a number", k, first[k], first[k])
		}
	}
	if first["version"] != "v-test" {
		t.Errorf("version %v, want the server's", first["version"])
	}
	if pid, _ := first["pid"].(int64); pid != int64(os.Getpid()) {
		t.Errorf("pid %v, want %d", first["pid"], os.Getpid())
	}
	second := call(t, cli, "sys.stats").(map[string]any)
	if second["cpu_seconds"].(float64) < first["cpu_seconds"].(float64) {
		t.Errorf("cpu_seconds fell: %v then %v", first["cpu_seconds"], second["cpu_seconds"])
	}
	if second["started_at"] != first["started_at"] {
		t.Errorf("started_at moved: %v then %v", first["started_at"], second["started_at"])
	}
}

// TestSysStatsNeedsProtocol25: a session at 24 is told sys.stats is an unknown method and its
// contracts do not list it; one at 25 lists and calls it.
func TestSysStatsNeedsProtocol25(t *testing.T) {
	r := serve(t)
	listed := func(cli *golibrpc.Client) bool {
		res, err := cli.Call(context.Background(), "sys.contracts", "sys.stats")
		if err != nil {
			t.Fatalf("sys.contracts: %v", err)
		}
		verbs, _ := res.(map[string]any)["verbs"].([]any)
		return len(verbs) == 1
	}
	old, _, err := helloAs(r, 24, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Call(context.Background(), "sys.stats"); code(err) != golibrpc.CodeMethodNotFound {
		t.Errorf("sys.stats at 24: %v, want an unknown method", err)
	}
	if listed(old) {
		t.Error("sys.contracts at 24 lists sys.stats")
	}
	cur, _, err := helloAs(r, 25, "a-client")
	if err != nil {
		t.Fatal(err)
	}
	if !listed(cur) {
		t.Error("sys.contracts at 25 does not list sys.stats")
	}
	if _, err := cur.Call(context.Background(), "sys.stats"); err != nil {
		t.Errorf("sys.stats at 25: %v", err)
	}
}
