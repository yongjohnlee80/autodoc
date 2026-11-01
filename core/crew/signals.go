package crew

// Signals are what a Store adapter tells once a write has committed, so the long-polls and the
// wake scheduler look again without polling (docs/decisions/crew.md#wakers-and-the-wake-scheduler).
// A write that rolls back tells nothing, and a write tells each signal at most once. Either func
// may be nil.
type Signals struct {
	// Feed: the write appended to the human feed's journal (inbox_change).
	Feed func()
	// Wake: the write changed a wake row: a send or a lapsed lease raised its pending_gen, a
	// register seeded it, an endpoint reset it, or an attempt moved it. The scheduler looks again,
	// and every long-poll (mail.wait, crew.wait) checks again. Every change is told, not only a row
	// left due: a send to a session whose row is in_flight raises pending_gen without making the row
	// due, and a waiter must still hear it. A poke is idempotent, so the superset costs a re-check.
	// Mail's fallback jobs raise it too (a job made, taken or done), so a host's mail.fallbacks
	// hears a job wherever it was made.
	Wake func()
}

// Fire tells the signals a committed write raised.
func (s Signals) Fire(feed, wake bool) {
	if feed && s.Feed != nil {
		s.Feed()
	}
	if wake && s.Wake != nil {
		s.Wake()
	}
}
