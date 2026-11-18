//go:build !server

package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// pgLocation skips: a build without the server tag has no PostgreSQL store to open.
func pgLocation(t *testing.T) Location {
	t.Helper()
	t.Skip("the PostgreSQL store is the server build's: run with -tags server")
	return Location{}
}

// A build without the server tag has no PostgreSQL store: OpenAt of a PostgreSQL Location answers
// ErrNoPostgresInThisBuild, naming the server build, and never panics; a SQLite Location still
// opens.
func TestAnUntaggedBuildHasNoPostgresStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	loc := Location{Engine: EngineOfPostgres, DSN: "postgres://u@h/db", Schema: "autodoc_store", KeyFile: filepath.Join(dir, "store.key")}
	s, err := OpenAt(ctx, loc)
	if s != nil || !errors.Is(err, ErrNoPostgresInThisBuild) {
		t.Fatalf("OpenAt of a PostgreSQL Location: %v, %v; want ErrNoPostgresInThisBuild", s, err)
	}
	if want := "use the server build (-tags server)"; !strings.Contains(err.Error(), want) {
		t.Errorf("the error %q does not name the server build (%q)", err, want)
	}
	sq, err := OpenAt(ctx, Location{Path: filepath.Join(dir, "autodoc.db")})
	if err != nil {
		t.Fatalf("a SQLite Location: %v", err)
	}
	_ = sq.Close()
}
