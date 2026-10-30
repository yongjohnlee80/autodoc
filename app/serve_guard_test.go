package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/fsbounds"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// TestAGuardThatCannotBeBuiltStopsTheStart: a daemon whose guard cannot be built (a relative path
// with no working folder to resolve it against) does not start: it serves nothing unguarded, and
// leaves no socket.
func TestAGuardThatCannotBeBuiltStopsTheStart(t *testing.T) {
	failed := errors.New("no working folder")
	defer func(f func(...string) (*fsbounds.Guard, error)) { newGuard = f }(newGuard)
	newGuard = func(...string) (*fsbounds.Guard, error) { return nil, failed }
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute) // a daemon that serves anyway stops here
	defer cancel()
	err := runServe(ctx, writeConfig(t, sock, filepath.Join(dir, "state"), "", "kb="+t.TempDir()), io.Discard, testBuild, nil, "")
	if !errors.Is(err, failed) {
		t.Fatalf("a daemon whose guard failed: %v, want %v", err, failed)
	}
	if _, err := os.Stat(sock); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the daemon left its socket: %v", err)
	}
}

// TestTheDiskOutsideEveryWorkspaceRefusesABoundedCall: the daemon's files outside every workspace
// (the whole disk, a local peer's file.*) answer the user's call, and refuse any bounded one that
// reaches them, should a route ever skip a workspace's own.
func TestTheDiskOutsideEveryWorkspaceRefusesABoundedCall(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a.md")
	writeAt(t, p, "# A\n")
	reg, err := registrations.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	d := &process{b: build{reg: reg}, log: logger.New(logger.WithWriter(io.Discard))}
	defer d.stops.unwind()
	if err := d.openFiles(); err != nil {
		t.Fatal(err)
	}
	name := strings.TrimPrefix(p, "/") // as rpc names a file on the disk (its root is "/")
	if _, err := d.files.Read(context.Background(), name); err != nil {
		t.Fatalf("the user's read: %v", err)
	}
	for _, within := range []string{fsbounds.AnyWorkspace, "kb"} {
		ctx := fsbounds.With(context.Background(), fsbounds.Bounds{Within: within})
		if _, err := d.files.Read(ctx, name); !errors.Is(err, fsbounds.ErrRefused) {
			t.Errorf("a read bounded within %q of the disk outside every workspace: %v, want refused", within, err)
		}
	}
}
