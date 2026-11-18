//go:build server

package daemon

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/pgstore/pgtest"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// The start's compaction leaves a store on PostgreSQL to PostgreSQL's vacuum: it neither compacts
// nor logs a failure, and records no compaction event. The mutant, named here: the ErrNotSQLite
// skip deleted (every start on PostgreSQL logs store.compact.failed).
func TestTheStartLeavesAStoreOnPostgresToItsVacuum(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.OpenAt(ctx, store.Location{Engine: store.EngineOfPostgres, DSN: pgtest.ScratchURL(t), Schema: "autodoc_store",
		KeyFile: filepath.Join(t.TempDir(), "store.key")})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var log bytes.Buffer
	m := New(ctx, db, Options{Log: logger.New(logger.WithWriter(&log))})
	defer m.StopAll()
	m.convert(ctx)
	if strings.Contains(log.String(), "store.compact") {
		t.Errorf("the start's compaction on PostgreSQL logged:\n%s", log.String())
	}
	evs, _, _, err := db.Events(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if strings.HasPrefix(e.Kind, "store.compact") {
			t.Errorf("the start's compaction on PostgreSQL recorded %s: %s", e.Kind, e.Detail)
		}
	}
}
