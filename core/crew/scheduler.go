package crew

import (
	"context"
	"errors"
	"time"
)

// WakeNotice is the only text any waker sends: constant, with no subject, no sender, nothing a peer
// wrote (§4.7, rationale §R15).
const WakeNotice = "AutoDoc mail is waiting for you. Run: autodoc mail receive"

// The scheduler's bounds (§4.7).
const (
	wakePermits    = 4                // calls out at once, whatever the sessions
	wakeCallLimit  = 10 * time.Second // one waker call's deadline
	wakeUnanswered = 2 * time.Minute  // an accepted notice with no receive after it times out
	wakeLookBatch  = 64               // rows one look reserves from, before it looks again
)

// ChangeReachability is the human feed's change when a session stops (or starts again) being
// reachable by its waker (§4.7, §4.8): its ref is the session.
const ChangeReachability = "reachability"

// failed counts one more consecutive failure of w and backs it off from now; the failure that
// crosses unreachableAfter journals the loss in the same write, once.
func failed(tx Tx, w *Wake, now time.Time) error {
	w.Failures++
	at := now.Add(backoff(w.Failures)).UnixNano()
	w.State, w.Token, w.NextAt = WakeIdle, "", &at
	if w.Failures == unreachableAfter {
		return tx.AddInboxChange(ChangeReachability, w.Session)
	}
	return nil
}

// Outcome is what a waker's call came to.
type Outcome int

const (
	OutcomeAccepted Outcome = iota // the channel took the notice
	OutcomeUnknown                 // it may have: a deadline, a lost answer
	OutcomeFailed                  // it did not
)

// ErrWakeUnknown is a waker's call whose outcome it can't tell: classified OutcomeUnknown.
var ErrWakeUnknown = errors.New("crew: the wake's outcome is unknown")

// Waker sends the notice through one CLI's own channel (§4.7): argv or a socket write, never a
// shell string. Phase 3 brings one per CLI kind.
type Waker interface {
	Wake(ctx context.Context, ep Endpoint, notice string) error
}

func classify(err error) Outcome {
	switch {
	case err == nil:
		return OutcomeAccepted
	case errors.Is(err, ErrWakeUnknown), errors.Is(err, context.DeadlineExceeded):
		return OutcomeUnknown
	}
	return OutcomeFailed
}

// backoff is the wait after the nth consecutive failure: 5 s, 30 s, 2 min, then every 10 min.
func backoff(n int64) time.Duration {
	switch {
	case n <= 1:
		return 5 * time.Second
	case n == 2:
		return 30 * time.Second
	case n == 3:
		return 2 * time.Minute
	}
	return 10 * time.Minute
}

// expire runs mail's time-driven changes in tx, when the mail module is wired.
func (m *Manager) expire(tx Tx) error {
	if m.ender == nil {
		return nil
	}
	return m.ender.Expire(tx)
}

// RecoverRun is a daemon start's recovery (§4.7): every in_flight row an earlier run left has no
// worker any more, so it goes idle with next_at = now, to be retried at once (a duplicate notice
// names no mail). Then mail's expiry runs, so Due finds the start's work.
func (m *Manager) RecoverRun(ctx context.Context, run string) error {
	return m.store.Write(ctx, func(tx Tx) error {
		ws, err := tx.WakesInFlight()
		if err != nil {
			return err
		}
		now := tx.Now().UnixNano()
		for _, w := range ws {
			if w.DaemonRun == run {
				continue
			}
			w.State, w.Token, w.NextAt = WakeIdle, "", &now
			if err := tx.PutWake(w); err != nil {
				return err
			}
		}
		return m.expire(tx)
	})
}

// sweep ends the unanswered waits that timed out (awaiting_receive → idle, a failure, backoff),
// after mail's expiry, in one write; it answers the sessions now due.
func (m *Manager) sweep(ctx context.Context) ([]string, error) {
	var due []string
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := m.expire(tx); err != nil {
			return err
		}
		now := tx.Now()
		late, err := tx.WakesTimedOut(now.UnixNano(), wakeLookBatch)
		if err != nil {
			return err
		}
		for _, w := range late {
			if err := failed(tx, &w, now); err != nil {
				return err
			}
			if err := tx.PutWake(w); err != nil {
				return err
			}
		}
		ws, err := tx.WakesDue(now.UnixNano(), wakeLookBatch)
		for _, w := range ws {
			due = append(due, w.Session)
		}
		return err
	})
	return due, err
}

