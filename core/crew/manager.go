package crew

import (
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Manager is the crew module: the registry of scopes, hosts, profiles and sessions (ADR 1793271500
// §4.3–4.5). Its state machines run on a Store's transactions, so every engine shares them.
type Manager struct {
	store   Store
	ender   SessionEnder
	storeID string // the daemon's store id (sys.hello's store_id): a session context names it
	uid     int    // the daemon's user: endpoint sockets must be this user's
	started time.Time
	sched   atomic.Pointer[Scheduler] // the daemon's scheduler, once NewScheduler made it: Watch hears its pokes
	polls   *LongPolls                // the connections' long-polls (crew.wait's cap); nil caps nothing

	// secrets are the endpoint secrets a waker needs and the store never holds (an agy session's
	// language-server token, §4.7), by session, each for the endpoint generation it came with: a
	// daemon restart forgets them all, and the session's bridge registers again.
	secretsMu sync.Mutex
	secrets   map[string]endpointSecret
}

// forgetSecret drops session's endpoint secret, whatever its generation: the session has ended
// (endSession, in its write). An end that rolls back loses the secret of a session still live: its
// wakes fail, safely, until its bridge registers again.
func (m *Manager) forgetSecret(session string) {
	m.secretsMu.Lock()
	defer m.secretsMu.Unlock()
	delete(m.secrets, session)
}

// endpointSecret is a session's endpoint secret and the generation it belongs to.
type endpointSecret struct {
	generation int64
	value      string
}

// EndpointSecret is the secret registered with session's endpoint generation gen, if that is still
// the one registered: never an older endpoint's.
func (m *Manager) EndpointSecret(session string, gen int64) (string, bool) {
	m.secretsMu.Lock()
	defer m.secretsMu.Unlock()
	s, ok := m.secrets[session]
	if !ok || s.generation != gen {
		return "", false
	}
	return s.value, true
}

// keepSecret holds secret for session's endpoint generation gen, called in the write that stores
// that generation (writes are one at a time, so in their order): an empty secret drops the
// session's, and one older than the secret held changes nothing. A write that rolls back after it
// leaves a secret for a generation never stored, which no wake reads (it reads the stored
// generation's) and the next registration replaces.
func (m *Manager) keepSecret(session string, gen int64, secret string) {
	m.secretsMu.Lock()
	defer m.secretsMu.Unlock()
	if s, ok := m.secrets[session]; ok && s.generation > gen {
		return
	}
	if secret == "" {
		delete(m.secrets, session)
		return
	}
	if m.secrets == nil {
		m.secrets = map[string]endpointSecret{}
	}
	m.secrets[session] = endpointSecret{generation: gen, value: secret}
}

// SessionEnder is the mail module's half of crew's lifecycle changes, run in crew's own write
// transaction after its guard and its own change (ADR 1793271500 §4.3). The mail module implements
// it; its methods take the same tx, which the one adapter makes a mail transaction too.
type SessionEnder interface {
	// EndSessionDeliveries: s ended for why; the deliveries bound to it fail ('session_ended'),
	// and their senders are told.
	EndSessionDeliveries(tx Tx, s Session, why string) error
	// MergeDeliveries: adoption's mail half. from's open deliveries are normalized onto into; a
	// duplicate of one into already holds becomes 'merged'; moved rows take into's next seqs, and
	// into's live session's pending_gen is raised.
	MergeDeliveries(tx Tx, from, into string) error
	// DiscardDeliveries: every unhandled delivery of profile fails ('discarded'), senders told.
	DiscardDeliveries(tx Tx, profile string) error
	// HasEligible: profile has a delivery a new session of it may receive now. crew.register
	// seeds the new session's pending_gen from it, so queued mail wakes it (§4.4).
	HasEligible(tx Tx, profile string) (bool, error)
	// EligibleFor: session, a live session of profile, has a delivery it may receive now, unbound
	// or bound to it. crew.wait asks it for each session a host waits on: HasEligible's
	// new-session view leaves out the mail bound to the session itself.
	EligibleFor(tx Tx, profile, session string) (bool, error)
	// UnhandledCount: how many of profile's deliveries are queued or leased, for crew.orphans.
	UnhandledCount(tx Tx, profile string) (int, error)
	// Expire makes mail's time-driven changes real in tx (a lapsed lease queued again, raising
	// pending_gen; an expired message ended), first in every scheduler write that reads
	// eligibility (§4.2, §4.7).
	Expire(tx Tx) error
	// NextDeadline is the earliest instant mail's state changes by time alone (a lease lapsing, a
	// message expiring), so the scheduler's timer wakes for it; false when none is pending.
	NextDeadline(tx Tx) (int64, bool, error)
}

// Option configures a Manager.
type Option func(*Manager)

// WithStarted sets when the daemon started: hosts get 90 s from then to reconnect before their
// agents read offline (presence "reconnecting").
func WithStarted(t time.Time) Option { return func(m *Manager) { m.started = t } }

// WithUID sets the user whose CLI sockets endpoints may name (os.Getuid() by default).
func WithUID(uid int) Option { return func(m *Manager) { m.uid = uid } }

// WithStoreID sets the daemon's store id, which crew.whoami checks a session context against.
func WithStoreID(id string) Option { return func(m *Manager) { m.storeID = id } }

// WithLongPolls sets the daemon's long-poll limiter, which crew.wait enters: the same one the other
// modules' long-polls share.
func WithLongPolls(l *LongPolls) Option { return func(m *Manager) { m.polls = l } }

// WithSessionEnder sets the mail module's effect of a session's end.
func WithSessionEnder(e SessionEnder) Option { return func(m *Manager) { m.ender = e } }

// New is the crew module over store. A nil store builds a Manager that only answers Name, Verbs and
// Errors, as --contracts needs.
func New(store Store, opts ...Option) *Manager {
	m := &Manager{store: store, uid: os.Getuid(), started: time.Now()}
	for _, o := range opts {
		o(m)
	}
	return m
}
