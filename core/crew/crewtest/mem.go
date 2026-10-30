// Package crewtest holds the crew module's test kit: an in-memory Store, and the conformance suite
// every engine's adapter runs (Run), so the state machines are checked the same way on each.
package crewtest

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// Mem is a crew.Store in memory. A Write works on a copy and commits it only when fn returns nil,
// so a refused or failed verb leaves nothing behind, as a database transaction does. Writes are
// serialized, as the daemon's one SQLite writer serializes them.
type Mem struct {
	mu  sync.Mutex
	now func() time.Time
	st  memState
}

type memState struct {
	hosts    map[string]crew.Host
	scopes   map[string]crew.Scope
	profiles map[string]crew.Profile
	sessions map[string]crew.Session
}

// NewMem is an empty store whose transactions read the time from now.
func NewMem(now func() time.Time) *Mem {
	return &Mem{now: now, st: memState{
		hosts: map[string]crew.Host{}, scopes: map[string]crew.Scope{},
		profiles: map[string]crew.Profile{}, sessions: map[string]crew.Session{},
	}}
}

func (s memState) clone() memState {
	return memState{hosts: maps.Clone(s.hosts), scopes: maps.Clone(s.scopes),
		profiles: maps.Clone(s.profiles), sessions: maps.Clone(s.sessions)}
}

// Write runs fn on a copy of the state and commits the copy if fn succeeds.
func (m *Mem) Write(_ context.Context, fn func(crew.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	tx := &memTx{st: m.st.clone(), now: m.now()}
	if err := fn(tx); err != nil {
		return err
	}
	m.st = tx.st
	return nil
}

// Read runs fn on a copy of the state; its changes are dropped.
func (m *Mem) Read(_ context.Context, fn func(crew.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn(&memTx{st: m.st.clone(), now: m.now()})
}

type memTx struct {
	st  memState
	now time.Time
}

func (t *memTx) Now() time.Time { return t.now }

func (t *memTx) Host(id string) (crew.Host, bool, error) {
	h, ok := t.st.hosts[id]
	h.Incarnation = slices.Clone(h.Incarnation)
	return h, ok, nil
}

func (t *memTx) PutHost(h crew.Host) error {
	h.Incarnation = slices.Clone(h.Incarnation)
	t.st.hosts[h.ID] = h
	return nil
}

func (t *memTx) Scope(id string) (crew.Scope, bool, error) {
	s, ok := t.st.scopes[id]
	return s, ok, nil
}

func (t *memTx) PutScope(s crew.Scope) error { t.st.scopes[s.ID] = s; return nil }

func (t *memTx) Profile(id string) (crew.Profile, bool, error) {
	p, ok := t.st.profiles[id]
	return p, ok, nil
}

func (t *memTx) ActiveProfile(scope, codename string) (crew.Profile, bool, error) {
	for _, p := range t.st.profiles {
		if p.Scope == scope && p.Codename == codename && p.Status == crew.ProfileActive {
			return p, true, nil
		}
	}
	return crew.Profile{}, false, nil
}

func (t *memTx) PutProfile(p crew.Profile) error { t.st.profiles[p.ID] = p; return nil }

func (t *memTx) Session(id string) (crew.Session, bool, error) {
	s, ok := t.st.sessions[id]
	return copySession(s), ok, nil
}

func (t *memTx) SessionByInstance(host string, epoch int64, instance string) (crew.Session, bool, error) {
	for _, s := range t.st.sessions {
		if s.Host == host && s.Epoch == epoch && s.Instance == instance {
			return copySession(s), true, nil
		}
	}
	return crew.Session{}, false, nil
}

func (t *memTx) LiveSession(profile string) (crew.Session, bool, error) {
	for _, s := range t.st.sessions {
		if s.Profile == profile && s.Ended == nil {
			return copySession(s), true, nil
		}
	}
	return crew.Session{}, false, nil
}

// LiveSessionsOfScope is in id order, as the SQL adapters' ORDER BY id gives it.
func (t *memTx) LiveSessionsOfScope(scope string) ([]crew.Session, error) {
	var out []crew.Session
	for _, s := range t.st.sessions {
		if s.Host == scope && s.Ended == nil {
			out = append(out, copySession(s))
		}
	}
	slices.SortFunc(out, func(a, b crew.Session) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (t *memTx) PutSession(s crew.Session) error { t.st.sessions[s.ID] = copySession(s); return nil }

// copySession keeps a stored session from sharing a caller's slices or pointer.
func copySession(s crew.Session) crew.Session {
	s.SecretHash, s.ArgsDigest = slices.Clone(s.SecretHash), slices.Clone(s.ArgsDigest)
	if s.Ended != nil {
		e := *s.Ended
		s.Ended = &e
	}
	return s
}
