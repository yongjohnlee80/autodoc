package inbox

import (
	"context"
	"fmt"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/mail"
)

// The feed's limits (§4.8).
const (
	MaxWait     = 25 * time.Second // the longest inbox.watch waits
	changesMax  = 256              // changes one watch returns; the next takes the rest
	listMax     = 200              // items one list returns
	snapshotMax = 500              // open items one snapshot page carries; Next pages in the rest
	// pageBytes bounds the text one snapshot page or list answer carries (subjects and bodies, plus
	// itemOverhead each), well under the transport's 4 MiB message: 65 bodies of mail's 64 KiB
	// already pass it. A page always carries at least one item, so paging makes progress.
	pageBytes    = 2 << 20
	itemOverhead = 512 // an item's ids, names, states and times, as the wire carries them
)

// List filters.
const (
	ListAll           = ""
	ListUndeliverable = "undeliverable"
)

// Manager is the inbox module.
type Manager struct {
	store Store
	mail  Mail
	feed  *Feed
}

// New is the inbox module over store, answering through mail, its watches woken by feed. A nil
// store builds a Manager that only answers Name, Verbs and Errors, as --contracts needs.
func New(store Store, mail Mail, feed *Feed) *Manager {
	if feed == nil {
		feed = NewFeed()
	}
	return &Manager{store: store, mail: mail, feed: feed}
}

// Item is a message to the human, or a notice nobody received, with its delivery and its sender
// named as a window shows it.
type Item struct {
	mail.Item
	Codename, Scope string // the sender's
}

// Attention is an agent waiting on the human, named as a window shows it.
type Attention struct {
	crew.Attention
	Codename, Scope string // the session's
}

// Snapshot is what the human has open: requests to answer, reports not yet read, and the
// attention agents raised. It's bounded: a page carries at most snapshotMax items, and Next, when
// not 0, is where the rest continues (Watch's page). Continuation pages carry items only.
type Snapshot struct {
	Items     []Item
	Attention []Attention
	Next      int64
}

// name is p's codename and scope, as a window shows it; a profile no longer kept is shown by id.
func name(tx Tx, profile string) (string, string, error) {
	p, ok, err := tx.Profile(profile)
	if err != nil || !ok {
		return profile, "", err
	}
	return p.Codename, p.Scope, nil
}

func named(tx Tx, items []mail.Item) ([]Item, error) {
	out := make([]Item, 0, len(items))
	for _, it := range items {
		c, sc, err := name(tx, it.Message.Sender)
		if err != nil {
			return nil, err
		}
		out = append(out, Item{Item: it, Codename: c, Scope: sc})
	}
	return out, nil
}

// Watched is one inbox.watch's answer: the changes after the cursor, or a snapshot when the
// cursor can't be resumed; Cursor resumes the next watch.
type Watched struct {
	Cursor   int64
	Snapshot *Snapshot
	Changes  []Change
}

// Watch is inbox.watch (§4.8): the changes after cursor, waiting up to wait for one; or, for
// cursor 0, a cursor older than the oldest retained change, or one past the newest (another
// store's), a snapshot with the cursor taken in the same read, so the two agree. It never leases.
// A journal that never had a row has nothing to show, so cursor 0 on it waits like any other:
// otherwise its snapshot's cursor, 0 again, would answer every next watch at once.
//
// A snapshot is bounded and lossless. Its first page carries at most snapshotMax open items, the
// attention, and Next when more remain. The caller asks for the rest with page = Next, passing the
// snapshot's cursor. A page answers at once, with items only and that cursor echoed, and the
// caller then watches from the first page's cursor. An item answered between pages arrives as its
// change, and one sent between pages appears both in a page and as its change; the caller keys
// items by message.
func (m *Manager) Watch(ctx context.Context, cursor int64, wait time.Duration, page int64) (Watched, error) {
	if page > 0 {
		return m.snapshotPage(ctx, cursor, page)
	}
	wait = min(max(wait, 0), MaxWait)
	heard, stop := m.feed.Watch() // before the read: a commit after it is heard
	defer stop()
	// NOTE: timed by the caller's long-poll: inbox.watch answers "nothing new" once wait passes, so
	// a host's loop comes back with its cursor; the feed and ctx end the wait sooner.
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		var out Watched
		err := m.store.Read(ctx, func(tx Tx) error {
			oldest, newest, err := tx.ChangeBounds()
			if err != nil {
				return err
			}
			if (cursor == 0 && newest > 0) || cursor > newest || (oldest > 0 && cursor+1 < oldest) {
				snap, err := snapshot(tx)
				out = Watched{Cursor: newest, Snapshot: &snap}
				return err
			}
			ch, err := tx.Changes(cursor, changesMax)
			out = Watched{Cursor: cursor, Changes: ch}
			if len(ch) > 0 {
				out.Cursor = ch[len(ch)-1].Seq
			}
			return err
		})
		if err != nil || out.Snapshot != nil || len(out.Changes) > 0 {
			return out, err
		}
		select {
		case <-ctx.Done():
			return Watched{}, ctx.Err()
		case <-timer.C:
			out.Changes = []Change{}
			return out, nil
		case <-heard:
		}
	}
}

