package crew

import (
	"context"
	"errors"
	"maps"
	"sync"
	"time"
)

// errNoWaker is an endpoint of a kind no waker serves (phase 3 brings them): a failed attempt,
// backed off like any other.
var errNoWaker = errors.New("crew: no waker for the endpoint's kind")

// wakeRetry is how soon a look that met a store error, or left an end unrecorded, looks again.
const wakeRetry = 5 * time.Second

// Scheduler is the wake scheduler (§4.7): one loop, woken by pokes (a send, an expiry, an endpoint,
// a reattach), by a timer on the earliest deadline, and by a retry timer after a store error,
// reserving attempts for due sessions under wakePermits permits. A permit is held by an attempt's
// goroutine until its call returns: resetting a row never frees one.
type Scheduler struct {
	m       *Manager
	run     string
	wakers  map[string]Waker
	clock   func() time.Time
	report  func(error)
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

// WithWakeRetry sets how soon a look that met a store error looks again (tests shorten it).
func WithWakeRetry(d time.Duration) SchedulerOption { return func(s *Scheduler) { s.retry = d } }

// NewScheduler is the wake scheduler for this daemon run (a fresh random id made once at start),
// calling the wakers by endpoint kind. It is the Manager's from then on: Watch hears its pokes.
func (m *Manager) NewScheduler(run string, wakers map[string]Waker, opts ...SchedulerOption) *Scheduler {
	s := &Scheduler{m: m, run: run, wakers: wakers, clock: time.Now, report: func(error) {}, retry: wakeRetry,
		permits: make(chan struct{}, wakePermits), poke: make(chan struct{}, 1),
		watchers: map[chan struct{}]struct{}{}, owed: map[owedKey]owedEnd{}}
	for _, o := range opts {
		o(s)
	}
	m.sched.Store(s) // the long-polls watch its pokes (Manager.Watch)
	return s
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
		case <-time.After(s.retry):
		}
	}
	defer s.wg.Wait()
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
		if at, ok, err := s.m.nextAt(ctx); err != nil {
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
	due, err := s.m.sweep(ctx)
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
		tok, ep, ok, err := s.m.Reserve(ctx, session, s.run)
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

func (s *Scheduler) attempt(ctx context.Context, session, tok string, ep Endpoint) {
	defer func() { <-s.permits; s.wg.Done(); s.Poke() }() // the permit returns only when this call does
	current, err := s.m.StillCurrent(ctx, session, tok, ep.Generation)
	if err != nil || !current {
		if err != nil {
			s.report(err)
		}
		if err := s.m.Abandon(ctx, session, tok); err != nil {
			s.report(err)
			s.owe(session, tok, owedEnd{abandon: true})
		}
		return
	}
	werr := errNoWaker
	if w := s.wakers[ep.Kind]; w != nil {
		cctx, cancel := context.WithTimeout(ctx, wakeCallLimit)
		werr = w.Wake(cctx, ep, WakeNotice)
		cancel()
	}
	o := classify(werr)
	if err := s.m.Outcome(ctx, session, tok, o); err != nil {
		s.report(err)
		s.owe(session, tok, owedEnd{outcome: o})
	}
}
