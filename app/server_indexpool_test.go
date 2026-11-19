//go:build server

package app

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// An index pool under two connections is refused, exit 2, at the start and by --check alike, naming
// the field whose variable holds the DSN and what to set, before anything is opened: an index build
// holds two connections at once, so with one the first build would wait for good and the server
// with it. Two is enough. The mutant, named here: the refusal left out (the start goes on, and
// --check passes a pool of one).
func TestAnIndexPoolUnderTwoConnectionsIsRefused(t *testing.T) {
	f := newServerFixture(t, serverView)
	dsn := os.Getenv("AUTODOC_TEST_SERVER_DSN")
	run2 := func(args ...string) (int, string) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		code := run(f.ctx, append([]string{"--server"}, append(args, f.path)...), testOptions, &stdout, &stderr)
		return code, stderr.String()
	}
	refused := func(what, field string, args ...string) {
		t.Helper()
		code, stderr := run2(args...)
		if code != 2 || !strings.Contains(stderr, field+": its DSN sets pool_max_conns=1: the index's pool needs at least 2") {
			t.Errorf("%s: exit %d, %s; want 2, refusing %s's pool of one", what, code, stderr, field)
		}
	}

	f.storePool(1)
	refused("--check, the index in the views' database", "postgres.dsn_env", "--check")
	refused("the start, the index in the views' database", "postgres.dsn_env")
	if got := f.scalar(`SELECT (to_regnamespace('` + f.schema + `') IS NULL)::text`); got != "true" {
		t.Errorf("a start refused for its pool made schema %s", f.schema)
	}

	// the index in a database of its own: its DSN's pool is the one refused, and the views' may be
	// one connection, which reads alone
	f.pg = `index_dsn_env = "AUTODOC_TEST_INDEX_DSN"`
	f.configure("fake-model", f.url)
	t.Setenv("AUTODOC_TEST_INDEX_DSN", withPool(t, dsn, 1))
	t.Setenv("AUTODOC_TEST_SERVER_DSN", dsn)
	refused("--check, the index in a database of its own", "postgres.index_dsn_env", "--check")
	refused("the start, the index in a database of its own", "postgres.index_dsn_env")

	t.Setenv("AUTODOC_TEST_INDEX_DSN", withPool(t, dsn, minIndexConns))
	t.Setenv("AUTODOC_TEST_SERVER_DSN", withPool(t, dsn, 1))
	if code, stderr := run2("--check"); code != 0 {
		t.Errorf("--check of an index pool of %d, the views' of 1: exit %d, %s; want 0", minIndexConns, code, stderr)
	}
}
