package sqlitecrewmail_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestTheSnapshotAndAHostsSessionsReadWhatIsLive (docs/decisions/crew.md#the-crew-and-mail-tables):
// a watch's snapshot, at every window's start, reads the user's open mail, and a host's claim its
// live sessions. With twenty thousand messages the user has handled, and a host whose sessions
// mostly ended beside another host's live one, each reads what is open or live, never the history:
// the user's open deliveries by state (mail_delivery_state), the host's sessions from the live ones
// (crew_session_live). The answers are as before: the open requests and unread reports past the
// cursor, in seq order, to the limit, a generated notice never among them; the host's own live
// sessions, by id.
func TestTheSnapshotAndAHostsSessionsReadWhatIsLive(t *testing.T) {
	rec := sqlitecrewmail.RecordStatements(t)
	ctx := context.Background()
	clock := crewtest.NewClock()
	path := filepath.Join(t.TempDir(), "autodoc.db")
	s, err := store.Open(ctx, path, store.WithClock(clock.Now))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	st := sqlitecrewmail.New(s)
	m := crew.New(st)
	const scope = "autodoc:aaaa"
	e, err := m.Claim(ctx, scope, "run-a", "")
	if err != nil {
		t.Fatal(err)
	}
	var agents []crew.Registered
	for i := range 30 {
		r, err := m.Register(ctx, crew.Registration{Scope: scope, Run: "run-a", Epoch: e, Codename: fmt.Sprintf("agent%02d", i),
			Kind: "agy", Workspace: "w", Instance: fmt.Sprintf("agent-%d", i), Secret: strings.Repeat("s", 40)})
		if err != nil {
			t.Fatal(err)
		}
		agents = append(agents, r)
	}
	for _, a := range agents[5:] { // most of the host's sessions ended
		if err := m.Exit(ctx, scope, "run-a", e, a.Session, crew.EndedExit); err != nil {
			t.Fatal(err)
		}
	}
	live := agents[:5]
	// another host's live session, never one of this host's
	e2, err := m.Claim(ctx, "autodoc:bbbb", "run-b", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Register(ctx, crew.Registration{Scope: "autodoc:bbbb", Run: "run-b", Epoch: e2, Codename: "other",
		Kind: "agy", Workspace: "w", Instance: "other-0", Secret: strings.Repeat("s", 40)}); err != nil {
		t.Fatal(err)
	}

	body := strings.Repeat("b", 4096)
	if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
		put := func(seq int64, kind, state, delivery string) error {
			id := fmt.Sprintf("m%05d", seq)
			if err := tx.PutMessage(mail.Message{ID: id, Sender: live[seq%5].Profile, IdemKey: id, Kind: kind,
				Subject: "s", Body: body, State: state, Created: seq}); err != nil {
				return err
			}
			return tx.PutDelivery(mail.Delivery{ID: "d" + id, Message: id, Recipient: mail.UserProfile, Seq: seq, State: delivery})
		}
		for seq := int64(1); seq <= 20000; seq++ { // what the user has handled
			if err := put(seq, mail.KindMessage, mail.MessageRead, mail.Acked); err != nil {
				return err
			}
		}
		// what is open: a report, a request, a report; and a generated notice, never listed
		for _, o := range []struct {
			seq  int64
			kind string
		}{{20001, mail.KindMessage}, {20002, mail.KindRequest}, {20003, mail.KindEvent}, {20004, mail.KindMessage}} {
			if err := put(o.seq, o.kind, mail.MessageOpen, mail.Queued); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	rec.Reset()
	if err := st.Inbox().Read(ctx, func(tx inbox.Tx) error {
		first, err := tx.OpenToUser(mail.UserProfile, 0, 2)
		if err != nil {
			return err
		}
		if got := messageIDs(first); !slices.Equal(got, []string{"m20001", "m20002"}) {
			return fmt.Errorf("the snapshot's first page: %v, want m20001 and m20002, in seq order, to the limit", got)
		}
		rest, err := tx.OpenToUser(mail.UserProfile, first[1].Delivery.Seq, 0)
		if err != nil {
			return err
		}
		if got := messageIDs(rest); !slices.Equal(got, []string{"m20004"}) {
			return fmt.Errorf("the snapshot past m20002: %v, want m20004 alone (a generated notice is never listed)", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := st.Write(ctx, func(tx crew.Tx) error {
		ss, err := tx.LiveSessionsOfScope(scope)
		if err != nil {
			return err
		}
		var got, want []string
		for _, s := range ss {
			got = append(got, s.ID)
		}
		for _, a := range live {
			want = append(want, a.Session)
		}
		slices.Sort(want)
		if !slices.Equal(got, want) {
			return fmt.Errorf("the host's live sessions: %v, want %v, by id", got, want)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro") // the driver the store's own golib/dao/sqlite registers
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	seen := map[string]bool{}
	for _, r := range rec.Reads() {
		steps := strings.Join(plan(t, db, r), " | ")
		switch r.Table {
		case "mail_delivery":
			seen["mail_delivery"] = true
			if !strings.Contains(steps, "USING INDEX mail_delivery_state") {
				t.Errorf("the snapshot reads the user's deliveries without mail_delivery_state, walking their history:\n  %s\n  plan: %s", r.SQL, steps)
			}
		case "crew_session":
			seen["crew_session"] = true
			if strings.Contains(steps, "sqlite_autoindex_crew_session_2 (host=?)") {
				t.Errorf("a host's live sessions are read by host, every session it ever had:\n  %s\n  plan: %s", r.SQL, steps)
			}
		}
	}
	if !seen["mail_delivery"] || !seen["crew_session"] {
		t.Fatalf("recorded reads of %v, want the snapshot's and the host's sessions'", seen)
	}
}

func messageIDs(items []mail.Item) []string {
	var out []string
	for _, it := range items {
		out = append(out, it.Message.ID)
	}
	return out
}
