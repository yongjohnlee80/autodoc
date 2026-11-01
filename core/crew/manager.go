package crew

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// Manager is the crew module: the registry of scopes, hosts, profiles and sessions
// (docs/decisions/crew.md#the-crew-registry-and-durable-mail). Its state machines run on a Store's
// transactions, so every engine shares them.
type Manager struct {
	store   Store
	ender   SessionEnder
	storeID string // the daemon's store id (sys.hello's store_id): a session context names it
	uid     int    // the daemon's user: endpoint sockets must be this user's
	started time.Time
	sched   atomic.Pointer[Scheduler] // the daemon's scheduler, once NewScheduler made it: Watch hears its pokes
	polls   *LongPolls                // the connections' long-polls (crew.wait's cap); nil caps nothing
	refused func(Registration, error) // each refused registration, its secret blanked (WithRefusals); nil tells no one

	// secrets are the endpoint secrets a waker needs and the store never holds (an agy session's
	// language-server token, docs/decisions/crew.md#wakers-and-the-wake-scheduler), by session,
	// each for the endpoint generation it came with; ended is the sessions that have ended, whose
	// secrets are never held again. Both change only after the write that warrants it has committed
	// (write): a write that rolls back changes neither. A daemon restart forgets them all, and each
	// session's bridge registers again.
	secretsMu sync.Mutex
	secrets   map[string]endpointSecret
	ended     map[string]bool
	// alive is each session's prompt reporter's last heartbeat (ReporterAlive), and causes each
	// session's last failed wake's cause (noteCause), in memory alone
	aliveMu sync.Mutex
	alive   map[string]time.Time
	causes  map[string]string
}

// endpointSecret is a session's endpoint secret and the generation it belongs to.
type endpointSecret struct {
	generation int64
	value      string // "": that generation was registered with none
}

// EndpointSecret is the secret registered with session's endpoint generation gen, if that is still
// the one registered: never an older endpoint's, nor an ended session's.
func (m *Manager) EndpointSecret(session string, gen int64) (string, bool) {
	m.secretsMu.Lock()
	defer m.secretsMu.Unlock()
	s, ok := m.secrets[session]
	if !ok || s.generation != gen || s.value == "" {
		return "", false
	}
	return s.value, true
}

// afterCommit is what a write changes in the manager's memory once the store has committed it, so
// memory follows the store and never leads it.
type afterCommit struct {
	secrets []endpointPublish // endpoint generations stored, with their secrets
	ended   []string          // sessions ended
}

// endpointPublish is one stored endpoint generation's secret, for afterCommit.
type endpointPublish struct {
	session    string
	generation int64
	secret     string
}

// write is m.store.Write with what fn leaves in after applied once the write has committed, and
// nothing applied if it has not (a write the store runs again starts with after empty).
func (m *Manager) write(ctx context.Context, fn func(tx Tx, after *afterCommit) error) error {
	var after afterCommit
	if err := m.store.Write(ctx, func(tx Tx) error {
		after = afterCommit{}
		return fn(tx, &after)
	}); err != nil {
		return err
	}
	if len(after.secrets) == 0 && len(after.ended) == 0 {
		return nil
	}
	m.secretsMu.Lock()
	for _, p := range after.secrets {
		m.publishSecret(p)
	}
	for _, id := range after.ended {
		m.forgetSecret(id)
	}
	m.secretsMu.Unlock()
	if len(after.ended) > 0 { // an ended session's reporter has nothing more to report, nor its wakes
		m.aliveMu.Lock()
		for _, id := range after.ended {
			delete(m.alive, id)
			delete(m.causes, id)
		}
		m.aliveMu.Unlock()
	}
	return nil
}

// publishSecret holds p's secret for its generation, with m.secretsMu held. Writes commit one at a
// time, but what each leaves for afterwards is applied in no set order: a generation older than the
// one held is a write overtaken (it changes nothing), and an ended session never holds a secret
// again, whatever was committed before its end and applied after.
func (m *Manager) publishSecret(p endpointPublish) {
	if m.ended[p.session] {
		return
	}
	if cur, ok := m.secrets[p.session]; ok && cur.generation >= p.generation {
		return
	}
	if m.secrets == nil {
		m.secrets = map[string]endpointSecret{}
	}
	m.secrets[p.session] = endpointSecret{generation: p.generation, value: p.secret}
}

// forgetSecret drops an ended session's secret, with m.secretsMu held, and keeps it ended: a
// session id is never used again, so its mark is all that holds back a registration committed
// before the end and applied after it.
func (m *Manager) forgetSecret(session string) {
	delete(m.secrets, session)
	if m.ended == nil {
		m.ended = map[string]bool{}
	}
	m.ended[session] = true
}

// SessionEnder is the mail module's half of crew's lifecycle changes, run in crew's own write
// transaction after its guard and its own change
// (docs/decisions/crew.md#scopes-and-fenced-ownership). The mail module implements
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
	// seeds the new session's pending_gen from it, so queued mail wakes it
	// (docs/decisions/crew.md#sessions-and-the-guard).
	HasEligible(tx Tx, profile string) (bool, error)
	// EligibleFor: session, a live session of profile, has a delivery it may receive now, unbound
	// or bound to it. crew.wait asks it for each session a host waits on: HasEligible's
	// new-session view leaves out the mail bound to the session itself.
	EligibleFor(tx Tx, profile, session string) (bool, error)
	// UnhandledCount: how many of profile's deliveries are queued or leased, for crew.orphans.
	UnhandledCount(tx Tx, profile string) (int, error)
	// Expire makes mail's time-driven changes real in tx (a lapsed lease queued again, raising
	// pending_gen; an expired message ended), first in every scheduler write that reads
	// eligibility (docs/decisions/crew.md#the-crew-and-mail-tables,
	// docs/decisions/crew.md#wakers-and-the-wake-scheduler).
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

// WithRefusals sets where each refused crew.register is told (the daemon's log): the registration,
// its secret blanked, and why (a fenced or stale epoch, a codename taken, an invalid one).
func WithRefusals(fn func(r Registration, err error)) Option {
	return func(m *Manager) { m.refused = fn }
}

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
