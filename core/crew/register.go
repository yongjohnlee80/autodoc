package crew

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// Registration is what crew.register records for one agent process (§4.4).
type Registration struct {
	Scope     string
	Run       string
	Epoch     int64
	Codename  string
	Kind      string // claude | codex | agy | opencode …
	Workspace string // the workspace's uid
	Instance  string // the host's spawn id: with the epoch, the registration's idempotency key
	Secret    string // host-generated, 32 random bytes as text; only its hash is kept
}

// Registered is the session a registration made (or found again, on a replay).
type Registered struct {
	Session string `json:"session"`
	Profile string `json:"profile"`
}

// argsDigest is the digest of a registration's immutable arguments: a replay must match them.
func argsDigest(r Registration) []byte {
	return sha(r.Codename + "\x00" + r.Kind + "\x00" + r.Workspace)
}

// newID is a fresh random id: 16 bytes, hex.
func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func checkRegistration(r Registration) error {
	switch {
	case r.Codename == "" || r.Codename == "user" || strings.ContainsAny(r.Codename, "@ \t\n"):
		return fmt.Errorf("%w: codename %q", ErrInvalid, r.Codename)
	case r.Kind == "":
		return fmt.Errorf("%w: an empty kind", ErrInvalid)
	case r.Instance == "":
		return fmt.Errorf("%w: an empty instance", ErrInvalid)
	case len(r.Secret) < 32:
		return fmt.Errorf("%w: a secret shorter than 32 bytes", ErrInvalid)
	}
	return nil
}

// Register starts a session for (r.Scope, r.Codename) under the host's epoch (§4.4). One write:
//   - guardHost: the host still owns the scope at r.Epoch;
//   - a session for (scope, epoch, instance) exists: a replay, answered with the same session, only
//     if its secret and immutable arguments match and it has not ended; else ErrInstanceReused;
//   - otherwise the scope's active profile for the codename is found or made; a live session of
//     it is ErrCodenameBusy; the new session is recorded with its wake row, pending_gen seeded
//     when the profile already has mail it may receive.
//
// A new epoch is a new namespace for instances: a crash successor's agent-1 never meets the old
// one's (§R22). The host keeps the secret and writes the session context only after this succeeds.
func (m *Manager) Register(ctx context.Context, r Registration) (Registered, error) {
	if err := checkRegistration(r); err != nil {
		return Registered{}, err
	}
	var out Registered
	err := m.store.Write(ctx, func(tx Tx) error {
		if err := guardHost(tx, r.Scope, r.Run, r.Epoch); err != nil {
			return err
		}
		s, ok, err := tx.SessionByInstance(r.Scope, r.Epoch, r.Instance)
		if err != nil {
			return err
		}
		if ok {
			if s.Ended != nil || !bytes.Equal(s.SecretHash, sha(r.Secret)) || !bytes.Equal(s.ArgsDigest, argsDigest(r)) {
				return ErrInstanceReused
			}
			out = Registered{Session: s.ID, Profile: s.Profile}
			return nil
		}
		now := tx.Now().UnixNano()
		p, ok, err := tx.ActiveProfile(r.Scope, r.Codename)
		if err != nil {
			return err
		}
		if !ok {
			id, err := newID()
			if err != nil {
				return err
			}
			p = Profile{ID: id, Scope: r.Scope, Codename: r.Codename, Kind: r.Kind, Status: ProfileActive, Created: now}
			if err := tx.PutProfile(p); err != nil {
				return err
			}
		}
		if _, live, err := tx.LiveSession(p.ID); err != nil {
			return err
		} else if live {
			return ErrCodenameBusy
		}
		id, err := newID()
		if err != nil {
			return err
		}
		s = Session{ID: id, Profile: p.ID, Host: r.Scope, Epoch: r.Epoch, Instance: r.Instance,
			SecretHash: sha(r.Secret), ArgsDigest: argsDigest(r), Started: now}
		if err := tx.PutSession(s); err != nil {
			return err
		}
		w := Wake{Session: s.ID, State: WakeIdle}
		if m.ender != nil {
			pending, err := m.ender.HasEligible(tx, p.ID)
			if err != nil {
				return err
			}
			if pending {
				w.PendingGen = 1
			}
		}
		if err := tx.PutWake(w); err != nil {
			return err
		}
		out = Registered{Session: s.ID, Profile: p.ID}
		return nil
	})
	return out, err
}

// Renew keeps the host's ownership of scope: its lease runs another 90 s (§4.4). Hosts renew
// every 30 s.
func (m *Manager) Renew(ctx context.Context, scope, run string, epoch int64) error {
	return m.store.Write(ctx, func(tx Tx) error {
		if err := guardHost(tx, scope, run, epoch); err != nil {
			return err
		}
		h, _, err := tx.Host(scope)
		if err != nil {
			return err
		}
		h.Renewed = tx.Now().UnixNano()
		return tx.PutHost(h)
	})
}

// Exit ends session, which the host started under its epoch: why is EndedExit, or
// EndedStartFailed for a spawn that failed after its registration (§4.4). Ending an ended session
// again is no error.
func (m *Manager) Exit(ctx context.Context, scope, run string, epoch int64, session, why string) error {
	if why != EndedExit && why != EndedStartFailed {
		return fmt.Errorf("%w: exit reason %q", ErrInvalid, why)
	}
	return m.store.Write(ctx, func(tx Tx) error {
		if err := guardHost(tx, scope, run, epoch); err != nil {
			return err
		}
		s, ok, err := tx.Session(session)
		if err != nil {
			return err
		}
		if !ok || s.Host != scope || s.Epoch != epoch {
			return ErrBadCredential // not a session this host started under this epoch
		}
		if s.Ended != nil {
			return nil
		}
		return m.endSession(tx, s, why)
	})
}
