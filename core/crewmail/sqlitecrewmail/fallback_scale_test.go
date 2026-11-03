package sqlitecrewmail_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// handledAtScale is how many handled messages the store holds: a crew's weeks of mail, which the
// store keeps until retention takes it.
const handledAtScale = 20000

// TestTheNextFallbackIsReadFromTheDueMailNotEveryMessage (docs/decisions/crew.md#the-fallback-exactly-once):
// every mail wait asks NextFallbackAt inside its write, once per live session and kind, so it must
// cost what the due mail costs, not what the store keeps: with twenty live sessions, none with mail
// due, and twenty thousand handled messages, it answers nothing due quickly. Walking mail_message
// in created order, checking each message's deliveries, read the whole table per session and kind:
// seconds under the write lock every other write waits behind. What it answers when mail is due is
// the conformance's (mailtest's fallback cells, on this store in TestSQLiteKeepsTheMailStateMachines).
func TestTheNextFallbackIsReadFromTheDueMailNotEveryMessage(t *testing.T) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"), store.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	st := sqlitecrewmail.New(s)
	m := crew.New(st)
	e, err := m.Claim(ctx, "autodoc:aaaa", "run-a", "")
	if err != nil {
		t.Fatal(err)
	}
	var agents []crew.Registered
	for i := range 20 {
		r, err := m.Register(ctx, crew.Registration{Scope: "autodoc:aaaa", Run: "run-a", Epoch: e, Codename: fmt.Sprintf("agent%02d", i),
			Kind: "agy", Workspace: "w", Instance: fmt.Sprintf("agent-%d", i), Secret: strings.Repeat("s", 40)})
		if err != nil {
			t.Fatal(err)
		}
		agents = append(agents, r)
	}
	if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
		for i := range handledAtScale {
			id := fmt.Sprintf("m%05d", i)
			to := agents[i%len(agents)].Profile
			if err := tx.PutMessage(mail.Message{ID: id, Sender: agents[(i+1)%len(agents)].Profile, Kind: mail.KindMessage,
				Subject: "s", Body: "b", Created: int64(i + 1)}); err != nil {
				return err
			}
			if err := tx.PutDelivery(mail.Delivery{ID: "d" + id, Message: id, Recipient: to, Seq: int64(i/len(agents) + 1), State: mail.Acked}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	next := func() (int64, bool, time.Duration) {
		t.Helper()
		var at int64
		var has bool
		began := time.Now()
		if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
			var err error
			at, has, err = tx.NextFallbackAt(int64(time.Minute), int64(10*time.Minute))
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return at, has, time.Since(began)
	}
	// warm once: the first read of a fresh store pages it in
	next()
	at, has, took := next()
	if has {
		t.Fatalf("with nothing queued NextFallbackAt answered %d", at)
	}
	// a bound with room for -race on two CPUs: the walk took seconds here, the read from the due
	// mail takes milliseconds
	if took > 500*time.Millisecond {
		t.Fatalf("with %d handled messages and %d live sessions, nothing due, NextFallbackAt took %s", handledAtScale, len(agents), took)
	}

}
