package crew

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"time"
)

// errNoWaker is an endpoint of a kind no waker serves. A reservation takes only the kinds there are
// wakers for, so an attempt meets it only if that ever changes under it: a failed attempt, backed
// off like any other.
var errNoWaker = errors.New("crew: no waker for the endpoint's kind")

// wakeRetry is how soon a look that met a store error, or left an end unrecorded, looks again.
const wakeRetry = 5 * time.Second

// Scheduler is the wake scheduler (docs/decisions/crew.md#wakers-and-the-wake-scheduler): one loop,
// woken by pokes (a send, an expiry, an endpoint, a reattach), by a timer on the earliest deadline,
// and by a retry timer after a store error, reserving attempts for due sessions under wakePermits
// permits. A permit is held by an attempt's goroutine until its call returns: resetting a row never
// frees one.
type Scheduler struct {
	m   *Manager
	run string
	// wakers and the kinds they serve (sorted: the only ones reserved), under wmu: a waker found
	// after the start (AddWaker) joins them while the loop runs
	wmu     sync.RWMutex
	wakers  map[string]Waker
	kinds   []string
	clock   func() time.Time
	report  func(error)
	failed  func(Endpoint, error)
	retry   time.Duration
	permits chan struct{}
	poke    chan struct{}
	wg      sync.WaitGroup

	mu       sync.Mutex
	watchers map[chan struct{}]struct{}

	// owed are the ends (an outcome, or an abandon) a call came to that the store refused to
	// record: kept here, token-fenced, and written again at each look until the store takes them.
	// Without it, one refused write would leave the row in_flight with no deadline, and nothing in
	// this run would look at it again.
	omu  sync.Mutex
	owed map[owedKey]owedEnd

	// stopped is Run's end: from then no Fire starts a call (stopMu orders it against wg.Add)
	stopMu  sync.Mutex
	stopped bool
}

// owedKey is one attempt: a session can owe the ends of two (an old call that outlived an endpoint
// replacement, and the new one), and neither may replace the other.
type owedKey struct{ session, token string }

// owedEnd is one attempt's unrecorded end.
type owedEnd struct {
	outcome Outcome
	abandon bool
}

// SchedulerOption configures a Scheduler.
type SchedulerOption func(*Scheduler)

// WithWakeClock sets the clock the scheduler's timer measures deadlines against (the store's).
func WithWakeClock(now func() time.Time) SchedulerOption { return func(s *Scheduler) { s.clock = now } }

// WithWakeErrors sets where a look's or an attempt's store errors go (the daemon's log).
func WithWakeErrors(report func(error)) SchedulerOption {
	return func(s *Scheduler) { s.report = report }
}

// WithWakeFailures sets where a wake that failed, or whose outcome is unknown, is told, with the
// waker's reason (the daemon's log): the session's row keeps the count and the backoff, never why,
// so this is the only trace of why a session is not being woken.
func WithWakeFailures(failed func(ep Endpoint, err error)) SchedulerOption {
	return func(s *Scheduler) { s.failed = failed }
}

// WithWakeRetry sets how soon a look that met a store error looks again (tests shorten it).
func WithWakeRetry(d time.Duration) SchedulerOption { return func(s *Scheduler) { s.retry = d } }

// NewScheduler is the wake scheduler for this daemon run (a fresh random id made once at start),
// calling the wakers by endpoint kind. It is the Manager's from then on: Watch hears its pokes.
func (m *Manager) NewScheduler(run string, wakers map[string]Waker, opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{m: m, run: run, wakers: map[string]Waker{}, clock: time.Now, report: func(error) {}, retry: wakeRetry,
		failed: func(Endpoint, error) {}, permits: make(chan struct{}, wakePermits), poke: make(chan struct{}, 1),
		watchers: map[chan struct{}]struct{}{}, owed: map[owedKey]owedEnd{}}
	for kind, w := range wakers {
		if w != nil {
			s.wakers[kind] = w
			s.kinds = append(s.kinds, kind)
		}
	}
	slices.Sort(s.kinds)
	for _, o := range opts {
		o(s)
	}
	m.sched.Store(s) // the long-polls watch its pokes (Manager.Watch)
	return s
}

// AddWaker serves kind with w from now on, as when a CLI's waker is found only after the daemon
// started (its capability probed in the background): its sessions, watch-only until then, are
// reserved from the next look, which this pokes. A nil w adds nothing.
func (s *Scheduler) AddWaker(kind string, w Waker) {
	if w == nil {
		return
	}
	s.wmu.Lock()
	if _, ok := s.wakers[kind]; !ok {
		s.kinds = append(s.kinds, kind)
		slices.Sort(s.kinds)
	}
	s.wakers[kind] = w
	s.wmu.Unlock()
	s.Poke()
}

// served is the kinds wakers serve, a copy safe to keep while AddWaker runs.
func (s *Scheduler) served() []string {
	s.wmu.RLock()
	defer s.wmu.RUnlock()
	return slices.Clone(s.kinds)
}

// wakerFor is kind's waker, or nil.
func (s *Scheduler) wakerFor(kind string) Waker {
	s.wmu.RLock()
	defer s.wmu.RUnlock()
	return s.wakers[kind]
}

