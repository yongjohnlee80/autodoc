package sqlitecrewmail

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// A sweep reads only the mail older than its window
// (docs/decisions/crew.md#handled-mail-is-kept-for-a-window): over a store of 20,000 young
// messages of 4 KB each and three old handled ones, the old ones are deleted and the young kept,
// and a sweep then costs what one over 200 young messages does, never a walk of the store: each
// write reads a page of the oldest messages by mail_message_created, and no young one is read.
func TestASweepReadsOnlyTheOldMail(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	const keep = 30 * 24 * time.Hour
	type swept struct {
		m  *mail.Manager
		st *Store
	}
	seeded := func(young int) swept {
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"), store.WithClock(func() time.Time { return now }))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		st := New(s)
		old := now.Add(-40 * 24 * time.Hour).UnixNano()
		fresh := now.Add(-time.Hour).UnixNano()
		if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
			// a body as mail carries: the table keeps every row whole, so a walk of it reads each one
			body := strings.Repeat("a message's text. ", 4096/18)
			put := func(id string, at int64) error {
				if err := tx.PutMessage(mail.Message{ID: id, Sender: "p-sender", IdemKey: id, Digest: []byte{1},
					Kind: mail.KindMessage, Body: body, State: mail.MessageOpen, Created: at}); err != nil {
					return err
				}
				return tx.PutDelivery(mail.Delivery{ID: "d-" + id, Message: id, Recipient: "p-recipient", Seq: int64(len(id)) + at,
					State: mail.Acked, DoneAt: &at})
			}
			for i := range 3 {
				if err := put(fmt.Sprintf("old-%d", i), old+int64(i)); err != nil {
					return err
				}
			}
			for i := range young {
				if err := put(fmt.Sprintf("young-%06d", i), fresh+int64(i)); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		return swept{m: mail.New(st.Mail(), nil), st: st}
	}
	count := func(st *Store) (old, young int) {
		in := inspector{t: t, st: st}
		for _, msg := range in.Messages() {
			if msg.Created < now.Add(-keep).UnixNano() {
				old++
			} else {
				young++
			}
		}
		return old, young
	}
	// fastest is the least time of five sweeps, each with nothing left to delete
	fastest := func(s swept) time.Duration {
		best := time.Duration(1 << 62)
		for range 5 {
			start := time.Now()
			if _, err := s.m.Sweep(ctx, keep); err != nil {
				t.Fatal(err)
			}
			best = min(best, time.Since(start))
		}
		return best
	}
	small, big := seeded(200), seeded(20_000)
	got, err := big.m.Sweep(ctx, keep)
	if err != nil {
		t.Fatal(err)
	}
	if old, young := count(big.st); got.Messages != 3 || old != 0 || young != 20_000 {
		t.Fatalf("the first sweep deleted %d; left %d old and %d young, want the 3 old deleted and every young one kept",
			got.Messages, old, young)
	}
	if _, err := small.m.Sweep(ctx, keep); err != nil {
		t.Fatal(err)
	}
	s, b := fastest(small), fastest(big)
	t.Logf("the fastest sweep over 200 young messages: %v; over 20,000: %v", s, b)
	if b > 5*s+3*time.Millisecond {
		t.Fatalf("a sweep over 20,000 young messages took %v, over 200 %v: it read the young mail, not only the old", b, s)
	}
}
