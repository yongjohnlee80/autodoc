package sqlitecrewmail_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// historyScan is a plan step that reads a table's history rather than what is due: a full scan of
// the tables the store keeps every row of (an index scan is a full scan too), a recipient's
// deliveries searched by recipient alone, every one it ever had, or a host's fallback jobs by host
// alone, its done ones too.
var historyScan = regexp.MustCompile(`^SCAN (mail_delivery|mail_message|mail_fallback)\b|` +
	`^SEARCH mail_delivery USING INDEX sqlite_autoindex_mail_delivery_2 \(recipient=\?\)$|` +
	`^SEARCH mail_fallback USING INDEX mail_fallback_host \(host=\?\)$`)

// TestTheWritePathReadsWhatIsDueNotTheHistory (docs/decisions/crew.md#the-crew-and-mail-tables):
// every mail wait, host wait and scheduler sweep reads inside its write, which every other write
// waits behind, so each read there must cost what is due, never what the store keeps. With twenty
// live agents, ten ended, twenty thousand handled messages and two hundred done fallback jobs, each
// of those reads is run as the store renders it, and SQLite's plan for it (no statistics, as a
// daemon's store has none) never scans the history. The few due items are answered as before: the
// unhandled in seq order, the earliest expiry, the expiring, the fallback offers oldest first. An
// ended session's wake row is gone, so the wake reads, which scan crew_wake, read the live crew.
func TestTheWritePathReadsWhatIsDueNotTheHistory(t *testing.T) {
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
	// every agent has a wake row; then ten agents' sessions end, and their rows go with them
	if err := st.Write(ctx, func(tx crew.Tx) error {
		for _, a := range agents {
			if err := m.RaisePending(tx, a.Profile); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, a := range agents[20:] {
		if err := m.Exit(ctx, scope, "run-a", e, a.Session, crew.EndedExit); err != nil {
			t.Fatal(err)
		}
	}
	live := agents[:20]
	if err := st.Write(ctx, func(tx crew.Tx) error {
		for _, a := range agents[20:] {
			if _, ok, err := tx.Wake(a.Session); err != nil || ok {
				return fmt.Errorf("an ended session's wake row: found %v, %v; want it gone", ok, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	body := strings.Repeat("b", 4096)
	now := clock.Now().UnixNano()
	if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
		if err := tx.TakeFallbacks(scope, e); err != nil {
			return err
		}
		for i := range 20000 {
			id := fmt.Sprintf("m%05d", i)
			expires := int64(i + 1) // every handled request has an expiry long past
			if err := tx.PutMessage(mail.Message{ID: id, Sender: live[(i+1)%len(live)].Profile, IdemKey: id, Kind: mail.KindRequest,
				Subject: "s", Body: body, Created: int64(i + 1), Expires: &expires}); err != nil {
				return err
			}
			if err := tx.PutDelivery(mail.Delivery{ID: "d" + id, Message: id, Recipient: live[i%len(live)].Profile,
				Session: live[i%len(live)].Session, Seq: int64(i/len(live) + 1), State: mail.Acked}); err != nil {
				return err
			}
		}
		for i := range 200 {
			done := int64(i + 1)
			if err := tx.PutFallbackJob(mail.FallbackJob{ID: fmt.Sprintf("j%03d", i), Delivery: fmt.Sprintf("dm%05d", i),
				Message: fmt.Sprintf("m%05d", i), Recipient: live[0].Profile, Host: scope, State: mail.JobDone, Created: int64(i + 1),
				DoneAt: &done}); err != nil {
				return err
			}
		}
		// what is due: two queued to agent00, the later one expiring soonest; one open fallback job
		soon, later := now+int64(time.Minute), now+int64(time.Hour)
		for i, d := range []struct {
			id      string
			seq     int64
			expires int64
		}{{"due-a", 9001, later}, {"due-b", 9000, soon}} {
			if err := tx.PutMessage(mail.Message{ID: d.id, Sender: live[1].Profile, IdemKey: d.id, Kind: mail.KindRequest,
				Subject: "s", Body: "b", Created: now + int64(i), Expires: &d.expires}); err != nil {
				return err
			}
			if err := tx.PutDelivery(mail.Delivery{ID: "d" + d.id, Message: d.id, Recipient: live[0].Profile, Seq: d.seq,
				State: mail.Queued}); err != nil {
				return err
			}
		}
		return tx.PutFallbackJob(mail.FallbackJob{ID: "j-open", Delivery: "ddue-a", Message: "due-a", Recipient: live[0].Profile,
			Host: scope, State: mail.JobOpen, Created: now})
	}); err != nil {
		t.Fatal(err)
	}

	// every read the write path makes, as the store renders it
	rec.Reset()
	if err := st.Mail().Write(ctx, func(tx mail.Tx) error {
		if ds, err := tx.Unhandled(live[0].Profile); err != nil || len(ds) != 2 || ds[0].ID != "ddue-b" || ds[1].ID != "ddue-a" {
			return fmt.Errorf("Unhandled: %v, %v; want ddue-b then ddue-a, in seq order", ids(ds), err)
		}
		if at, has, err := tx.NextDeadline(); err != nil || !has || at != now+int64(time.Minute) {
			return fmt.Errorf("NextDeadline: %d, %v, %v; want the earliest expiry of what is unhandled, %d", at, has, err, now+int64(time.Minute))
		}
		if ds, err := tx.Expiring(now+int64(2*time.Minute), 100); err != nil || len(ds) != 1 || ds[0].ID != "ddue-b" {
			return fmt.Errorf("Expiring: %v, %v; want ddue-b alone", ids(ds), err)
		}
		if _, err := tx.LeasesDue(now, 100); err != nil {
			return err
		}
		if _, err := tx.Eligible(live[0].Profile, live[0].Session, now, 1); err != nil {
			return err
		}
		if _, err := tx.BoundTo(live[3].Session); err != nil {
			return err
		}
		if _, err := tx.FallbackDue(now, now, 100); err != nil {
			return err
		}
		if _, _, err := tx.NextFallbackAt(int64(time.Minute), int64(10*time.Minute)); err != nil {
			return err
		}
		if js, err := tx.FallbackOffers(scope, now, 50); err != nil || len(js) != 1 || js[0].ID != "j-open" {
			return fmt.Errorf("FallbackOffers: %d jobs, %v; want j-open alone (the done ones are no offer)", len(js), err)
		}
		_, _, err := tx.NextFallbackLease(scope)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	mailReads := rec.Reads()
	if err := st.Write(ctx, func(tx crew.Tx) error {
		if _, err := tx.WakesDue(now, []string{"agy"}, 10); err != nil {
			return err
		}
		if _, err := tx.WakesTimedOut(now, 10); err != nil {
			return err
		}
		if _, err := tx.WakesInFlight(); err != nil {
			return err
		}
		_, _, err := tx.NextWakeAt([]string{"agy"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro") // the driver the store's own golib/dao/sqlite registers
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if len(mailReads) < 10 {
		t.Fatalf("recorded %d reads of the mail write path, want each one's", len(mailReads))
	}
	for _, r := range rec.Reads() {
		steps := plan(t, db, r)
		for _, step := range steps {
			if historyScan.MatchString(step) {
				t.Errorf("a write-path read of %s walks its history (%s):\n  %s\n  plan: %s", r.Table, step, r.SQL, strings.Join(steps, " | "))
			}
		}
	}
	var wakes int
	if err := db.QueryRow(`SELECT count(*) FROM crew_wake`).Scan(&wakes); err != nil || wakes != len(live) {
		t.Fatalf("crew_wake holds %d rows (%v), want the %d live sessions': an ended session's row goes at its end", wakes, err, len(live))
	}
}

// plan is SQLite's query plan for r, one step a line, as EXPLAIN QUERY PLAN reports it.
func plan(t *testing.T, db *sql.DB, r sqlitecrewmail.Statement) []string {
	t.Helper()
	rows, err := db.Query("EXPLAIN QUERY PLAN "+r.SQL, r.Args...)
	if err != nil {
		t.Fatalf("the plan of %s: %v", r.SQL, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return slices.Clip(out)
}

func ids(ds []mail.Delivery) []string {
	var out []string
	for _, d := range ds {
		out = append(out, d.ID)
	}
	return out
}