// Poke asks the loop to look again, and tells every watcher something may have changed. It never
// blocks: a poke while one is pending is the same poke.
func (s *Scheduler) Poke() {
	select {
	case s.poke <- struct{}{}:
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for ch := range s.watchers {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// Watch is a channel that hears every poke, for a long-poll (mail.wait, crew.wait): register it
// before checking, so a change committed in between still signals. stop unregisters it.
func (s *Scheduler) Watch() (ch <-chan struct{}, stop func()) {
	c := make(chan struct{}, 1)
	s.mu.Lock()
	s.watchers[c] = struct{}{}
	s.mu.Unlock()
	return c, func() {
		s.mu.Lock()
		delete(s.watchers, c)
		s.mu.Unlock()
	}
}

// Run recovers the earlier run's attempts, then loops until ctx ends, and waits for its attempts.
// A recovery that fails is retried after the retry wait, never given up on.
func (s *Scheduler) Run(ctx context.Context) error {
	for {
		err := s.m.RecoverRun(ctx, s.run)
		if err == nil {
			break
		}
		s.report(err)
		select {
		case <-ctx.Done():
			return ctx.Err()
		// NOTE: a store that refused the recovery announces nothing when it heals, so the retry is
		// timed; ctx still ends the wait at once.
		case <-time.After(s.retry):
		}
	}
	defer func() {
		// no call starts once the loop is done: a Fire after this finds the scheduler stopped
		s.stopMu.Lock()
		s.stopped = true
		s.stopMu.Unlock()
		s.wg.Wait()
	}()
	// NOTE: the times no event announces (a lease's end, a message's expiry, a backoff's next
	// attempt, the retry after a store error) are waited on with this one timer, reset each turn to
	// the first of them; a poke or ctx ends the wait before it.
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		again := s.look(ctx)
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		// After a store error (or an end still owed) the next look waits the retry, never less:
		// a deadline already past is one this look could not process, and acting on it at once
		// would spin on the store's error. Otherwise the earliest deadline decides.
		wait, armed := s.retry, again
		if at, ok, err := s.m.nextAt(ctx, s.served()); err != nil {
			s.report(err)
			armed = true
		} else if ok && !again {
			wait, armed = max(at.Sub(s.clock()), 0), true
		}
		if armed {
			timer.Reset(wait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.poke:
		case <-timer.C:
		}
	}
}

// Look writes the ends still owed, ends the timed-out waits, then reserves an attempt for each due
// session while permits last, and starts each one's call. It returns once they are started; Wait
// waits for their calls.
func (s *Scheduler) Look(ctx context.Context) { s.look(ctx) }

// look is Look, answering whether it must look again soon: it met a store error, or an end is
// still owed.
func (s *Scheduler) look(ctx context.Context) bool {
	again := s.payOwed(ctx)
	kinds := s.served() // one set of kinds for the whole look, whatever AddWaker does meanwhile
	due, err := s.m.sweep(ctx, kinds)
	if err != nil {
		s.report(err)
		return true
	}
	for _, session := range due {
		select {
		case s.permits <- struct{}{}:
		default:
			return again // none free: a returning call pokes the loop
		}
		tok, ep, ok, err := s.m.Reserve(ctx, session, s.run, kinds)
		if err != nil || !ok {
			if err != nil {
				s.report(err)
				again = true
			}
			<-s.permits
			continue
		}
		s.wg.Add(1)
		go s.attempt(ctx, session, tok, ep)
	}
	return again
}

// Wait waits for every started attempt's call to return.
func (s *Scheduler) Wait() { s.wg.Wait() }

// owe keeps an end the store refused, to be written again at the next look.
func (s *Scheduler) owe(session, token string, e owedEnd) {
	s.omu.Lock()
	s.owed[owedKey{session, token}] = e
	s.omu.Unlock()
}

// payOwed writes each owed end again, token-fenced (a row reset since then takes nothing), and
// answers whether any is still owed.
func (s *Scheduler) payOwed(ctx context.Context) bool {
	s.omu.Lock()
	owed := maps.Clone(s.owed)
	s.omu.Unlock()
	left := false
	for k, e := range owed {
		var err error
		if e.abandon {
			err = s.m.Abandon(ctx, k.session, k.token)
		} else {
			err = s.m.Outcome(ctx, k.session, k.token, e.outcome)
		}
		if err != nil {
			s.report(err)
			left = true
			continue
		}
		s.omu.Lock()
		delete(s.owed, k) // this attempt's end, and only it: written, or stale and taken by nothing
		s.omu.Unlock()
	}
	return left
}

func (s *Scheduler) attempt(ctx context.Context, session, tok string, ep Endpoint) Outcome {
	return s.attemptWith(ctx, ctx, session, tok, ep)
}

// attemptWith is an attempt whose waker call is bounded by call and whose checks and records run
// on record (Fire's: a caller that stops waiting never leaves the row in flight). It answers what
// the call came to; an attempt abandoned before its call is a failure to its caller.
func (s *Scheduler) attemptWith(call, record context.Context, session, tok string, ep Endpoint) Outcome {
	defer func() { <-s.permits; s.wg.Done(); s.Poke() }() // the permit returns only when this call does
	current, via, err := s.m.stillCurrent(record, session, tok, ep.Generation)
	if err != nil || !current {
		if err != nil {
			s.report(err)
		}
		if err := s.m.Abandon(record, session, tok); err != nil {
			s.report(err)
			s.owe(session, tok, owedEnd{abandon: true})
		}
		return OutcomeFailed
	}
	werr := errNoWaker
	if w := s.wakerFor(ep.Kind); w != nil {
		cctx, cancel := context.WithTimeout(call, wakeCallLimit)
		werr = w.Wake(cctx, ep, NoticeFor(SessionMailVia(via, ep.Kind)))
		cancel()
	}
	o := classify(werr)
	if werr != nil {
		s.failed(ep, werr)
	}
	if o == OutcomeFailed || o == OutcomeGone {
		s.m.noteCause(session, werr.Error())
	}
	if err := s.m.Outcome(record, session, tok, o); err != nil {
		s.report(err)
		s.owe(session, tok, owedEnd{outcome: o})
	}
	return o
}
