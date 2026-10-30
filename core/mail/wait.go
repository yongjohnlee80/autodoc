package mail

import (
	"context"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// Ready is mail.wait's answer: the caller has mail it may receive, or the wait timed out.
type Ready struct {
	Ready    bool
	TimedOut bool
}

// Wait is mail.wait (ADR 1793271500 §4.6): it answers as soon as credential's session has eligible
// mail, or at wait (at most crew.MaxWait). It registers on crew's wake signal before it checks, so a
// send committed in between is heard. Each check is one write (the credential, then expire, then
// eligibility); between checks it holds no transaction, and it checks again only when the wake
// signal fires or mail's next deadline passes (a lapsing lease makes a delivery eligible again).
func (m *Manager) Wait(ctx context.Context, credential string, wait time.Duration) (Ready, error) {
	wait = min(max(wait, 0), crew.MaxWait)
	heard, stop := m.crew.Watch()
	defer stop()
	// NOTE: timed by the caller's long-poll: mail.wait answers "timed out" once wait passes, so the
	// agent comes back; a send, a lapsing lease or ctx ends the wait sooner.
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		var ready bool
		next := time.Duration(-1)
		err := m.store.Write(ctx, func(tx Tx) error {
			ready, next = false, -1
			s, err := m.crew.Authenticate(tx, credential)
			if err != nil {
				return err
			}
			if err := m.expire(tx); err != nil {
				return err
			}
			now := tx.Now().UnixNano()
			ds, err := tx.Eligible(s.Profile, s.ID, now, 1)
			if err != nil {
				return err
			}
			ready = len(ds) > 0
			at, has, err := tx.NextDeadline()
			if err == nil && has {
				next = max(time.Duration(at-now), 0)
			}
			return err
		})
		if err != nil || ready {
			return Ready{Ready: ready}, err
		}
		if timedOut, err := crew.WaitFor(ctx, heard, deadline.C, next); err != nil || timedOut {
			return Ready{TimedOut: timedOut}, err
		}
	}
}
