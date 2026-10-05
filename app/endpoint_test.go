package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// configSharing writes a config with its own socket and state directory over the store in data.
func configSharing(t *testing.T, sock, state, data string) string {
	t.Helper()
	body := fmt.Sprintf("[server]\nsocket = %q\nstate_dir = %q\ndata_dir = %q\n[follow]\npoll_interval = \"50ms\"\n", sock, state, data)
	p := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func printEndpoint(t *testing.T, cfg string) string {
	t.Helper()
	var out bytes.Buffer
	if err := runPrintEndpoint(context.Background(), cfg, false, &out); err != nil {
		t.Fatalf("--print-endpoint: %v", err)
	}
	return out.String()
}

// TestPrintEndpointNamesTheConfiguredSocket: with nothing serving the store, the endpoint is the
// config's own socket, as one line a client parses.
func TestPrintEndpointNamesTheConfiguredSocket(t *testing.T) {
	dir := short(t)
	sock := filepath.Join(dir, "s.sock")
	if got := printEndpoint(t, configSharing(t, sock, filepath.Join(dir, "state"), filepath.Join(dir, "data"))); got != "unix\t"+sock+"\n" {
		t.Errorf("--print-endpoint = %q", got)
	}
}

// TestASecondConfigFindsTheStoresDaemon: two configs over one store, with their own sockets and
// state directories: the second's clients find the daemon the first started, through the store's
// lease-info, and call it there, never spawning a second daemon into the lease.
func TestASecondConfigFindsTheStoresDaemon(t *testing.T) {
	dir := short(t)
	data := filepath.Join(dir, "data")
	a, b := filepath.Join(dir, "a.sock"), filepath.Join(dir, "b.sock")
	cfgA := configSharing(t, a, filepath.Join(dir, "state-a"), data)
	cfgB := configSharing(t, b, filepath.Join(dir, "state-b"), data)
	start(t, cfgA, a)
	if got := printEndpoint(t, cfgB); got != "unix\t"+a+"\n" {
		t.Errorf("--print-endpoint for the second config = %q, want the first daemon's %s", got, a)
	}
	var out bytes.Buffer
	if err := runCall(context.Background(), cfgB, "workspace.list", "", &out); err != nil {
		t.Fatalf("--call through the second config: %v", err)
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Errorf("a second daemon was started on %s: %v", b, err)
	}
}

// TestALeaseInfoNotBelievedLeavesTheConfiguredSocket: a lease-info whose address answers nothing,
// or names another store, is not believed: the endpoint is the config's own socket.
func TestALeaseInfoNotBelievedLeavesTheConfiguredSocket(t *testing.T) {
	dir := short(t)
	data := filepath.Join(dir, "data")
	sock := filepath.Join(dir, "s.sock")
	cfg := configSharing(t, sock, filepath.Join(dir, "state"), data)
	storePath := filepath.Join(data, "autodoc.db")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := store.Identity(storePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, li := range []store.LeaseInfo{
		{StoreID: id, Addr: filepath.Join(dir, "dead.sock"), Instance: "gone"},
		{StoreID: "another-store", Addr: filepath.Join(dir, "other.sock"), Instance: "x"},
	} {
		if err := store.WriteLeaseInfo(storePath, li); err != nil {
			t.Fatal(err)
		}
		if got := printEndpoint(t, cfg); !strings.HasSuffix(got, "\t"+sock+"\n") {
			t.Errorf("with lease-info %+v: --print-endpoint = %q, want the configured %s", li, got, sock)
		}
	}
}

// TestALiveDaemonOfAnotherStoreIsNotTakenForThisOne: a lease-info naming a socket where a daemon
// does answer, but for another store, or as another process than the record's, is not believed.
func TestALiveDaemonOfAnotherStoreIsNotTakenForThisOne(t *testing.T) {
	dir := short(t)
	a := filepath.Join(dir, "a.sock")
	start(t, configSharing(t, a, filepath.Join(dir, "state-a"), filepath.Join(dir, "data-a")), a)
	live, err := store.ReadLeaseInfo(filepath.Join(dir, "data-a", "autodoc.db"))
	if err != nil {
		t.Fatalf("the daemon wrote no lease-info: %v", err)
	}
	data := filepath.Join(dir, "data-b")
	sock := filepath.Join(dir, "b.sock")
	cfg := configSharing(t, sock, filepath.Join(dir, "state-b"), data)
	storePath := filepath.Join(data, "autodoc.db")
	if err := os.MkdirAll(data, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(storePath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := store.Identity(storePath)
	if err != nil {
		t.Fatal(err)
	}
	for name, li := range map[string]store.LeaseInfo{
		"another store's daemon": {StoreID: id, Addr: a, Instance: live.Instance},
	} {
		if err := store.WriteLeaseInfo(storePath, li); err != nil {
			t.Fatal(err)
		}
		if got := printEndpoint(t, cfg); !strings.HasSuffix(got, "\t"+sock+"\n") {
			t.Errorf("%s: --print-endpoint = %q, want the configured %s", name, got, sock)
		}
	}
	// the first daemon's own store, with a record naming another process: not believed either
	storeA := filepath.Join(dir, "data-a", "autodoc.db")
	wrong := live
	wrong.Instance = "someone-else"
	if err := store.WriteLeaseInfo(storeA, wrong); err != nil {
		t.Fatal(err)
	}
	cfgA2 := configSharing(t, filepath.Join(dir, "a2.sock"), filepath.Join(dir, "state-a2"), filepath.Join(dir, "data-a"))
	if got := printEndpoint(t, cfgA2); !strings.HasSuffix(got, "\t"+filepath.Join(dir, "a2.sock")+"\n") {
		t.Errorf("a record naming another process: --print-endpoint = %q", got)
	}
}
