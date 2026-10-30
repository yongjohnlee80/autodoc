package sqlitecrewmail_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestSQLiteConforms: the crew tables on the daemon's SQLite store keep the crew state machines,
// as the in-memory store does (crewtest.Run).
func TestSQLiteConforms(t *testing.T) {
	crewtest.Run(t, crewtest.Harness{New: func(t *testing.T, now func() time.Time) crew.Store {
		s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "autodoc.db"), store.WithClock(now))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		return sqlitecrewmail.New(s)
	}})
}
