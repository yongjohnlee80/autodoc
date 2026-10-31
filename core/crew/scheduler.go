package crew

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"syscall"
	"time"
)

// WakeNotice is the wake's text for a session that reads its mail with the CLI (MailViaCLI):
// constant, with no subject, no sender, nothing a peer wrote
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler). OpenCode's plugin wakes with its own copy.
const WakeNotice = "AutoDoc mail is waiting for you. Run: autodoc mail receive"

// WakeNoticeMCP is the wake's text for a session that reads its mail with `autodoc crew mcp`'s
// tools (MailViaMCP): constant too. Its rules allow the tools, not the shell command.
const WakeNoticeMCP = "AutoDoc mail is waiting for you: call the mail_receive tool until more is false."

// MailVia is how a session reads its mail, which decides what its wake says (NoticeFor).
type MailVia int

const (
	MailViaCLI MailVia = iota // `autodoc mail …` in its shell
	MailViaMCP                // the `autodoc crew mcp` server's tools (mail_receive, …)
)

// MailViaOf is how a session of kind reads its mail: agy's through the MCP tools its setup gives
// it, every other kind's through the CLI. A session's setup will say it per session, when a host
// turns MCP on for a kind.
func MailViaOf(kind string) MailVia {
	if kind == KindAgy {
		return MailViaMCP
	}
	return MailViaCLI
}

// NoticeFor is the one wake text for a session that reads its mail via: every waker sends it, and
// it names nothing of the mail.
func NoticeFor(via MailVia) string {
	if via == MailViaMCP {
		return WakeNoticeMCP
	}
	return WakeNotice
}

// The scheduler's bounds (docs/decisions/crew.md#wakers-and-the-wake-scheduler).
const (
	wakePermits    = 4                // calls out at once, whatever the sessions
	wakeCallLimit  = 10 * time.Second // one waker call's deadline
	wakeUnanswered = 2 * time.Minute  // an accepted notice with no receive after it times out
	wakeLookBatch  = 64               // rows one look reserves from, before it looks again
)

// ChangeReachability is the human feed's change when a session stops (or starts again) being
// reachable by its waker (docs/decisions/crew.md#wakers-and-the-wake-scheduler,
// docs/decisions/crew.md#the-human-inbox): its ref is the session.
const ChangeReachability = "reachability"

// failed counts one more consecutive failure of w and backs it off from now; the failure that
// crosses unreachableAfter journals the loss in the same write, once. A gone endpoint's failure
// counts as the one that crosses it, unless one did already.
func failed(tx Tx, w *Wake, now time.Time, gone bool) error {
	if gone && w.Failures < unreachableAfter-1 {
		w.Failures = unreachableAfter - 1
	}
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
	OutcomeGone                    // it did not, and the endpoint never will: no one is there
)

// ErrWakeUnknown is a waker's call whose outcome it can't tell: classified OutcomeUnknown.
var ErrWakeUnknown = errors.New("crew: the wake's outcome is unknown")

// ErrEndpointGone is a wake that found nothing at the session's endpoint, for certain: its socket
// gone, nothing listening there, or another process than the session's holding it. Classified
// OutcomeGone: the session is unreachable at once, not after unreachableAfter failures, until it
// registers an endpoint again. A failure that may pass (a deadline, a refusal, an error) is never
// this.
var ErrEndpointGone = errors.New("crew: the session's endpoint is gone")

// dialFailure is err, from dialing what at an endpoint, as ErrEndpointGone when it says for certain
// that no one is there (ENOENT: no socket; ECONNREFUSED: nothing listening), else a failure. Never
// an unknown: nothing was sent.
func dialFailure(what string, err error) error {
	if errors.Is(err, syscall.ENOENT) || errors.Is(err, syscall.ECONNREFUSED) {
		return fmt.Errorf("%w: dialing %s: %v", ErrEndpointGone, what, err)
	}
	return fmt.Errorf("crew: dialing %s: %v", what, err)
}

// Waker sends the notice through one CLI's own channel
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): argv or a socket write, never a shell
// string. Phase 3 brings one per CLI kind.
type Waker interface {
	Wake(ctx context.Context, ep Endpoint, notice string) error
}

