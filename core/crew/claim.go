package crew

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"
)

// leaseTTL is how long a host's ownership lasts unrenewed (§4.3); hosts renew every 30 s.
const leaseTTL = 90 * time.Second

// free reports whether h's owner may be replaced at now: it released the scope, its lease lapsed,
// or its process is known to be dead. An unknown process is not dead: the lease decides.
func free(h Host, now time.Time) bool {
	return len(h.Incarnation) == 0 || now.Sub(time.Unix(0, h.Renewed)) > leaseTTL || procDead(h.Proc)
}

// scopeKinds are the scopes a host may claim, by prefix; "global" (the human's) is never claimed.
var scopeKinds = map[string]string{"autovim:": "autovim", "nvim:": "nvim", "autodoc:": "autodoc"}

// kindOf is scope's kind, or "" for an id no host may claim.
func kindOf(scope string) string {
	for prefix, kind := range scopeKinds {
		if name, ok := strings.CutPrefix(scope, prefix); ok && name != "" && !strings.ContainsAny(name, " \t\n") {
			return kind
		}
	}
	return ""
}

// checkClaim refuses a scope id no host may claim, or an empty run token.
func checkClaim(scope, run string) error {
	if kindOf(scope) == "" {
		return fmt.Errorf("%w: scope %q is not autovim:, nvim: or autodoc:<name>", ErrInvalid, scope)
	}
	if run == "" {
		return fmt.Errorf("%w: an empty run token", ErrInvalid)
	}
	return nil
}

// Claim claims scope for run's process, or reattaches it when that process already holds it
// (§4.3). One write:
//   - the holder is this run and proc: a reattach, the same epoch (a daemon reconnect, or a retry
//     of a claim whose answer was lost);
//   - the holder is free: a claim, epoch + 1; every live session of the scope ends ('reclaim');
//   - otherwise ErrScopeBusy.
//
// A scope adoption has ended ('gone') or is moving ('adopting') is never claimed again.
func (m *Manager) Claim(ctx context.Context, scope, run, proc string) (int64, error) {
	return m.claim(ctx, scope, run, proc, false)
}

// Takeover claims scope as Claim does, but from a holder that is not free, after the user agreed
// (§4.3): the old owner is fenced by the new epoch, and its sessions end ('takeover').
func (m *Manager) Takeover(ctx context.Context, scope, run, proc string) (int64, error) {
	return m.claim(ctx, scope, run, proc, true)
}

func (m *Manager) claim(ctx context.Context, scope, run, proc string, force bool) (int64, error) {
	if err := checkClaim(scope, run); err != nil {
		return 0, err
	}
	var epoch int64
	err := m.store.Write(ctx, func(tx Tx) error {
		now := tx.Now()
		sc, scoped, err := tx.Scope(scope)
		if err != nil {
			return err
		}
		if scoped && sc.State != ScopeLive {
			return ErrScopeBusy
		}
		h, held, err := tx.Host(scope)
		if err != nil {
			return err
		}
		if held && len(h.Incarnation) > 0 && bytes.Equal(h.Incarnation, sha(run)) && h.Proc == proc {
			h.Renewed = now.UnixNano() // a reattach: the same process, the same epoch
			epoch = h.Epoch
			return tx.PutHost(h)
		}
		if held && !force && !free(h, now) {
			return ErrScopeBusy
		}
		if !scoped {
			if err := tx.PutScope(Scope{ID: scope, Kind: kindOf(scope), State: ScopeLive, Created: now.UnixNano()}); err != nil {
				return err
			}
		}
		why := EndedReclaim
		if force {
			why = EndedTakeover
		}
		if err := m.endScopeSessions(tx, scope, why); err != nil {
			return err
		}
		epoch = h.Epoch + 1
		return tx.PutHost(Host{ID: scope, Epoch: epoch, Incarnation: sha(run), Proc: proc, Renewed: now.UnixNano()})
	})
	return epoch, err
}

// Release gives scope back on a clean exit (§4.3): its sessions end ('exit'), and the incarnation
// is cleared, so this run can never reattach. The next claim takes a new epoch.
func (m *Manager) Release(ctx context.Context, scope, run string, epoch int64) error {
	return m.store.Write(ctx, func(tx Tx) error {
		if err := guardHost(tx, scope, run, epoch); err != nil {
			return err
		}
		if err := m.endScopeSessions(tx, scope, EndedExit); err != nil {
			return err
		}
		h, _, err := tx.Host(scope)
		if err != nil {
			return err
		}
		h.Incarnation = nil
		return tx.PutHost(h)
	})
}

// endScopeSessions ends every live session of scope, for why, in tx.
func (m *Manager) endScopeSessions(tx Tx, scope, why string) error {
	live, err := tx.LiveSessionsOfScope(scope)
	if err != nil {
		return err
	}
	for _, s := range live {
		if err := m.endSession(tx, s, why); err != nil {
			return err
		}
	}
	return nil
}

// endSession ends s in tx and applies what follows from it, in the same transaction: its
// endpoint goes, its attention clears, its wake row stops (no attempt, no deadline), and the mail
// module's effect on its deliveries (SessionEnder).
func (m *Manager) endSession(tx Tx, s Session, why string) error {
	at := tx.Now().UnixNano()
	s.Ended, s.EndedWhy = &at, why
	if err := tx.PutSession(s); err != nil {
		return err
	}
	if err := tx.DeleteEndpoint(s.ID); err != nil {
		return err
	}
	if err := clearAttention(tx, s.ID); err != nil {
		return err
	}
	if w, ok, err := tx.Wake(s.ID); err != nil {
		return err
	} else if ok {
		w.State, w.Token, w.NextAt = WakeIdle, "", nil
		if err := tx.PutWake(w); err != nil {
			return err
		}
	}
	if m.ender != nil {
		return m.ender.EndSessionDeliveries(tx, s, why)
	}
	return nil
}
