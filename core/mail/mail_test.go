package mail_test

import (
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/mail/mailtest"
)

// The crew module is the mail module's Crew.
var _ mail.Crew = (*crew.Manager)(nil)

// TestMemConforms: the in-memory store keeps the mail state machines (mailtest.Run).
func TestMemConforms(t *testing.T) {
	mailtest.Run(t, mailtest.Harness{New: func(_ *testing.T, now func() time.Time) mailtest.Stores {
		m := mailtest.NewMem(now)
		return mailtest.Stores{Mail: m, Crew: m.Crew(), Inspect: m}
	}})
}

// A crew effect on a transaction that is not mail's is an error, never a panic.
func TestTheEffectsRefuseAnotherTransaction(t *testing.T) {
	if err := mail.New(nil, nil).DiscardDeliveries(nil, "p-gold"); err == nil {
		t.Fatal("a nil transaction was taken")
	}
}
