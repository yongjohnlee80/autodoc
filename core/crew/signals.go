package crew

// Signals are what a Store adapter tells once a write has committed, so the long-polls and the
// wake scheduler look again without polling (ADR 1793271500 §4.7, §4.8). A write that rolls back
// tells nothing, and a write tells each signal at most once. Either func may be nil.
type Signals struct {
	// Feed: the write appended to the human feed's journal (inbox_change).
	Feed func()
	// Wake: the write left a wake row due (WakeDue), so a wake may now be reserved for it.
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

// WakeDue reports whether w, as written, leaves work to announce from an idle row: a send, a
// register's seed, a lapsed lease queued again, or a new endpoint's reset. Signals.Wake follows it.
func WakeDue(w Wake) bool { return w.State == WakeIdle && w.PendingGen > w.AnnouncedGen }
