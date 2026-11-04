package sqlitecrewmail_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// A read the store can't make is the call's error, never a zero answer taken for "nothing due":
// within a write whose context has ended, NextSeq, NextFallbackAt and FallbackDue each answer
// the failure. FallbackDue refuses a limit below one before it reads anything.
func TestAFailedMailReadIsTheCallsError(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"), store.WithClock(crewtest.NewClock().Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	st := sqlitecrewmail.New(s)

	if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
		if _, err := tx.FallbackDue(0, 0, 0); err == nil {
			t.Error("FallbackDue with a limit of 0 answered no error")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ended, cancel := context.WithCancel(ctx)
	_ = st.Mail().Write(ended, func(tx mail.Tx) error {
		cancel() // the write's context ends: every read after it fails
		if _, err := tx.NextSeq("p-1"); !errors.Is(err, context.Canceled) {
			t.Errorf("NextSeq after the write's context ended: %v, want context.Canceled", err)
		}
		if _, _, err := tx.NextFallbackAt(int64(time.Minute), int64(10*time.Minute)); !errors.Is(err, context.Canceled) {
			t.Errorf("NextFallbackAt after the write's context ended: %v, want context.Canceled", err)
		}
		if _, err := tx.FallbackDue(1, 1, 10); !errors.Is(err, context.Canceled) {
			t.Errorf("FallbackDue after the write's context ended: %v, want context.Canceled", err)
		}
		return nil
	})
}
