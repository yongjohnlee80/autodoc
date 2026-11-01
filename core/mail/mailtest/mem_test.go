package mailtest_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/mail/mailtest"
)

// TestMemHoldsTheSchemasUniqueKeys: the in-memory store refuses what the schema's unique keys
// refuse, as SQLite does, so a conformance cell run on it can't pass on a write the engine would
// fail: a second message of one sender's idempotency key, a second response of one sender to one
// request, a second delivery at one recipient's seq or of one message to one recipient, and a
// second fallback job for one delivery. Rewriting a row under its own id is no conflict, and a
// refused write commits nothing.
func TestMemHoldsTheSchemasUniqueKeys(t *testing.T) {
	ctx := context.Background()
	m := mailtest.NewMem(time.Now)
	put := func(fn func(mail.Tx) error) error { return m.Write(ctx, fn) }
	if err := put(func(tx mail.Tx) error {
		for _, msg := range []mail.Message{
			{ID: "m1", Sender: "p-a", IdemKey: "k1", Kind: mail.KindRequest},
			{ID: "r1", Sender: "p-b", IdemKey: "k2", Kind: mail.KindResponse, ReplyTo: "m1"},
		} {
			if err := tx.PutMessage(msg); err != nil {
				return err
			}
		}
		if err := tx.PutMessage(mail.Message{ID: "m1", Sender: "p-a", IdemKey: "k1", Kind: mail.KindRequest, Subject: "again"}); err != nil {
			return err // its own row, rewritten
		}
		if err := tx.PutDelivery(mail.Delivery{ID: "d1", Message: "m1", Recipient: "p-b", Seq: 1}); err != nil {
			return err
		}
		return tx.PutFallbackJob(mail.FallbackJob{ID: "j1", Delivery: "d1", Message: "m1", Recipient: "p-b", State: mail.JobOpen})
	}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, want string
		put        func(mail.Tx) error
	}{
		{"one sender's key twice", "UNIQUE (sender, idem_key)", func(tx mail.Tx) error {
			return tx.PutMessage(mail.Message{ID: "m2", Sender: "p-a", IdemKey: "k1", Kind: mail.KindRequest})
		}},
		{"a second response to one request", "mail_one_reply", func(tx mail.Tx) error {
			return tx.PutMessage(mail.Message{ID: "r2", Sender: "p-b", IdemKey: "k3", Kind: mail.KindResponse, ReplyTo: "m1"})
		}},
		{"one recipient's seq twice", "UNIQUE (recipient, seq)", func(tx mail.Tx) error {
			return tx.PutDelivery(mail.Delivery{ID: "d2", Message: "r1", Recipient: "p-b", Seq: 1})
		}},
		{"one message to one recipient twice", "UNIQUE (message, recipient)", func(tx mail.Tx) error {
			return tx.PutDelivery(mail.Delivery{ID: "d2", Message: "m1", Recipient: "p-b", Seq: 2})
		}},
		{"a second job for one delivery", "already has job j1", func(tx mail.Tx) error {
			return tx.PutFallbackJob(mail.FallbackJob{ID: "j2", Delivery: "d1", Message: "m1", Recipient: "p-b", State: mail.JobOpen})
		}},
	} {
		if err := put(c.put); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %s refused", c.name, err, c.want)
		}
	}
	if n, d, j := len(m.Messages()), len(m.Deliveries()), len(m.Jobs()); n != 2 || d != 1 || j != 1 {
		t.Errorf("after the refused writes: %d messages, %d deliveries, %d jobs; want 2, 1, 1", n, d, j)
	}
	if err := put(func(tx mail.Tx) error {
		return tx.PutMessage(mail.Message{ID: "r3", Sender: "p-c", IdemKey: "k4", Kind: mail.KindResponse, ReplyTo: "m1"})
	}); err != nil {
		t.Errorf("another sender's response to the request: %v, want it stored", err)
	}
}
