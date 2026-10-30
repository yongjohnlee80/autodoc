package sqlitecrewmail

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/inbox/inboxtest"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestSQLiteKeepsTheHumanFeed: the feed's reads and the human's transitions on the daemon's SQLite
// store hold as the in-memory store's do (inboxtest.Run), with the commit signals fired by this
// adapter.
func TestSQLiteKeepsTheHumanFeed(t *testing.T) {
	inboxtest.Run(t, inboxtest.Harness{New: func(t *testing.T, now func() time.Time, sig crew.Signals) inboxtest.Stores {
		s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "autodoc.db"), store.WithClock(now))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		st := New(s, WithSignals(sig))
		return inboxtest.Stores{Inbox: st.Inbox(), Mail: st.Mail()}
	}})
}
