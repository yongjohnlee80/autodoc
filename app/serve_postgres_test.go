//go:build server

package app

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/dao/postgres"

	"github.com/yongjohnlee80/autodoc/core/pglease"
	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/internal/client"
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
	// a daemon of another schema on this socket names the store it would serve, not a SQLite path
	pg2 := strings.Replace(pg, "autodoc_daemon", "another_schema", 1)
	err = runServe(context.Background(), writeConfig(t, sock, short(t), pg2), io.Discard, testBuild, nil, "")
	if err == nil || !strings.Contains(err.Error(), "that is not serving the PostgreSQL store in schema another_schema") {
		t.Errorf("--serve of another schema on this daemon's socket: %v", err)
	}
}

// A client that starts a daemon for a store on PostgreSQL whose last holder was killed (its lease
// unreleased, lapsing 20 seconds on, past the client's 15-second dial window) connects to that
// daemon once it opens, with no retry: the daemon binds its socket before it waits the lease out,
// and the client's hello waits for it. The mutant, named here: the store opened before the socket
// is bound (the client gives up dialing while the daemon waits).
func TestAClientStartsADaemonThatWaitsOutAKilledHoldersLease(t *testing.T) {
	skipOnPipes(t)
	ctx := context.Background()
	dsn := pgtest.ScratchURL(t)
	t.Setenv("AUTODOC_TEST_DAEMON_STORE_DSN", dsn)
	dir := short(t)
	sock := ipc.ScratchAddr(dir, "k.sock")
	cfg := writeConfig(t, sock, dir, "[store]\nengine = \"postgres\"\ndsn_env = \"AUTODOC_TEST_DAEMON_STORE_DSN\"\nschema = \"autodoc_killed\"\n")
	e, err := endpoint.Load(cfg)
	if err != nil {
		t.Fatal(err)
	}
	loc, err := e.StoreLocation()
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.OpenAt(ctx, loc) // the schema and its lease table, then let go
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	conn, err := postgres.OpenNamed(ctx, "killed-holder", dsn, postgres.WithConnectHook(func(ctx context.Context, c dao.ConnectedConn) error {
		_, err := c.ExecContext(ctx, `SET search_path TO "autodoc_killed"`)
		return err
	}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	if _, err := pglease.Claim(ctx, conn, pglease.Options{Table: store.LeaseTable, Scope: store.LeaseScope, TTL: 20 * time.Second, Info: "killed"}); err != nil {
		t.Fatal(err)
	}
	began := time.Now() // the holder is never renewed nor released: its lease lapses 20 seconds on

	p, err := e.Paths()
	if err != nil {
		t.Fatal(err)
	}
	var d *daemon
	spawn := func() (string, error) {
		sctx, cancel := context.WithCancel(context.Background())
		d = &daemon{t: t, sock: sock, out: &syncBuf{}, cancel: cancel, done: make(chan error, 1)}
		go func() { d.done <- runServe(sctx, cfg, d.out, testBuild, nil, "") }()
		return "", nil
	}
	s := client.NewSession(sock, spawn).As(rpc.CLIName).ForPaths(e.WithStore(p))
	cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	err = s.Connect(cctx)
	if d != nil {
		t.Cleanup(func() { d.stop() })
	}
	if err != nil {
		t.Fatalf("the client that started the daemon, after %s: %v", time.Since(began).Round(time.Second), err)
	}
	defer s.Close()
	if took := time.Since(began); took < 15*time.Second {
		t.Errorf("connected after %s, before the killed holder's lease lapsed", took.Round(time.Second))
	}
}

// --check-store and --compact are a SQLite store's: on a config whose store is on PostgreSQL each
// is refused (ErrNotSQLite) before it opens anything, naming PostgreSQL's own check or vacuum. The
// mutants, named here: either refusal deleted (the command opens the default SQLite path instead).
func TestTheSQLiteStoresCommandsRefuseAStoreOnPostgres(t *testing.T) {
	t.Setenv("AUTODOC_TEST_DAEMON_STORE_DSN", "postgres://u@h/db")
	dir := short(t)
	cfg := writeConfig(t, ipc.ScratchAddr(dir, "c.sock"), dir, "[store]\nengine = \"postgres\"\ndsn_env = \"AUTODOC_TEST_DAEMON_STORE_DSN\"\nschema = \"autodoc\"\n")
	for name, run := range map[string]func() error{
		"--check-store": func() error { return runCheckStore(context.Background(), cfg, io.Discard) },
		"--compact":     func() error { return runCompact(context.Background(), cfg, io.Discard) },
	} {
		if err := run(); !errors.Is(err, store.ErrNotSQLite) || !strings.Contains(err.Error(), "PostgreSQL") {
			t.Errorf("%s on a store on PostgreSQL: %v, want ErrNotSQLite", name, err)
		}
	}
}
