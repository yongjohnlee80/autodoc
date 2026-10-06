package app

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/dao/sqlite"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestCompactReclaimsAndRewritesTheStore (ADR 1791284787 §2.5): --compact, with the daemon stopped,
// reclaims a workspace's unused model and rewrites the store into incremental auto-vacuum mode; with
// the store's lease held (a daemon serving it), it is refused, saying how to stop the daemon.
func TestCompactReclaimsAndRewritesTheStore(t *testing.T) {
	ctx := context.Background()
	state := t.TempDir()
	cfg := writeConfig(t, filepath.Join(short(t), "s.sock"), state, "", "kb="+t.TempDir())
	path := filepath.Join(state, config.StoreName)
	raw, err := sqlite.OpenNamed(ctx, "seed:"+path, "file:"+path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "INSERT INTO model (workspace_id, fp, provider, name, dims, active, target) SELECT id, 'fake|old|x|3', 'fake', 'old', 3, 0, 0 FROM workspace WHERE name = 'kb'"); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.ExecContext(ctx, "INSERT INTO embedding (workspace_id, text_hash, model_fp, bits, f32) SELECT id, x'01', 'fake|old|x|3', x'00', x'00000000' FROM workspace WHERE name = 'kb'"); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	var out, errs bytes.Buffer
	if code := run(ctx, []string{"--config", cfg, "--compact"}, Options{Version: "v-test"}, &out, &errs); code != 0 {
		t.Fatalf("--compact: exit %d, %s", code, errs.String())
	}
	if !strings.Contains(out.String(), "kb: reclaimed fake|old|x|3, 1 vectors") || !strings.Contains(out.String(), "compacted ") {
		t.Errorf("--compact said %q", out.String())
	}
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if mode, _ := db.AutoVacuum(ctx); mode != store.AutoVacuumIncremental {
		t.Errorf("auto_vacuum after --compact: %d", mode)
	}

	// the lease held, as by a daemon serving the store
	out.Reset()
	errs.Reset()
	if code := run(ctx, []string{"--config", cfg, "--compact"}, Options{Version: "v-test"}, &out, &errs); code != 1 ||
		!strings.Contains(errs.String(), "the daemon is serving") {
		t.Errorf("--compact with the store held: exit %d, %q", code, errs.String())
	}
	_ = db.Close()
}
