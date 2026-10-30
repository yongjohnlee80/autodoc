package crew

import (
	"context"
	"crypto/subtle"
	"fmt"
	"strings"
)

// A session's credential is "<session id>.<secret>" (docs/decisions/crew.md#the-session-context):
// the session context file carries it, and the daemon keeps only the secret's hash.

// Credential is the credential for session id with secret, as a host writes it into the context.
func Credential(session, secret string) string { return session + "." + secret }

// parseCredential splits a credential into its session id and secret.
func parseCredential(c string) (id, secret string, ok bool) {
	id, secret, ok = strings.Cut(c, ".")
	return id, secret, ok && id != "" && secret != ""
}

// Authenticate is the session credential names, checked in tx: the secret matches its hash
// (compared in constant time), and the session passes guardSession. Every agent verb, crew's and
// mail's, starts with it inside its own write.
func (m *Manager) Authenticate(tx Tx, credential string) (Session, error) {
	id, secret, ok := parseCredential(credential)
	if !ok {
		return Session{}, ErrBadCredential
	}
	s, found, err := tx.Session(id)
	if err != nil {
		return Session{}, err
	}
	if !found || subtle.ConstantTimeCompare(s.SecretHash, sha(secret)) != 1 {
		return Session{}, ErrBadCredential
	}
	return guardSession(tx, id)
}

// Identity is who a credential belongs to (crew.whoami).
type Identity struct {
	Codename string `json:"codename"`
	Scope    string `json:"scope"`
	Profile  string `json:"profile"`
	Session  string `json:"session"`
	Store    string `json:"store"`
}

// Whoami is the identity credential belongs to (docs/decisions/crew.md#the-session-context). store
// is the store id the caller's session context names: a context written for another daemon's store
// is ErrContextMismatch, so an inherited context can't act against the wrong daemon. "" skips that
// check.
func (m *Manager) Whoami(ctx context.Context, credential, store string) (Identity, error) {
	if store != "" && m.storeID != "" && store != m.storeID {
		return Identity{}, fmt.Errorf("%w: the context is for store %q, this daemon's is %q", ErrContextMismatch, store, m.storeID)
	}
	var out Identity
	err := m.store.Read(ctx, func(tx Tx) error {
		s, err := m.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		p, ok, err := tx.Profile(s.Profile)
		if err != nil {
			return err
		}
		if !ok {
			return ErrBadCredential
		}
		out = Identity{Codename: p.Codename, Scope: p.Scope, Profile: p.ID, Session: s.ID, Store: m.storeID}
		return nil
	})
	return out, err
}
