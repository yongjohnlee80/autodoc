package inbox_test

import (
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/inbox/inboxtest"
	"github.com/yongjohnlee80/autodoc/core/mail/mailtest"
)

// TestMemConforms: the in-memory store keeps the human feed (inboxtest.Run).
func TestMemConforms(t *testing.T) {
	inboxtest.Run(t, inboxtest.Harness{New: func(_ *testing.T, now func() time.Time, sig crew.Signals) inboxtest.Stores {
		m := mailtest.NewMem(now)
		m.SetSignals(sig)
		return inboxtest.Stores{Inbox: m.Inbox(), Mail: m}
	}})
}
