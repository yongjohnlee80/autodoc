package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
)

func checkReport(t *testing.T, out *bytes.Buffer) storeCheck {
	t.Helper()
	var r storeCheck
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("--check-store's report %q: %v", out.String(), err)
	}
	return r
}

// spoilPage overwrites page n (1-based, 4096 bytes) of the file at path with garbage.
func spoilPage(t *testing.T, path string, n int64) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xA5}, 4096), (n-1)*4096); err != nil {
		t.Fatal(err)
	}
}

// TestCheckStoreChecksHereOrThroughTheDaemon (AM-4): with no daemon, --check-store checks the store
// itself; with one serving it, the daemon checks it (sys.check) and keeps serving.
func TestCheckStoreChecksHereOrThroughTheDaemon(t *testing.T) {
	dir := short(t)
	sock, state := filepath.Join(dir, "a.sock"), filepath.Join(dir, "state")
	cfg := writeConfig(t, sock, state, "", "kb="+t.TempDir())
	var out bytes.Buffer
	if err := runCheckStore(context.Background(), cfg, &out); err != nil {
		t.Fatal(err)
	}
	if r := checkReport(t, &out); !r.OK || r.CheckedBy != "this process" || r.Store != filepath.Join(state, config.StoreName) {
		t.Fatalf("with no daemon: %+v", r)
	}
	start(t, cfg, sock)
	out.Reset()
	if err := runCheckStore(context.Background(), cfg, &out); err != nil {
		t.Fatal(err)
	}
	if r := checkReport(t, &out); !r.OK || r.CheckedBy != "daemon" {
		t.Fatalf("with the daemon serving: %+v", r)
	}
}

// TestADamagedStoreIsNotServed (AM-4): the daemon does not serve a store whose file fails its
// check, and says how to see why; --check-store reports it, and exits as a problem.
func TestADamagedStoreIsNotServed(t *testing.T) {
	dir := short(t)
	sock, state := filepath.Join(dir, "a.sock"), filepath.Join(dir, "state")
	cfg := writeConfig(t, sock, state, "", "kb="+t.TempDir())
	spoilPage(t, filepath.Join(state, config.StoreName), 6)
	err := runServe(context.Background(), cfg, io.Discard, testBuild, nil)
	if !errors.Is(err, store.ErrDamaged) || !strings.Contains(err.Error(), "--check-store") {
		t.Fatalf("serving a damaged store: %v; want it refused, naming --check-store", err)
	}
	var out bytes.Buffer
	if err := runCheckStore(context.Background(), cfg, &out); !errors.Is(err, errStoreProblems) {
		t.Fatalf("--check-store on a damaged store: %v, want errStoreProblems", err)
	}
	if r := checkReport(t, &out); r.OK || len(r.Integrity) == 0 {
		t.Fatalf("the report on a damaged store: %+v", r)
	}
}
