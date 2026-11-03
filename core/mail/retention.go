package mail

import (
	"context"
	"time"
)

// HANDLED MAIL IS KEPT FOR A WINDOW (docs/decisions/crew.md#handled-mail-is-kept-for-a-window): a
// message every delivery of which was handled (acked, read, answered, dismissed, expired) more than
// the window ago is deleted, with its deliveries and fallback jobs, and so are the human feed's
// journal rows older than the window but its newest. An unread report or an open request is never
// deleted, however old. The sweep is bounded batches, each its own short write, so no write waits
// behind it for long.

const (
	// DefaultKeepHandled is how long handled mail is kept: 30 days.
	DefaultKeepHandled = 30 * 24 * time.Hour
	// MinKeepHandled is the shortest window a configuration may set.
	MinKeepHandled = 24 * time.Hour
	// sweepBatch bounds one write of a sweep: messages deleted, or journal rows pruned.
	sweepBatch = 200
)

// Swept is what one sweep deleted.
type Swept struct {
	Messages int // messages, with their deliveries and fallback jobs
	Changes  int // the human feed's journal rows
}

// Sweep deletes what was handled more than keep ago (a keep below MinKeepHandled is
// MinKeepHandled), in batches each one write that reads at most sweepBatch messages, oldest first,
// and only those created before the window, until none is left or ctx ends. What it deleted
// before an error or ctx's end stays deleted, and is counted.
func (m *Manager) Sweep(ctx context.Context, keep time.Duration) (Swept, error) {
	keep = max(keep, MinKeepHandled)
	var out Swept
	var cutoff int64
	var after MessageCursor
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var ids []string
		read := 0
		err := m.store.Write(ctx, func(tx Tx) error {
			if cutoff == 0 {
				cutoff = tx.Now().Add(-keep).UnixNano() // one cutoff for the whole sweep
			}
			var err error
			if ids, after, read, err = tx.HandledBefore(cutoff, after, sweepBatch); err != nil {
				return err
			}
			for _, id := range ids {
				if err := tx.DeleteMessage(id); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return out, err
		}
		out.Messages += len(ids)
		if read < sweepBatch { // a short page: no older message is left to read
			break
		}
	}
	for {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		var n int
		err := m.store.Write(ctx, func(tx Tx) error {
			var err error
			n, err = tx.PruneInbox(cutoff, sweepBatch)
			return err
		})
		if err != nil {
			return out, err
		}
		out.Changes += n
		if n < sweepBatch {
			return out, nil
		}
	}
}