func classify(err error) Outcome {
	switch {
	case err == nil:
		return OutcomeAccepted
	case errors.Is(err, ErrWakeUnknown), errors.Is(err, context.DeadlineExceeded):
		return OutcomeUnknown
	case errors.Is(err, ErrEndpointGone):
		return OutcomeGone
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

// RecoverRun is a daemon start's recovery (docs/decisions/crew.md#wakers-and-the-wake-scheduler):
// every in_flight row an earlier run left has no worker any more, so it goes idle with next_at =
// now, to be retried at once (a duplicate notice names no mail). Then mail's expiry runs, so Due
// finds the start's work.
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
// after mail's expiry, in one write; it answers the sessions now due whose endpoint is of one of
// kinds, the kinds there are wakers for.
func (m *Manager) sweep(ctx context.Context, kinds []string) ([]string, error) {
	var due, unanswered []string
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := m.expire(tx); err != nil {
			return err
		}
		now := tx.Now()
		late, err := tx.WakesTimedOut(now.UnixNano(), wakeLookBatch)
		if err != nil {
			return err
		}
		due, unanswered = nil, nil
		for _, w := range late {
			unanswered = append(unanswered, w.Session)
			if err := failed(tx, &w, now, false); err != nil {
				return err
			}
			if err := tx.PutWake(w); err != nil {
				return err
			}
		}
		ws, err := tx.WakesDue(now.UnixNano(), kinds, wakeLookBatch)
		for _, w := range ws {
			due = append(due, w.Session)
		}
		return err
	})
	if err == nil {
		for _, s := range unanswered {
			m.noteCause(s, CauseUnanswered)
		}
	}
	return due, err
}

// CauseUnanswered is the cause crew.list's why gives a failure that was a notice no mail.receive
// followed in time.
const CauseUnanswered = "no mail receive within 2m0s of the notice"

// maxCause bounds a failure's cause, as crew.list's why carries it.
const maxCause = 200

// noteCause keeps cause as session's last failed wake's, in memory alone (the store keeps the
// count, never the reason): crew.list's why names it while the session is unreachable. A daemon
// restart forgets it, and why then names no cause until the next failure.
func (m *Manager) noteCause(session, cause string) {
	if len(cause) > maxCause {
		cause = cause[:maxCause]
	}
	m.aliveMu.Lock()
	defer m.aliveMu.Unlock()
	if m.causes == nil {
		m.causes = map[string]string{}
	}
	m.causes[session] = cause
}

// cause is session's last failed wake's cause (noteCause), "" when none is known.
func (m *Manager) cause(session string) string {
	m.aliveMu.Lock()
	defer m.aliveMu.Unlock()
	return m.causes[session]
}

// Reserve takes session's idle → in_flight step as a compare-and-set, after mail's expiry, in one
// write (docs/decisions/crew.md#wakers-and-the-wake-scheduler): the row must still be due, its
// session live under its host's epoch, and an endpoint registered of one of kinds (those the caller
// has wakers for: a session whose endpoint no waker serves is watch-only, reached by crew.wait and
// the inbox, never reserved and never failed). It answers the attempt's token and the endpoint it
// is for.
func (m *Manager) Reserve(ctx context.Context, session, run string, kinds []string) (string, Endpoint, bool, error) {
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
			return err // no endpoint: the host shows ✉ n instead (docs/decisions/crew.md#wakers-and-the-wake-scheduler)
		}
		if !slices.Contains(kinds, e.Kind) {
			return nil // no waker for its kind: watch-only
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

// Outcome records an attempt's outcome, only while its token holds
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): if the session received meanwhile, the
// row is done; an accepted or unknown notice waits for a receive, at most wakeUnanswered; a failure
// backs off. A late outcome, after a reset, matches nothing.
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
		case o == OutcomeFailed || o == OutcomeGone:
			if err := failed(tx, &w, now, o == OutcomeGone); err != nil {
				return err
			}
		default:
			at := now.Add(wakeUnanswered).UnixNano()
			w.State, w.NextAt = WakeAwaiting, &at
		}
		return tx.PutWake(w)
	})
}

// Received is mail.receive's half of the wake
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler), in the receive's own write: the session
// has seen everything pending, so announced_gen catches up, failures reset, and the deadline goes,
// so new mail is reservable at once. An awaiting row is done; an in_flight one is left to its call,
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

// Settled is a withdrawn send's half of the wake (mail's fire), in its write: what was pending is
// no longer due (announced_gen catches up, and an awaiting row is done, its wait's deadline with
// it). Unlike a receive it is no sign the session can be woken: its failures and a backoff stay, so
// failed rings still add up to unreachable, and no reachability change is told.
func (m *Manager) Settled(tx Tx, session string) error {
	w, found, err := tx.Wake(session)
	if err != nil || !found {
		return err
	}
	w.AnnouncedGen = w.PendingGen
	if w.State == WakeAwaiting {
		w.State, w.Token, w.NextAt = WakeIdle, "", nil
	}
	return tx.PutWake(w)
}

// nextAt is when the scheduler must look again by time alone: the earliest wake deadline or mail
// deadline (of a row due by kinds, as sweep reads them); false when nothing is waiting on a clock.
func (m *Manager) nextAt(ctx context.Context, kinds []string) (time.Time, bool, error) {
	var at int64
	var has bool
	err := m.store.Read(ctx, func(tx Tx) error {
		w, ok, err := tx.NextWakeAt(kinds)
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
