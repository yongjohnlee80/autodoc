//go:build server

package app

import (
	"context"
	"strings"
	"testing"
)

// The server's [log] level leaves out the records less severe than it, and --log-level is taken
// over the key. Observed is the Info line it logs once every step has started. With the key alone,
// the server is stopped by its lost lock: it waits for that only after the line, so its exit
// shows the line was reached and left out.
func TestTheLogLevelFiltersTheServersLog(t *testing.T) {
	const serving = "serving enterprise search"
	f := newServerFixture(t, serverView)
	f.extra = "\n[log]\nlevel = \"WARNING\"\n"
	f.configure("fake-model", f.url)

	ctx, cancel := context.WithCancel(f.ctx)
	done, stderr := f.serve(ctx, "--log-level", "info")
	f.started(stderr) // waits for the Info line, which the flag lets through
	f.waitFor("both rows indexed", func() bool { return f.documents() == "2" })
	cancel()
	if code := exitOf(t, done, stderr); code != 0 {
		t.Fatalf("--server --log-level info exited %d: %s", code, stderr)
	}

	done, stderr = f.serve(f.ctx)
	f.exec(`INSERT INTO track VALUES (3, 'A', 'osprey song', now() - interval '1 minute')`)
	f.waitFor("the lock taken and a row written under it", func() bool { return f.locks() == "1" && f.documents() == "3" })
	f.exec(`SELECT pg_terminate_backend(pid) ` + ownLocks)
	f.exec(`INSERT INTO track VALUES (4, 'A', 'heron song', now() - interval '1 minute')`)
	if code := exitOf(t, done, stderr); code != 1 || !strings.Contains(stderr.String(), "writer lock was lost") {
		t.Fatalf("--server after its lock was lost: exit %d: %s", code, stderr)
	}
	if strings.Contains(stderr.String(), serving) {
		t.Errorf("[log] level = \"WARNING\" logged an Info line: %s", stderr)
	}
}
