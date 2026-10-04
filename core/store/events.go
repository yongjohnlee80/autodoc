package store

import (
	"context"
	"errors"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// EventsKept is how many events the log keeps: a client further behind takes a fresh snapshot.
const EventsKept = 1000

// ErrEventsExpired is a cursor older than the retained log, or newer than its head (another
// store's): the client takes a snapshot and resumes from the head.
var ErrEventsExpired = errors.New("store: the event cursor is outside the retained log")

// AppendEvent logs one configuration or lifecycle change and prunes the log to EventsKept.
func (s *Store) AppendEvent(ctx context.Context, e Event) (int64, error) {
	var seq int64
	err := s.Write(ctx, func(tx *Tx) error {
		var err error
		seq, err = tx.t.events.On(tx.tx).Set(EventKind, e.Kind).Set(EventWorkspace, e.Workspace).
			Set(EventClient, e.Client).Set(EventDetail, e.Detail).Set(EventAt, time.Now().Unix()).Insert()
		if err != nil {
			return err
		}
		if seq > EventsKept {
			return tx.t.events.On(tx.tx).WithPredicate(dao.Lte(`"event"."seq"`, seq-EventsKept)).Delete()
		}
		return nil
	})
	return seq, err
}

// Events is the log after cursor since, at most limit, oldest first: the events, the cursor to
// resume from, and whether more follow. A negative since asks for the head alone. since below the
// oldest retained event (less one) or above the head is ErrEventsExpired.
func (s *Store) Events(ctx context.Context, since int64, limit int) ([]Event, int64, bool, error) {
	var out []Event
	var cursor int64
	var more bool
	err := s.Read(ctx, func(tx *Tx) error {
		head, oldest, err := s.eventBounds(tx)
		if err != nil {
			return err
		}
		cursor = head
		if since < 0 {
			return nil
		}
		if since > head || (oldest > 0 && since < oldest-1) {
			return ErrEventsExpired
		}
		rows, err := tx.t.events.On(tx.tx).WithPredicate(dao.Gt(`"event"."seq"`, since)).
			OrderBy(dao.Asc(BySeq)).Limit(uint64(limit + 1)).Select()
		if err != nil {
			return err
		}
		if len(rows) > limit {
			rows, more = rows[:limit], true
		}
		cursor = since
		for _, r := range rows {
			out = append(out, *r)
			cursor = r.Seq
		}
		return nil
	})
	return out, cursor, more, err
}

// eventBounds is the newest event's seq and the oldest retained one's; 0 when there is none.
// Pruning never takes the newest, so the newest row is the head.
func (s *Store) eventBounds(tx *Tx) (head, oldest int64, err error) {
	rows, err := tx.t.events.On(tx.tx).OrderBy(dao.Asc(BySeq)).Limit(1).Select(EventSeq)
	if err != nil {
		return 0, 0, err
	}
	if len(rows) == 1 {
		oldest = rows[0].Seq
	}
	last, err := tx.t.events.On(tx.tx).OrderBy(dao.Desc(BySeq)).Limit(1).Select(EventSeq)
	if err != nil {
		return 0, 0, err
	}
	if len(last) == 1 {
		head = last[0].Seq
	}
	return head, oldest, nil
}
