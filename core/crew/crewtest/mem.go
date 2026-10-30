// Package crewtest holds the crew module's test kit: an in-memory Store, and the conformance suite
// every engine's adapter runs (Run), so the state machines are checked the same way on each.
package crewtest

import (
	"context"
	"errors"
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
	sig crew.Signals
}

type memState struct {
	hosts    map[string]crew.Host
	scopes   map[string]crew.Scope
	profiles map[string]crew.Profile
	sessions map[string]crew.Session
	wakes    map[string]crew.Wake
	ends     map[string]crew.Endpoint
	attn     map[string]crew.Attention
	inbox    []InboxChange
	inboxAt  []int64 // each journal row's time; its seq is its index + 1 (Mem never prunes)
}

// InboxChange is one row of the human feed's journal, as Mem keeps it.
type InboxChange struct{ Kind, Ref string }

// InboxRow is a journal row with its seq and time, as the inbox module reads it.
type InboxRow struct {
	Seq       int64
	Kind, Ref string
	At        int64
}

// NewMem is an empty store whose transactions read the time from now.
func NewMem(now func() time.Time) *Mem {
	return &Mem{now: now, st: memState{
		hosts: map[string]crew.Host{}, scopes: map[string]crew.Scope{},
		profiles: map[string]crew.Profile{}, sessions: map[string]crew.Session{}, wakes: map[string]crew.Wake{},
		ends: map[string]crew.Endpoint{}, attn: map[string]crew.Attention{},
	}}
}

func (s memState) clone() memState {
	return memState{hosts: maps.Clone(s.hosts), scopes: maps.Clone(s.scopes),
		profiles: maps.Clone(s.profiles), sessions: maps.Clone(s.sessions), wakes: maps.Clone(s.wakes),
		ends: maps.Clone(s.ends), attn: maps.Clone(s.attn), inbox: slices.Clone(s.inbox),
		inboxAt: slices.Clone(s.inboxAt)}
}

// SetSignals sets what a committed write tells (crew.Signals), as the engine's adapter does.
func (m *Mem) SetSignals(s crew.Signals) {
	m.mu.Lock()
	m.sig = s
	m.mu.Unlock()
}

// Write runs fn on a copy of the state and commits the copy if fn succeeds; then, outside the
// lock, it fires the signals the write raised.
func (m *Mem) Write(_ context.Context, fn func(crew.Tx) error) error {
	m.mu.Lock()
	tx := &memTx{st: m.st.clone(), now: m.now()}
	if err := fn(tx); err != nil {
		m.mu.Unlock()
		return err
	}
	m.st = tx.st
	sig := m.sig
	m.mu.Unlock()
	sig.Fire(tx.feed, tx.wake)
	return nil
}

// Read runs fn on a copy of the state; its changes are dropped.
func (m *Mem) Read(_ context.Context, fn func(crew.Tx) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fn(&memTx{st: m.st.clone(), now: m.now()})
}

