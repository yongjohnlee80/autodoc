package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/dao/sqlite"

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
	mark := filepath.Join(state, config.StoreName) + ".damaged"
	if err := os.WriteFile(mark, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := runCheckStore(context.Background(), cfg, &out); !errors.Is(err, errStoreProblems) {
		t.Fatalf("--check-store on a damaged store: %v, want errStoreProblems", err)
	}
	if r := checkReport(t, &out); r.OK || len(r.Integrity) == 0 || r.MarkCleared {
		t.Fatalf("the report on a damaged store: %+v", r)
	}
	if _, err := os.Stat(mark); err != nil {
		t.Fatal("a check that found damage cleared the damage mark")
	}
}

// fillStore adds a filler table of n rows of 1 KiB to the store at path (pages far from its
// schema), and checkpoints it into the file.
func fillStore(t *testing.T, path string, n int) {
	t.Helper()
	ctx := context.Background()
	raw, err := sqlite.Open(ctx, "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	for _, q := range []string{"CREATE TABLE filler (id INTEGER PRIMARY KEY, body BLOB)",
		fmt.Sprintf("INSERT INTO filler (body) SELECT randomblob(1024) FROM (WITH RECURSIVE c(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM c WHERE x < %d) SELECT x FROM c)", n),
		"PRAGMA wal_checkpoint(TRUNCATE)"} {
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDamageFoundAfterTheStartStopsTheDaemon (AM-4): a store whose start check runs past its wait
// is served while the daemon finishes the check; the damage it finds stops the daemon and marks the
// store, so the next start refuses it.
func TestDamageFoundAfterTheStartStopsTheDaemon(t *testing.T) {
	dir := short(t)
	sock, state := filepath.Join(dir, "a.sock"), filepath.Join(dir, "state")
	cfg := writeConfig(t, sock, state, "", "kb="+t.TempDir())
	path := filepath.Join(state, config.StoreName)
	fillStore(t, path, 300)
	if _, err := os.Stat(path + "-wal"); err == nil {
		t.Fatal("precondition: the write-ahead log is still there")
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	spoilPage(t, path, fi.Size()/4096)
	was := startCheckWait
	startCheckWait = time.Nanosecond
	t.Cleanup(func() { startCheckWait = was })
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	out := &syncBuf{}
	if err := runServe(ctx, cfg, out, testBuild, nil); err != nil || ctx.Err() != nil {
		t.Fatalf("the daemon over a store damaged far from its schema: %v (ctx %v); want it started, then stopped by its check\n%s", err, ctx.Err(), out)
	}
	if _, err := os.Stat(path + ".damaged"); err != nil {
		t.Fatalf("the damage found was not marked: %v", err)
	}
	if err := runServe(context.Background(), cfg, io.Discard, testBuild, nil); !errors.Is(err, store.ErrDamaged) {
		t.Fatalf("the next start: %v; want the marked store checked whole and refused", err)
	}
}

// TestCheckStoreClearsTheMarkOfASoundStore (AM-4): a store marked damaged is refused at every
// start; --check-store's whole check, finding it sound (restored), clears the mark, and the daemon
// serves it again.
func TestCheckStoreClearsTheMarkOfASoundStore(t *testing.T) {
	dir := short(t)
	sock, state := filepath.Join(dir, "a.sock"), filepath.Join(dir, "state")
	cfg := writeConfig(t, sock, state, "", "kb="+t.TempDir())
	mark := filepath.Join(state, config.StoreName) + ".damaged"
	if err := os.WriteFile(mark, []byte(`{"quick":true,"integrity":["page 9: bad"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := runServe(context.Background(), cfg, io.Discard, testBuild, nil); !errors.Is(err, store.ErrDamaged) {
		t.Fatalf("serving a marked store: %v, want it refused", err)
	}
	var out bytes.Buffer
	if err := runCheckStore(context.Background(), cfg, &out); err != nil {
		t.Fatal(err)
	}
	if r := checkReport(t, &out); !r.OK || !r.MarkCleared {
		t.Fatalf("--check-store on a sound, marked store: %+v; want it sound and the mark cleared", r)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Fatal("the mark is still there")
	}
	start(t, cfg, sock)
}