// Reserve takes session's idle → in_flight step as a compare-and-set, after mail's expiry, in one
// write (§4.7): the row must still be due, its session live under its host's epoch, and an
// endpoint registered. It answers the attempt's token and the endpoint it is for.
func (m *Manager) Reserve(ctx context.Context, session, run string) (string, Endpoint, bool, error) {
	var tok string
	var ep Endpoint
	var ok bool
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := m.expire(tx); err != nil {
			return err
		}
		w, found, err := tx.Wake(session)
		if err != nil || !found {
			return err
		}
		now := tx.Now().UnixNano()
		if w.State != WakeIdle || w.PendingGen <= w.AnnouncedGen || (w.NextAt != nil && *w.NextAt > now) {
			return nil
		}
		if _, err := guardSession(tx, session); err != nil {
			if errors.Is(err, ErrBadCredential) || errors.Is(err, ErrStaleEpoch) {
				return nil // ended or fenced meanwhile: nothing to wake
			}
			return err
		}
		e, has, err := tx.Endpoint(session)
		if err != nil || !has {
			return err // no endpoint: the host shows ✉ n instead (§4.7)
		}
		if tok, err = newID(); err != nil {
			return err
		}
		w.State, w.Token, w.AttemptGen, w.DaemonRun, w.NextAt = WakeInFlight, tok, w.PendingGen, run, nil
		ep, ok = e, true
		return tx.PutWake(w)
	})
	return tok, ep, ok, err
}

// StillCurrent reports whether an attempt may still make its call: its token holds, its session is
// live under its host's epoch, and its endpoint's generation is the one it reserved.
func (m *Manager) StillCurrent(ctx context.Context, session, token string, generation int64) (bool, error) {
	var ok bool
	err := m.store.Read(ctx, func(tx Tx) error {
		w, found, err := tx.Wake(session)
		if err != nil || !found || w.State != WakeInFlight || w.Token != token {
			return err
		}
		if _, err := guardSession(tx, session); err != nil {
			if errors.Is(err, ErrBadCredential) || errors.Is(err, ErrStaleEpoch) {
				return nil
			}
			return err
		}
		e, has, err := tx.Endpoint(session)
		ok = err == nil && has && e.Generation == generation
		return err
	})
	return ok, err
}

// Abandon gives up an attempt that never made its call: its row goes idle, its deadline kept.
func (m *Manager) Abandon(ctx context.Context, session, token string) error {
	return m.store.Write(ctx, func(tx Tx) error {
		w, found, err := tx.Wake(session)
		if err != nil || !found || w.State != WakeInFlight || w.Token != token {
			return err
		}
		w.State, w.Token = WakeIdle, ""
		return tx.PutWake(w)
	})
}

// Outcome records an attempt's outcome, only while its token holds (§4.7's table): if the session
// received meanwhile, the row is done; an accepted or unknown notice waits for a receive, at most
// wakeUnanswered; a failure backs off. A late outcome, after a reset, matches nothing.
func (m *Manager) Outcome(ctx context.Context, session, token string, o Outcome) error {
	return m.store.Write(ctx, func(tx Tx) error {
		w, found, err := tx.Wake(session)
		if err != nil || !found || w.State != WakeInFlight || w.Token != token {
			return err
		}
		now := tx.Now()
		w.Token = ""
		switch {
		case w.AnnouncedGen >= w.AttemptGen:
			w.State, w.Failures, w.NextAt = WakeIdle, 0, nil
		case o == OutcomeFailed:
			if err := failed(tx, &w, now); err != nil {
				return err
			}
		default:
			at := now.Add(wakeUnanswered).UnixNano()
			w.State, w.NextAt = WakeAwaiting, &at
		}
		return tx.PutWake(w)
	})
}

// Received is mail.receive's half of the wake (§4.7), in the receive's own write: the session has
// seen everything pending, so announced_gen catches up, failures reset, and the deadline goes, so
// new mail is reservable at once. An awaiting row is done; an in_flight one is left to its call,
// whose outcome then finds announced_gen past its attempt.
func (m *Manager) Received(tx Tx, session string) error {
	w, found, err := tx.Wake(session)
	if err != nil || !found {
		return err
	}
	if w.Failures >= unreachableAfter { // reachable again: the feed hears that too
		if err := tx.AddInboxChange(ChangeReachability, session); err != nil {
			return err
		}
	}
	w.AnnouncedGen, w.Failures, w.NextAt = w.PendingGen, 0, nil
	if w.State == WakeAwaiting {
		w.State, w.Token = WakeIdle, ""
	}
	return tx.PutWake(w)
}

// nextAt is when the scheduler must look again by time alone: the earliest wake deadline or mail
// deadline; false when nothing is waiting on a clock.
func (m *Manager) nextAt(ctx context.Context) (time.Time, bool, error) {
	var at int64
	var has bool
	err := m.store.Read(ctx, func(tx Tx) error {
		w, ok, err := tx.NextWakeAt()
		if err != nil {
			return err
		}
		at, has = w, ok
		if m.ender == nil {
			return nil
		}
		d, ok, err := m.ender.NextDeadline(tx)
		if err == nil && ok && (!has || d < at) {
			at, has = d, true
		}
		return err
	})
	return time.Unix(0, at), has, err
}