// snapshotPage is a snapshot's continuation: the open items past after, at once, with the
// snapshot's cursor echoed.
func (m *Manager) snapshotPage(ctx context.Context, cursor, after int64) (Watched, error) {
	out := Watched{Cursor: cursor, Changes: []Change{}}
	err := m.store.Read(ctx, func(tx Tx) error {
		items, next, err := openItems(tx, after)
		out.Snapshot = &Snapshot{Items: items, Attention: []Attention{}, Next: next}
		return err
	})
	return out, err
}

// openItems is one page of the human's open items past after, and the page's Next.
func openItems(tx Tx, after int64) ([]Item, int64, error) {
	user, ok, err := tx.ActiveProfile(mail.GlobalScope, mail.UserCodename)
	if err != nil || !ok {
		return []Item{}, 0, err
	}
	open, err := tx.OpenToUser(user.ID, after, snapshotMax+1)
	if err != nil {
		return nil, 0, err
	}
	page := fitPage(open, snapshotMax)
	next := int64(0)
	if len(page) < len(open) {
		next = page[len(page)-1].Delivery.Seq
	}
	items, err := named(tx, page)
	return items, next, err
}

// fitPage is the first of items that fit one answer: at most limit of them, and their text within
// pageBytes, but never none while there is one.
func fitPage(items []mail.Item, limit int) []mail.Item {
	size := 0
	for i, it := range items {
		size += len(it.Message.Subject) + len(it.Message.Body) + itemOverhead
		if i == limit || (i > 0 && size > pageBytes) {
			return items[:i]
		}
	}
	return items
}

func snapshot(tx Tx) (Snapshot, error) {
	snap := Snapshot{Items: []Item{}, Attention: []Attention{}}
	var err error
	if snap.Items, snap.Next, err = openItems(tx, 0); err != nil {
		return Snapshot{}, err
	}
	raised, err := tx.Raised()
	if err != nil {
		return Snapshot{}, err
	}
	for _, a := range raised {
		c, sc := a.Session, ""
		if s, ok, err := tx.Session(a.Session); err != nil {
			return Snapshot{}, err
		} else if ok {
			if c, sc, err = name(tx, s.Profile); err != nil {
				return Snapshot{}, err
			}
		}
		snap.Attention = append(snap.Attention, Attention{Attention: a, Codename: c, Scope: sc})
	}
	return snap, nil
}

// List is inbox.list: the human's history, newest first (filter ""), or the generated notices
// nobody could receive (filter "undeliverable"). before pages the history by seq.
func (m *Manager) List(ctx context.Context, filter string, before int64, limit int) ([]Item, error) {
	limit = min(max(limit, 1), listMax)
	out := []Item{}
	// a history page is bounded by bytes as a snapshot is: the caller pages on with before = the
	// last item's seq
	err := m.store.Read(ctx, func(tx Tx) error {
		var items []mail.Item
		switch filter {
		case ListAll:
			user, ok, err := tx.ActiveProfile(mail.GlobalScope, mail.UserCodename)
			if err != nil || !ok {
				return err
			}
			if items, err = tx.ToUser(user.ID, before, limit); err != nil {
				return err
			}
		case ListUndeliverable:
			var err error
			if items, err = tx.FailedNotices(limit); err != nil {
				return err
			}
		default:
			return fmt.Errorf("%w: filter %q is not \"\" or %q", ErrInvalid, filter, ListUndeliverable)
		}
		var err error
		out, err = named(tx, fitPage(items, limit))
		return err
	})
	return out, err
}

// Focused is the session inbox.focus asked to raise, and its current owner: the host whose
// terminal shows it.
type Focused struct {
	Session string
	Owner   string
}

// Focus is inbox.focus (§4.8): it resolves session's CURRENT owner at call time (its live
// session's host, or the owner of the attention it raised), never a toast's stale field, and
// journals a focus change that owner raises the agent for. An ended session is ErrNotFound.
func (m *Manager) Focus(ctx context.Context, session string) (Focused, error) {
	var out Focused
	err := m.store.Write(ctx, func(tx Tx) error {
		owner := ""
		if s, ok, err := tx.Session(session); err != nil {
			return err
		} else if ok && s.Ended == nil {
			owner = s.Host
		}
		if owner == "" {
			a, ok, err := tx.Attention(session)
			if err != nil {
				return err
			}
			if !ok || a.Cleared != nil {
				return fmt.Errorf("%w: session %s", ErrNotFound, session)
			}
			owner = a.Owner
		}
		out = Focused{Session: session, Owner: owner}
		return tx.AddInboxChange(ChangeFocus, session)
	})
	return out, err
}

// Reply is inbox.reply: mail's Answer.
func (m *Manager) Reply(ctx context.Context, message, body, key string) (mail.Message, error) {
	return m.mail.Answer(ctx, message, body, key)
}

// Dismiss is inbox.dismiss: mail's Dismiss.
func (m *Manager) Dismiss(ctx context.Context, message string) error {
	return m.mail.Dismiss(ctx, message)
}

// Ack is inbox.ack: mail's Read.
func (m *Manager) Ack(ctx context.Context, message string) error { return m.mail.Read(ctx, message) }
