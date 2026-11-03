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

// growthAllowed is how much slower NextFallbackAt may be with the handled history than without it,
// on the same store and runner, before it counts as reading the history: the read from the due
// mail costs the same at any size, the walk grows with every message.
const growthAllowed = 5

// growthSlack is a fixed allowance on top, for a runner's noise on a fast read.
const growthSlack = 50 * time.Millisecond

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
		return tx.TakeFallbacks("autodoc:aaaa", e) // the scope's host takes fallbacks: deliveries due are candidates
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
	// fastest is the best of a few reads: a runner's noise only ever slows one
	fastest := func() (int64, bool, time.Duration) {
		t.Helper()
		next() // warm: the first read pages the store in
		at, has, took := next()
		for range 2 {
			if _, _, d := next(); d < took {
				took = d
			}
		}
		return at, has, took
	}
	_, _, base := fastest() // the crew alone, no history
	if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
		for i := range handledAtScale {
			id := fmt.Sprintf("m%05d", i)
			to := agents[i%len(agents)].Profile
			if err := tx.PutMessage(mail.Message{ID: id, Sender: agents[(i+1)%len(agents)].Profile, IdemKey: id, Kind: mail.KindMessage,
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

	at, has, took := fastest()
	if has {
		t.Fatalf("with nothing queued NextFallbackAt answered %d", at)
	}
	// the bound is relative, the same store on the same runner without its history: -race runs the
	// store's SQLite 10-20x slower (0.5-0.66 s on a loaded CI runner, where a fixed 0.5 s failed), and
	// without -race the walk took under 5 s, so no fixed bound tells them apart in both
	if limit := growthAllowed*base + growthSlack; took > limit {
		t.Fatalf("with %d handled messages and %d live sessions, nothing due, NextFallbackAt took %s; without the history %s (limit %s)",
			handledAtScale, len(agents), took, base, limit)
	}

	// among them, one agent's two queued messages: the earlier decides, from the later of its
	// creation and the agent's session's start, plus the unreachable window (its session registered
	// no endpoint, so it can't be woken: the shorter window of the two), as fast
	sent := clock.Now().UnixNano()
	if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
		for i, created := range []int64{sent + 2, sent} {
			id := fmt.Sprintf("due%d", i)
			if err := tx.PutMessage(mail.Message{ID: id, Sender: agents[1].Profile, IdemKey: id, Kind: mail.KindMessage,
				Subject: "s", Body: "b", Created: created}); err != nil {
				return err
			}
			if err := tx.PutDelivery(mail.Delivery{ID: "d" + id, Message: id, Recipient: agents[7].Profile, Seq: int64(handledAtScale + i),
				State: mail.Queued}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var started int64
	if err := st.Mail().Read(ctx, func(tx mail.Tx) error {
		ss, ok, err := tx.Session(agents[7].Session)
		if !ok && err == nil {
			t.Fatal("agent07's session is gone")
		}
		started = ss.Started
		return err
	}); err != nil {
		t.Fatal(err)
	}
	at, has, took = fastest()
	if want := max(sent, started) + int64(time.Minute); !has || at != want {
		t.Fatalf("with two messages queued to agent07 NextFallbackAt answered %d (%v), want %d", at, has, want)
	}
	if limit := growthAllowed*base + growthSlack; took > limit {
		t.Fatalf("with mail due, NextFallbackAt took %s; without the history %s (limit %s)", took, base, limit)
	}
}
