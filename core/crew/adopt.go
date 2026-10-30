package crew

import (
	"context"
	"fmt"
)

// Orphan is a closed AutoDoc window's scope with mail nobody handled (crew.orphans, §4.3).
type Orphan struct {
	Scope    string   `json:"scope"`
	Profiles []string `json:"profiles"` // the codenames with unhandled mail
	Unread   int      `json:"unread"`
}

// Orphans are the AutoDoc scopes whose holder is free and that have unhandled mail: what a new
// window offers to Adopt, Keep or Discard (Q2 b). There is no orphan state to keep in sync: it is
// read from ownership and mail, now.
func (m *Manager) Orphans(ctx context.Context) ([]Orphan, error) {
	var out []Orphan
	err := m.store.Read(ctx, func(tx Tx) error {
		now := tx.Now()
		scopes, err := tx.Scopes("autodoc")
		if err != nil {
			return err
		}
		profiles, err := tx.ActiveProfiles()
		if err != nil {
			return err
		}
		for _, sc := range scopes {
			if sc.State != ScopeLive {
				continue
			}
			h, ok, err := tx.Host(sc.ID)
			if err != nil {
				return err
			}
			if ok && !free(h, now) {
				continue
			}
			o := Orphan{Scope: sc.ID}
			for _, p := range profiles {
				if p.Scope != sc.ID || m.ender == nil {
					continue
				}
				n, err := m.ender.UnhandledCount(tx, p.ID)
				if err != nil {
					return err
				}
				if n > 0 {
					o.Profiles = append(o.Profiles, p.Codename)
					o.Unread += n
				}
			}
			if o.Unread > 0 {
				out = append(out, o)
			}
		}
		return nil
	})
	return out, err
}

// fenceOrphan checks, in tx, that the caller still owns to and that from is a live scope whose
// holder is free (revalidated here, never trusted from an earlier Orphans), then fences from: its
// epoch rises and its incarnation clears, so the old owner can do nothing more, and its sessions
// end. It answers from's active profiles.
func (m *Manager) fenceOrphan(tx Tx, after *afterCommit, from, to, run string, epoch int64) ([]Profile, error) {
	if from == to {
		return nil, fmt.Errorf("%w: a scope can't adopt or discard itself", ErrInvalid)
	}
	if err := guardHost(tx, to, run, epoch); err != nil {
		return nil, err
	}
	sc, ok, err := tx.Scope(from)
	if err != nil {
		return nil, err
	}
	h, held, err := tx.Host(from)
	if err != nil {
		return nil, err
	}
	if !ok || sc.State != ScopeLive || (held && !free(h, tx.Now())) {
		return nil, ErrScopeBusy
	}
	h.ID, h.Epoch, h.Incarnation = from, h.Epoch+1, nil
	if err := tx.PutHost(h); err != nil {
		return nil, err
	}
	sc.State = ScopeAdopting
	if err := tx.PutScope(sc); err != nil {
		return nil, err
	}
	if err := m.endScopeSessions(tx, after, from, EndedAdopted); err != nil {
		return nil, err
	}
	all, err := tx.ActiveProfiles()
	if err != nil {
		return nil, err
	}
	var out []Profile
	for _, p := range all {
		if p.Scope == from {
			out = append(out, p)
		}
	}
	return out, nil
}

// Adopt fences the orphan scope from and moves its mail to the caller's scope to (crew.adopt,
// §4.3), in one write. For each active profile p of from: if to has an active profile of the same
// codename, p becomes 'merged' into it, so every later delivery to p follows; otherwise p moves to to
// whole, keeping its mail and order. Only then does the mail module move p's deliveries
// (MergeDeliveries), and raise the survivors' pending_gen: a delivery the merge itself makes to p
// already resolves to the survivor. from ends 'gone'.
func (m *Manager) Adopt(ctx context.Context, from, to, run string, epoch int64) error {
	return m.write(ctx, func(tx Tx, after *afterCommit) error {
		profiles, err := m.fenceOrphan(tx, after, from, to, run, epoch)
		if err != nil {
			return err
		}
		for _, p := range profiles {
			q, merge, err := tx.ActiveProfile(to, p.Codename)
			if err != nil {
				return err
			}
			// the forwarding first: once p is merged into q (or moved to to), any delivery the merge
			// itself makes to p, a notice to p as a sender say, resolves to the survivor. Merging
			// first queued such a notice on p, which then stopped receiving.
			into := ""
			if merge {
				into = q.ID
				p.Status, p.MergedInto = ProfileMerged, q.ID
			} else {
				p.Scope = to
			}
			if err := tx.PutProfile(p); err != nil {
				return err
			}
			if m.ender != nil {
				if err := m.ender.MergeDeliveries(tx, p.ID, into); err != nil {
					return err
				}
			}
		}
		return goneScope(tx, from)
	})
}

// Discard fences the orphan scope from and fails its unhandled mail, telling each sender
// (crew.discard, §4.3), in one write. The caller proves it owns to, as for Adopt. The scope goes
// gone BEFORE the mail half runs: a profile that mailed itself gets its discard notice queued
// on it, and only a gone scope makes mail fail that notice rather than leave it waiting forever.
func (m *Manager) Discard(ctx context.Context, from, to, run string, epoch int64) error {
	return m.write(ctx, func(tx Tx, after *afterCommit) error {
		profiles, err := m.fenceOrphan(tx, after, from, to, run, epoch)
		if err != nil {
			return err
		}
		if err := goneScope(tx, from); err != nil {
			return err
		}
		for _, p := range profiles {
			if m.ender != nil {
				if err := m.ender.DiscardDeliveries(tx, p.ID); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// goneScope ends scope: nothing claims it again.
func goneScope(tx Tx, scope string) error {
	sc, _, err := tx.Scope(scope)
	if err != nil {
		return err
	}
	sc.State = ScopeGone
	return tx.PutScope(sc)
}
