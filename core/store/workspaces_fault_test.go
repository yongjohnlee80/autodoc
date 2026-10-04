package store

import (
	"context"
	"errors"
	"testing"

	"github.com/yongjohnlee80/golib/dao/sqlite"
)

// The settings' storage failures surface as themselves: a workspace whose row cannot be read or
// written is not reported missing (ErrNoWorkspace), and a provider deleted under its workspace
// reads as the daemon's.
func TestWorkspaceSettingsSurfaceAStorageFailure(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)
	w, err := s.AddWorkspace(ctx, "kb", "/kb", []string{"*.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddProvider(ctx, ProviderSpec{Name: "local", Kind: KindOllama, BaseURL: "http://x", Model: "m"}); err != nil {
		t.Fatal(err)
	}
	raw := s.raw(t)
	exec := func(q string) {
		t.Helper()
		if _, err := raw.ExecContext(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	failed := func(what string, err error) {
		t.Helper()
		if err == nil || errors.Is(err, ErrNoWorkspace) {
			t.Errorf("%s = %v, want the storage failure", what, err)
		}
	}

	// a provider row gone without the cascade (foreign keys off on this connection alone)
	loose, err := sqlite.Open(ctx, "file:"+s.path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = loose.Close() }()
	if _, err := loose.ExecContext(ctx, "UPDATE workspace SET provider_id = 9999"); err != nil {
		t.Fatal(err)
	}
	if name, err := s.WorkspaceProvider(ctx, w.ID); err != nil || name != "" {
		t.Errorf("a dangling provider = %q, %v; want the daemon's", name, err)
	}

	// every write to the row fails
	exec("CREATE TRIGGER no_update BEFORE UPDATE ON workspace BEGIN SELECT RAISE(ABORT, 'injected'); END")
	failed("SetWorkspaceSchema", s.SetWorkspaceSchema(ctx, w.ID, "s.yaml"))
	failed("SetWorkspaceTextExtensions", s.SetWorkspaceTextExtensions(ctx, w.ID, []string{".log"}))
	failed("SetWorkspaceProvider", s.SetWorkspaceProvider(ctx, w.ID, ""))
	failed("SetWorkspacePatterns", s.SetWorkspacePatterns(ctx, w.ID, []string{"*.txt"}, nil))
	exec("DROP TRIGGER no_update")

	// the patterns cannot be deleted
	exec("CREATE TRIGGER no_delete BEFORE DELETE ON workspace_pattern BEGIN SELECT RAISE(ABORT, 'injected'); END")
	failed("SetWorkspacePatterns (delete)", s.SetWorkspacePatterns(ctx, w.ID, []string{"*.txt"}, nil))
	exec("DROP TRIGGER no_delete")

	// the patterns cannot be read
	exec("ALTER TABLE workspace_pattern RENAME COLUMN pattern TO pattern_gone")
	_, err = s.Workspaces(ctx)
	failed("Workspaces (patterns)", err)
	exec("ALTER TABLE workspace_pattern RENAME COLUMN pattern_gone TO pattern")

	// the provider cannot be looked up
	exec("ALTER TABLE embedding_provider RENAME COLUMN name TO name_gone")
	failed("SetWorkspaceProvider (lookup)", s.SetWorkspaceProvider(ctx, w.ID, "local"))
	exec("ALTER TABLE embedding_provider RENAME COLUMN name_gone TO name")

	// the row's settings cannot be read
	exec("ALTER TABLE workspace RENAME COLUMN schema_path TO schema_path_gone")
	_, err = s.SchemaPath(ctx, w.ID)
	failed("SchemaPath", err)
	_, err = s.Workspaces(ctx)
	failed("Workspaces", err)
	exec("ALTER TABLE workspace RENAME COLUMN schema_path_gone TO schema_path")
	exec("ALTER TABLE workspace RENAME COLUMN text_extensions TO text_extensions_gone")
	_, err = s.TextExtensions(ctx, w.ID)
	failed("TextExtensions", err)
	exec("ALTER TABLE workspace RENAME COLUMN text_extensions_gone TO text_extensions")
	exec("ALTER TABLE workspace RENAME COLUMN provider_id TO provider_id_gone")
	_, err = s.WorkspaceProvider(ctx, w.ID)
	failed("WorkspaceProvider", err)
	exec("ALTER TABLE workspace RENAME COLUMN provider_id_gone TO provider_id")

	// and the row reads again once the storage is whole
	if _, err := s.SchemaPath(ctx, w.ID); err != nil {
		t.Errorf("after the repair: %v", err)
	}
}
