package crew

// Manager is the crew module: the registry of scopes, hosts, profiles and sessions (ADR 1793271500
// §4.3–4.5). Its state machines run on a Store's transactions, so every engine shares them.
type Manager struct {
	store Store
	ender SessionEnder
}

// SessionEnder is what follows a session's end in the mail module, in the same transaction:
// deliveries bound to the session fail ('session_ended'), and their senders are told. The mail
// module implements it.
type SessionEnder interface {
	EndSessionDeliveries(tx Tx, s Session, why string) error
}

// Option configures a Manager.
type Option func(*Manager)

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
