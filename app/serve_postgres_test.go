//go:build server

package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
	"github.com/yongjohnlee80/autodoc/internal/ipc"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// A daemon whose [store] is on PostgreSQL serves it: it says so in its hello (engine, schema and
// the store's own id), writes there, and a client whose config names another schema of the same
// database refuses the daemon on its socket, naming both stores. The mutant, named here: the
// daemon not saying its engine and schema (every client of a store on PostgreSQL refuses it).
func TestADaemonServesAStoreOnPostgres(t *testing.T) {
	skipOnPipes(t)
	t.Setenv("AUTODOC_TEST_DAEMON_STORE_DSN", pgtest.ScratchURL(t))
	dir := short(t)
	sock := ipc.ScratchAddr(dir, "a.sock")
	pg := "[store]\nengine = \"postgres\"\ndsn_env = \"AUTODOC_TEST_DAEMON_STORE_DSN\"\nschema = \"autodoc_daemon\"\n"
	cfg := writeConfig(t, sock, dir, pg)
	d := start(t, cfg, sock)
	pctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	info, err := rpc.Probe(pctx, ipc.Network, sock)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	if info.Engine != store.EngineOfPostgres || info.Schema != "autodoc_daemon" || len(info.StoreID) != 32 {
		t.Errorf("the daemon's hello: engine %q, schema %q, store_id %q", info.Engine, info.Schema, info.StoreID)
	}
	cli := dial(t, sock)
	call(t, cli, "sys.hello", map[string]any{"protocol": rpc.Protocol, "name": "test"})
	call(t, cli, "workspace.add", "kb", t.TempDir(), nil, nil)
	if out := d.out.String(); !strings.Contains(out, "PostgreSQL, schema autodoc_daemon") {
		t.Errorf("the daemon did not say where its store is: %s", out)
	}
	other := endpoint.Paths{Socket: sock, Engine: store.EngineOfPostgres, Schema: "another_schema"}
	_, err = endpoint.ProbePaths(context.Background(), sock, other)
	var m *endpoint.StoreMismatchError
	if !errors.As(err, &m) || !strings.Contains(err.Error(), "schema autodoc_daemon") || !strings.Contains(err.Error(), "schema another_schema") {
		t.Errorf("a client of another schema: %v, want a mismatch naming both", err)
	}
	same := endpoint.Paths{Socket: sock, Engine: store.EngineOfPostgres, Schema: "autodoc_daemon"}
	if _, err := endpoint.ProbePaths(context.Background(), sock, same); err != nil {
		t.Errorf("a client of this store: %v", err)
	}
}
