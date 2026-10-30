package mail_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/mail/mailtest"
)

// lazyCrew is the crew Manager as mail's port, set once both modules exist: each is the other's
// dependency (mail authenticates through crew; crew's adopt calls mail's effects).
type lazyCrew struct{ m *crew.Manager }

func (c *lazyCrew) Authenticate(tx crew.Tx, credential string) (crew.Session, error) {
	return c.m.Authenticate(tx, credential)
}

func (c *lazyCrew) RaisePending(tx crew.Tx, profile string) error {
	return c.m.RaisePending(tx, profile)
}

// The real crew.Adopt over mail's real effects, on one store (Zen's #62 r0 F3): a profile of the
// adopted scope sent itself and its namesake in the adopting scope a message, and the namesake's
// copy had failed. Adoption reopens it, and the reopening notice, to the old profile as the sender,
// must follow that profile's merge to the survivor: nothing is left on a profile nobody receives.
func TestAdoptionsNoticesFollowTheMergeThroughCrewsAdopt(t *testing.T) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	mem := mailtest.NewMem(clock.Now)
	port := &lazyCrew{}
	mm := mail.New(mem, port)
	cm := crew.New(mem.Crew(), crew.WithSessionEnder(mm))
	port.m = cm

	secret := strings.Repeat("s", 40)
	host := func(scope, run string) int64 {
		t.Helper()
		e, err := cm.Claim(ctx, scope, run, "")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	register := func(scope, run string, epoch int64, instance string) crew.Registered {
		t.Helper()
		r, err := cm.Register(ctx, crew.Registration{Scope: scope, Run: run, Epoch: epoch, Codename: "gold",
			Kind: "claude", Workspace: "w", Instance: instance, Secret: secret})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	oldEpoch := host("autodoc:old", "r1")
	old := register("autodoc:old", "r1", oldEpoch, "i1")
	newEpoch := host("autodoc:new", "r2")
	survivor := register("autodoc:new", "r2", newEpoch, "i2")

	self, err := mm.Send(ctx, crew.Credential(old.Session, secret), mail.Send{To: []string{"gold", "gold@autodoc:new"}, Body: "self", Key: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.Write(ctx, func(tx mail.Tx) error {
		for _, d := range self.Deliveries {
			if d.Recipient == survivor.Profile {
				d.State, d.Why = mail.Failed, mail.WhyAttempts
				return tx.PutDelivery(d)
			}
		}
		t.Fatal("no delivery to the survivor")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := cm.Release(ctx, "autodoc:old", "r1", oldEpoch); err != nil {
		t.Fatal(err)
	}
	if err := cm.Adopt(ctx, "autodoc:old", "autodoc:new", "r2", newEpoch); err != nil {
		t.Fatal(err)
	}

	byID := map[string]mail.Message{}
	for _, m := range mem.Messages() {
		byID[m.ID] = m
	}
	reopened := 0
	for _, d := range mem.Deliveries() {
		m := byID[d.Message]
		if d.Recipient == old.Profile && (d.State == mail.Queued || d.State == mail.Leased) {
			t.Errorf("a delivery was left on the merged profile: %+v (%s %q)", d, m.Kind, m.Body)
		}
		if m.Kind == mail.KindEvent && m.Body == mail.WhyReopened && m.ReplyTo == self.Message.ID {
			reopened++
			if d.Recipient != survivor.Profile || d.State != mail.Queued {
				t.Errorf("the reopening notice: %+v, want queued for the survivor %s", d, survivor.Profile)
			}
		}
	}
	if reopened != 1 {
		t.Fatalf("%d reopening notices, want 1", reopened)
	}
}

// The real crew.Discard over mail's real effects: a discarded profile's self-sent message leaves
// nothing waiting on it, its notice is undeliverable history, and a later send into the scope is
// refused (Zen's #62 r2). It needs crew to mark the scope gone before DiscardDeliveries.
func TestDiscardLeavesNothingWaitingThroughCrewsDiscard(t *testing.T) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	mem := mailtest.NewMem(clock.Now)
	port := &lazyCrew{}
	mm := mail.New(mem, port)
	cm := crew.New(mem.Crew(), crew.WithSessionEnder(mm))
	port.m = cm
	secret := strings.Repeat("s", 40)
	oldEpoch, err := cm.Claim(ctx, "autodoc:old", "r1", "")
	if err != nil {
		t.Fatal(err)
	}
	old, err := cm.Register(ctx, crew.Registration{Scope: "autodoc:old", Run: "r1", Epoch: oldEpoch, Codename: "gold",
		Kind: "claude", Workspace: "w", Instance: "i1", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	newEpoch, err := cm.Claim(ctx, "autodoc:new", "r2", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := cm.Register(ctx, crew.Registration{Scope: "autodoc:new", Run: "r2", Epoch: newEpoch, Codename: "zen",
		Kind: "claude", Workspace: "w", Instance: "i2", Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mm.Send(ctx, crew.Credential(old.Session, secret), mail.Send{To: []string{"gold"}, Body: "self", Key: "k"}); err != nil {
		t.Fatal(err)
	}
	if err := cm.Release(ctx, "autodoc:old", "r1", oldEpoch); err != nil {
		t.Fatal(err)
	}
	if err := cm.Discard(ctx, "autodoc:old", "autodoc:new", "r2", newEpoch); err != nil {
		t.Fatal(err)
	}
	for _, d := range mem.Deliveries() {
		if d.Recipient == old.Profile && (d.State == mail.Queued || d.State == mail.Leased) {
			t.Errorf("a delivery waits on the discarded profile: %+v", d)
		}
	}
	if _, err := mm.Send(ctx, crew.Credential(other.Session, secret), mail.Send{To: []string{"gold@autodoc:old"}, Body: "b", Key: "later"}); !errors.Is(err, mail.ErrNoRecipient) {
		t.Fatalf("a send into the discarded scope: %v, want ErrNoRecipient", err)
	}
}