type memTx struct {
	st         memState
	now        time.Time
	feed, wake bool // the signals this write raised (crew.Signals)
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

func (t *memTx) Scopes(kind string) ([]crew.Scope, error) {
	var out []crew.Scope
	for _, s := range t.st.scopes {
		if s.Kind == kind {
			out = append(out, s)
		}
	}
	slices.SortFunc(out, func(a, b crew.Scope) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (t *memTx) ActiveProfiles() ([]crew.Profile, error) {
	var out []crew.Profile
	for _, p := range t.st.profiles {
		if p.Status == crew.ProfileActive {
			out = append(out, p)
		}
	}
	slices.SortFunc(out, func(a, b crew.Profile) int {
		if c := strings.Compare(a.Scope, b.Scope); c != 0 {
			return c
		}
		return strings.Compare(a.Codename, b.Codename)
	})
	return out, nil
}

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

func (t *memTx) Wake(session string) (crew.Wake, bool, error) {
	w, ok := t.st.wakes[session]
	return copyWake(w), ok, nil
}

func (t *memTx) PutWake(w crew.Wake) error {
	t.st.wakes[w.Session] = copyWake(w)
	t.wake = true
	return nil
}

// wakesWhere is the wake rows keep admits, by session, at most limit (0: all).
func (t *memTx) wakesWhere(keep func(crew.Wake) bool, limit int) []crew.Wake {
	var out []crew.Wake
	for _, w := range t.st.wakes {
		if keep(w) {
			out = append(out, copyWake(w))
		}
	}
	slices.SortFunc(out, func(a, b crew.Wake) int { return strings.Compare(a.Session, b.Session) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// errLimit is a limited read asked for no rows: a caller's bug, as SQLite's LIMIT 0 would hide.
var errLimit = errors.New("crewtest: a limit must be > 0")

func (t *memTx) WakesDue(now int64, limit int) ([]crew.Wake, error) {
	if limit <= 0 {
		return nil, errLimit
	}
	return t.wakesWhere(func(w crew.Wake) bool {
		s, live := t.st.sessions[w.Session]
		_, reachable := t.st.ends[w.Session]
		return w.State == crew.WakeIdle && w.PendingGen > w.AnnouncedGen && (w.NextAt == nil || *w.NextAt <= now) &&
			live && s.Ended == nil && reachable
	}, limit), nil
}

func (t *memTx) WakesTimedOut(now int64, limit int) ([]crew.Wake, error) {
	if limit <= 0 {
		return nil, errLimit
	}
	return t.wakesWhere(func(w crew.Wake) bool {
		return w.State == crew.WakeAwaiting && w.NextAt != nil && *w.NextAt <= now
	}, limit), nil
}

func (t *memTx) WakesInFlight() ([]crew.Wake, error) {
	return t.wakesWhere(func(w crew.Wake) bool { return w.State == crew.WakeInFlight }, 0), nil
}

func (t *memTx) NextWakeAt() (int64, bool, error) {
	var at int64
	has := false
	for _, w := range t.st.wakes {
		if w.NextAt == nil || !(w.State == crew.WakeAwaiting || (w.State == crew.WakeIdle && w.PendingGen > w.AnnouncedGen)) {
			continue
		}
		if !has || *w.NextAt < at {
			at, has = *w.NextAt, true
		}
	}
	return at, has, nil
}

func (t *memTx) Endpoint(session string) (crew.Endpoint, bool, error) {
	e, ok := t.st.ends[session]
	return e, ok, nil
}

func (t *memTx) PutEndpoint(e crew.Endpoint) error { t.st.ends[e.Session] = e; return nil }

func (t *memTx) DeleteEndpoint(session string) error { delete(t.st.ends, session); return nil }

func (t *memTx) Attention(session string) (crew.Attention, bool, error) {
	a, ok := t.st.attn[session]
	if a.Cleared != nil {
		c := *a.Cleared
		a.Cleared = &c
	}
	return a, ok, nil
}

func (t *memTx) PutAttention(a crew.Attention) error {
	if a.Cleared != nil {
		c := *a.Cleared
		a.Cleared = &c
	}
	t.st.attn[a.Session] = a
	return nil
}

func (t *memTx) AddInboxChange(kind, ref string) error {
	t.st.inbox = append(t.st.inbox, InboxChange{kind, ref})
	t.st.inboxAt = append(t.st.inboxAt, t.now.UnixNano())
	t.feed = true
	return nil
}

// InboxRows is the journal's rows after seq, at most limit (0: all), for the inbox module's Mem.
func (t *memTx) InboxRows(after int64, limit int) []InboxRow {
	var out []InboxRow
	for i := max(after, 0); i < int64(len(t.st.inbox)) && (limit <= 0 || len(out) < limit); i++ {
		out = append(out, InboxRow{Seq: i + 1, Kind: t.st.inbox[i].Kind, Ref: t.st.inbox[i].Ref, At: t.st.inboxAt[i]})
	}
	return out
}

// InboxBounds is the journal's oldest and newest seq: 0, 0 when it never had a row.
func (t *memTx) InboxBounds() (int64, int64) {
	if len(t.st.inbox) == 0 {
		return 0, 0
	}
	return 1, int64(len(t.st.inbox))
}

// Raised is every attention row not cleared, by session.
func (t *memTx) Raised() ([]crew.Attention, error) {
	var out []crew.Attention
	for _, a := range t.st.attn {
		if a.Cleared == nil {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, func(a, b crew.Attention) int { return strings.Compare(a.Session, b.Session) })
	return out, nil
}

// Inbox is the human feed's journal so far, in order.
func (m *Mem) Inbox() []InboxChange {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.st.inbox)
}

// copyWake keeps a stored wake row from sharing a caller's pointer.
func copyWake(w crew.Wake) crew.Wake {
	if w.NextAt != nil {
		n := *w.NextAt
		w.NextAt = &n
	}
	return w
}

// copySession keeps a stored session from sharing a caller's slices or pointer.
func copySession(s crew.Session) crew.Session {
	s.SecretHash, s.ArgsDigest = slices.Clone(s.SecretHash), slices.Clone(s.ArgsDigest)
	if s.Ended != nil {
		e := *s.Ended
		s.Ended = &e
	}
	return s
}
