package daemon

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestASweepThatCannotReadTheStoreSaysSo: the start sweep and the conversion each say why they did
// nothing when the store cannot be read, and return; the next start tries again.
func TestASweepThatCannotReadTheStoreSaysSo(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	var log bytes.Buffer
	m := New(ctx, db, Options{Poll: 20 * time.Millisecond, Log: logger.New(logger.WithWriter(&log))})
	_ = db.Close()
	m.SweepModels(ctx)
	m.convert(ctx)
	for _, event := range []string{"models.sweep.failed", "store.compact.failed"} {
		if !strings.Contains(log.String(), event) {
			t.Errorf("no %s in the log: %s", event, log.String())
		}
	}
}
