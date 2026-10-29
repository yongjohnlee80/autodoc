package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
)

var daemonStoreImage = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "autodoc-test-store-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "autodoc.db")
	db, err := store.Open(context.Background(), path)
	if err != nil {
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

func openDaemonStore(t *testing.T) (*store.Store, error) {
	t.Helper()
	return openDaemonStoreAt(t, filepath.Join(t.TempDir(), "autodoc.db"))
}

// openDaemonStoreAt is openDaemonStore at path, its folder made: the store a config names, for a
// fake daemon that serves it as a real one would (launch_test, debug_test).
func openDaemonStoreAt(t *testing.T, path string) (*store.Store, error) {
	t.Helper()
	image, err := daemonStoreImage()
	if err != nil {
		return nil, fmt.Errorf("daemon store image: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, image, 0o600); err != nil {
		return nil, err
	}
	return store.Open(context.Background(), path)
}

func TestDaemonStoresKeepTheirOwnDataAndLease(t *testing.T) {
	ctx := context.Background()
	first, err := openDaemonStore(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	if err := first.SetPreference(ctx, "fixture.only", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := first.AddWorkspace(ctx, "first", t.TempDir(), nil, nil); err != nil {
		t.Fatal(err)
	}
	second, err := openDaemonStore(t)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	preferences, err := second.Preferences(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspaces, err := second.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(preferences) != 0 || len(workspaces) != 0 || first.Path() == second.Path() {
		t.Fatalf("daemon stores shared data: preferences %v, workspaces %v", preferences, workspaces)
	}
	if err := second.SetPreference(ctx, "fixture.only", "second"); err != nil {
		t.Fatal(err)
	}
	preferences, err = first.Preferences(ctx)
	if err != nil || preferences["fixture.only"] != "first" {
		t.Fatalf("writing the second store changed the first: %v, %v", preferences, err)
	}
	if duplicate, err := store.Open(ctx, first.Path()); !errors.Is(err, store.ErrBusy) {
		if duplicate != nil {
			_ = duplicate.Close()
		}
		t.Fatalf("opening the fixture twice: %v, want ErrBusy", err)
	}
}
