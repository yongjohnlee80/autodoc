package crew

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
)

// The states of a session's crew access, as crew.access gives them
// (docs/decisions/crew.md#re-issuing-a-bridge).
const (
	AccessLive  = "live"  // the credential's session is live, its host holding the scope at its epoch
	AccessEnded = "ended" // the credential's session ended, or its host's epoch moved on: the host re-joins the slot
)

// The states of a live session's endpoint, as crew.access gives them.
const (
	EndpointRegistered = "registered" // it registered a way to wake it, and its wakes reach it
	EndpointNone       = "none"       // it registered none
	EndpointFailing    = "failing"    // its last UnreachableAfter wakes failed
)

// Access is what a session's crew access holds now (crew.access): what `autodoc crew reissue` reads to
// know what to re-issue. It never holds a credential or a secret: only a host writes those, into the
// slot's own context file.
type Access struct {
	Session    string `json:"session"`              // AccessLive or AccessEnded
	Why        string `json:"why,omitempty"`        // ended: how (exit, takeover, reclaim, adopted, start_failed, stale_epoch); live: crew.list's why
	Codename   string `json:"codename"`             // the session's profile
	Kind       string `json:"kind"`                 // claude, codex, agy, opencode …
	Scope      string `json:"scope"`                // its profile's scope
	Host       string `json:"host"`                 // holds, or gone (its host's lease lapsed or its process died)
	Endpoint   string `json:"endpoint,omitempty"`   // a live session's: Endpoint*
	Generation int64  `json:"generation,omitempty"` // its endpoint's generation, when it has one
	Store      string `json:"store"`                // this daemon's store id
}

// AccessOf is the crew access of credential's session (crew.access). A credential whose secret
// matches an ended session's (a context the slot kept across a re-join, a daemon restart that came
// after a reclaim) is that session's, ended: what it can prove is that it held that session, so
// the answer says no more than that session's own state. A credential matching no session is
// ErrBadCredential, as every agent verb's. store is as Whoami's.
func (m *Manager) AccessOf(ctx context.Context, credential, store string) (Access, error) {
	if store != "" && m.storeID != "" && store != m.storeID {
		return Access{}, fmt.Errorf("%w: the context is for store %q, this daemon's is %q", ErrContextMismatch, store, m.storeID)
	}
	id, secret, ok := parseCredential(credential)
	if !ok {
		return Access{}, ErrBadCredential
	}
	var out Access
	err := m.store.Read(ctx, func(tx Tx) error {
		s, found, err := tx.Session(id)
		if err != nil {
			return err
		}
		if !found || subtle.ConstantTimeCompare(s.SecretHash, sha(secret)) != 1 {
			return ErrBadCredential
		}
		p, ok, err := tx.Profile(s.Profile)
		if err != nil {
			return err
		}
		if !ok {
			return ErrBadCredential
		}
		out = Access{Codename: p.Codename, Kind: p.Kind, Scope: p.Scope, Store: m.storeID, Host: "holds"}
		now := tx.Now()
		if gone, err := m.hostGone(tx, s, now); err != nil {
			return err
		} else if gone {
			out.Host = "gone"
		}
		if _, err := guardSession(tx, s.ID); err != nil {
			if !errors.Is(err, ErrBadCredential) && !errors.Is(err, ErrStaleEpoch) {
				return err
			}
			out.Session, out.Why = AccessEnded, s.EndedWhy
			if s.Ended == nil {
				out.Why = "stale_epoch" // live by its row, but its host holds the scope at another epoch
			}
			return nil
		}
		out.Session = AccessLive
		e, has, err := tx.Endpoint(s.ID)
		if err != nil {
			return err
		}
		_, out.Why, err = m.reachability(tx, s, now)
		if err != nil {
			return err
		}
		switch {
		case !has:
			out.Endpoint = EndpointNone
		default:
			out.Generation = e.Generation
			w, _, err := tx.Wake(s.ID)
			if err != nil {
				return err
			}
			out.Endpoint = EndpointRegistered
			if w.Failures >= UnreachableAfter {
				out.Endpoint = EndpointFailing
			}
		}
		return nil
	})
	return out, err
}
