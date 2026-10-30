package mail

import (
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// The module's limits (§4.6).
const (
	MaxBody     = 64 << 10         // a body past it is ErrTooLarge
	LeaseFor    = 10 * time.Minute // a receive's lease
	ReceiveMax  = 8                // deliveries one receive leases
	MaxAttempts = 5                // leases a delivery may lapse before it fails
	expireBatch = 256              // rows one expire step changes, per kind, before it looks again
	mergeHops   = 8                // merged links resolveRecipient follows
)

// Manager is the mail module: durable messages and their deliveries, leased and acknowledged
// (ADR 1793271500 §4.6). Its state machines run on a Store's transactions, so every engine shares
// them; crew guards each session inside the same write.
type Manager struct {
	store Store
	crew  Crew
	newID func() string
	polls *crew.LongPolls // the connections' long-polls (mail.wait's cap); nil caps nothing
}

// Option configures a Manager.
type Option func(*Manager)

// WithIDs sets how message, delivery and lease ids are made (tests make them predictable).
func WithIDs(f func() string) Option { return func(m *Manager) { m.newID = f } }

// WithLongPolls sets the daemon's long-poll limiter, which mail.wait enters: the same one the other
// modules' long-polls share.
func WithLongPolls(l *crew.LongPolls) Option { return func(m *Manager) { m.polls = l } }

// New is the mail module over store, guarded by crew. A nil store builds a Manager that only
// answers Name, Verbs and Errors, as --contracts needs.
func New(store Store, crew Crew, opts ...Option) *Manager {
	m := &Manager{store: store, crew: crew, newID: randomID}
	for _, o := range opts {
		o(m)
	}
	return m
}

func randomID() string {
	var b [16]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24+)
	return hex.EncodeToString(b[:])
}
