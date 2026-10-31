package crew

import "context"

// Fire rings profile's live session's doorbell now, for a send that must be fired or nothing (the
// user's send from a window, inbox.send's fire, docs/decisions/crew.md#the-human-inbox): fired is
// true once a wake for the mail pending now was accepted, or the session received it. It is false,
// with nothing left to undo here, when no wake can carry it now: no live session, no endpoint or
// waker for its kind, a backoff, an earlier wake still unanswered, the waker's failure or an
// unknown outcome, or ctx's end (the caller's bound).
//
// The attempt is the scheduler's own: reserved, permitted and recorded as the loop's are, so it
// never races the loop's attempt for the same row; when the loop holds the row already, Fire waits
// for that attempt's end. Before the scheduler exists nothing fires.
func (m *Manager) Fire(ctx context.Context, profile string) (bool, error) {
	s := m.sched.Load()
	if s == nil {
		return false, nil
	}
	return s.fire(ctx, profile)
}

// fireRead is the session and wake row Fire works from.
func (s *Scheduler) fireRead(ctx context.Context, profile string) (session string, w Wake, ok bool, err error) {
	err = s.m.store.Read(ctx, func(tx Tx) error {
		live, found, err := tx.LiveSession(profile)
		if err != nil || !found {
			return err
		}
		w, ok, err = tx.Wake(live.ID)
		session = live.ID
		return err
	})
	return session, w, ok, err
}

func (s *Scheduler) fire(ctx context.Context, profile string) (bool, error) {
	ch, stop := s.Watch() // before the first read, so a change in between still signals
	defer stop()
	session, w, ok, err := s.fireRead(ctx, profile)
	if err != nil || !ok {
		return false, err
	}
	gen := w.PendingGen // the mail pending now: the caller's, committed before
	for {
		switch {
		case w.AnnouncedGen >= gen:
			return true, nil // received
		case w.State == WakeAwaiting && w.AttemptGen >= gen:
			return true, nil // the loop's attempt for it was accepted (or may have been)
		case w.State == WakeAwaiting:
			return false, nil // an earlier wake is unanswered: no new one until it ends
		case w.State == WakeIdle && w.AttemptGen >= gen:
			return false, nil // an attempt for it failed
		case w.State == WakeIdle:
			if fired, tried, err := s.fireNow(ctx, session); tried || err != nil {
				return fired, err
			}
			// every permit is taken: a returning call pokes
		}
		// in flight (the loop's attempt, for this mail or an earlier one, whose end decides), or
		// waiting for a permit
		select {
		case <-ctx.Done():
			return false, nil
		case <-ch:
		}
		again, next, found, err := s.fireRead(ctx, profile)
		if err != nil || !found || again != session {
			return false, err // the session ended, or another took its place: not this mail's
		}
		w = next
	}
}

// fireNow reserves session's attempt under a permit and makes its call here: tried is false only
// when no permit is free. The call is bounded by ctx; its outcome is recorded whatever ctx does
// since, so the row never stays in flight for a caller that stopped waiting.
func (s *Scheduler) fireNow(ctx context.Context, session string) (fired, tried bool, err error) {
	select {
	case s.permits <- struct{}{}:
	default:
		return false, false, nil
	}
	s.stopMu.Lock()
	if s.stopped {
		s.stopMu.Unlock()
		<-s.permits
		return false, true, nil // the daemon is stopping: nothing rings
	}
	s.wg.Add(1)
	s.stopMu.Unlock()
	tok, ep, reserved, err := s.m.Reserve(ctx, session, s.run, s.served())
	if err != nil || !reserved {
		<-s.permits
		s.wg.Done()
		return false, true, err // not due (a backoff), no endpoint, no waker: nothing to ring now
	}
	return s.attemptWith(ctx, context.WithoutCancel(ctx), session, tok, ep) == OutcomeAccepted, true, nil
}
