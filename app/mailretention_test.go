package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/crewmail/sqlitecrewmail"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// The daemon sweeps handled mail as it starts (docs/decisions/crew.md#handled-mail-is-kept-for-a-window):
// a message handled longer ago than mail.keep_handled is deleted with its delivery, and an unread one,
// however old, is kept. The config's window and its least are core/mail's own.
func TestTheDaemonSweepsHandledMailAsItStarts(t *testing.T) {
	if config.DefaultKeepHandled != mail.DefaultKeepHandled || config.MinKeepHandled != mail.MinKeepHandled {
		t.Fatalf("the config's mail window %v (least %v) is not core/mail's %v (least %v)",
			config.DefaultKeepHandled, config.MinKeepHandled, mail.DefaultKeepHandled, mail.MinKeepHandled)
	}
	ctx := context.Background()
	dir := short(t)
	state, sock := filepath.Join(dir, "state"), filepath.Join(dir, "s.sock")
	cfg := writeConfig(t, sock, state, "")
	if err := os.MkdirAll(state, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(state, config.StoreName)
	seed := func(fn func(tx mail.Tx) error) {
		t.Helper()
		db, err := store.Open(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		if err := sqlitecrewmail.New(db).Mail().Write(ctx, fn); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-40 * 24 * time.Hour).UnixNano()
	seed(func(tx mail.Tx) error {
		for i, m := range []struct {
			id, state string
			done      *int64
		}{{"handled-long-ago", mail.Acked, &old}, {"unread-long-ago", mail.Queued, nil}} {
			if err := tx.PutMessage(mail.Message{ID: m.id, Sender: "p-sender", IdemKey: m.id, Digest: []byte{1},
				Kind: mail.KindMessage, Body: "b", State: mail.MessageOpen, Created: old}); err != nil {
				return err
			}
			if err := tx.PutDelivery(mail.Delivery{ID: "d-" + m.id, Message: m.id, Recipient: "p-recipient",
				Seq: int64(i + 1), State: m.state, DoneAt: m.done}); err != nil {
				return err
			}
		}
		return nil
	})
	d := start(t, cfg, sock)
	eventually(t, "the start's sweep in the log", func() bool { return strings.Contains(d.out.String(), "mail.swept") })
	d.stop()
	left := map[string]bool{}
	seed(func(tx mail.Tx) error {
		for _, id := range []string{"handled-long-ago", "unread-long-ago"} {
			_, ok, err := tx.Message(id)
			if err != nil {
				return err
			}
			left[id] = ok
		}
		return nil
	})
	if left["handled-long-ago"] || !left["unread-long-ago"] {
		t.Fatalf("after the start's sweep: %v; want the handled message deleted, the unread one kept", left)
	}
}
