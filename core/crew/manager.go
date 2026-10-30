package crew

// Manager is the crew module: the registry of scopes, hosts, profiles and sessions (ADR 1793271500
// §4.3–4.5). Its state machines run on a Store's transactions, so every engine shares them.
type Manager struct {
	store   Store
	ender   SessionEnder
	storeID string // the daemon's store id (sys.hello's store_id): a session context names it
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
}

// Option configures a Manager.
type Option func(*Manager)

// WithStoreID sets the daemon's store id, which crew.whoami checks a session context against.
func WithStoreID(id string) Option { return func(m *Manager) { m.storeID = id } }

// WithSessionEnder sets the mail module's effect of a session's end.
func WithSessionEnder(e SessionEnder) Option { return func(m *Manager) { m.ender = e } }

// New is the crew module over store. A nil store builds a Manager that only answers Name, Verbs and
// Errors, as --contracts needs.
func New(store Store, opts ...Option) *Manager {
	m := &Manager{store: store}
	for _, o := range opts {
		o(m)
	}
	return m
}
