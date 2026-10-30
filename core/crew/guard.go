package crew

import (
	"bytes"
	"crypto/sha256"
)

// The guards (docs/decisions/crew.md#sessions-and-the-guard). Every state-changing verb, a host's
// and an agent's alike, calls one inside its OWN write transaction, before it reads or changes
// anything else. golib's rpc runs a connection's handlers concurrently, so a check before the write
// would let a takeover commit in between; inside the write, the takeover and the verb are
// serialized and the loser sees the other.

// sha is the digest the tables keep of a run token or a secret; neither is ever stored.
func sha(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// guardHost passes when run's process still owns scope at epoch, and the scope is live.
func guardHost(tx Tx, scope, run string, epoch int64) error {
	h, ok, err := tx.Host(scope)
	if err != nil {
		return err
	}
	if !ok || len(h.Incarnation) == 0 || !bytes.Equal(h.Incarnation, sha(run)) || h.Epoch != epoch {
		return ErrStaleEpoch
	}
	return scopeLive(tx, scope)
}

// GuardSession is guardSession for another module's write transaction (the mail module's send,
// receive, ack and reply): the same checks, in tx.
func (m *Manager) GuardSession(tx Tx, id string) (Session, error) { return guardSession(tx, id) }

// guardSession re-reads session id in this transaction (never trusting an earlier read) and passes
// when it is live and its host still holds the scope at the epoch it was registered under.
func guardSession(tx Tx, id string) (Session, error) {
	s, ok, err := tx.Session(id)
	if err != nil {
		return Session{}, err
	}
	if !ok || s.Ended != nil {
		return Session{}, ErrBadCredential
	}
	h, ok, err := tx.Host(s.Host)
	if err != nil {
		return Session{}, err
	}
	if !ok || h.Epoch != s.Epoch {
		return Session{}, ErrStaleEpoch
	}
	if err := scopeLive(tx, s.Host); err != nil {
		return Session{}, err
	}
	return s, nil
}

// scopeLive passes when scope exists and is live: adoption and its end fence every verb of it.
func scopeLive(tx Tx, scope string) error {
	sc, ok, err := tx.Scope(scope)
	if err != nil {
		return err
	}
	if !ok || sc.State != ScopeLive {
		return ErrStaleEpoch
	}
	return nil
}
