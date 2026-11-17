package embedder

import (
	"errors"
	"sync"
	"time"
)

// The worker's pace: how many texts one provider call carries, and how many calls are in flight at
// once. It starts at the configured batch and slots. A call that runs out of its own time halves
// both, down to one text and one call: a slow provider (a CPU-only one, on a first load) is then
// asked for less at once, so some size answers in time and the texts get embedded, where a fixed
// size would time out every call and store nothing while the provider kept computing the calls it
// was left with. Calls that answer in well under their time step back up: the batch first, then
// the calls in flight, to the configured values.

// errSteppedDown is a batch that ran out of its time and stepped the pace down: its texts wait for
// the next round, at the smaller size, which is no failure to back off after.
var errSteppedDown = errors.New("embedding: the call ran out of time, and the pace stepped down")

// growAfter is how many calls in a row must answer in under half their time before the pace steps
// up once.
const growAfter = 4

// pace is the worker's batch size and calls in flight now, shared by every tenant's rounds (the
// provider is one).
type pace struct {
	mu                 sync.Mutex
	batch, inFlight    int // now
	maxBatch, maxSlots int // configured: where it starts, and what it grows back to
	inTime             int // calls in a row that answered in under half their time
	timeouts           int64
	lastCall           time.Duration
	down               bool // stepped down since it was last at full, for the log
}

func newPace(batch, slots int) *pace {
	return &pace{batch: batch, inFlight: slots, maxBatch: batch, maxSlots: slots}
}

// now is the batch size and calls in flight a round sends.
func (p *pace) now() (batch, inFlight int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.batch, p.inFlight
}

// step is what a step of the pace left: the size and calls in flight now, whether this is the first
// step down since it was at full, and whether it is back at full.
type step struct {
	batch, inFlight int
	first, full     bool
}

// timedOut records a call of size texts, sent at inFlight, that ran out of its time after took.
// It steps the pace down, halving the batch and the calls in flight, when the call was sent at the
// pace as it is now; a call sent at a larger pace than now (another batch of the same round
// stepped it down already) steps nothing more. ok false: the pace is at its floor, one text and
// one call, and the provider fails even so.
func (p *pace) timedOut(size, inFlight int, took time.Duration) (s step, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.timeouts++
	p.lastCall = took
	p.inTime = 0
	if size > p.batch || inFlight > p.inFlight {
		return step{batch: p.batch, inFlight: p.inFlight}, true // stepped already by its round's other calls
	}
	if p.batch == 1 && p.inFlight == 1 {
		return step{batch: 1, inFlight: 1}, false
	}
	p.batch, p.inFlight = max(1, p.batch/2), max(1, p.inFlight/2)
	first := !p.down
	p.down = true
	return step{batch: p.batch, inFlight: p.inFlight, first: first}, true
}

// answered records a call that answered after took, of timeout. growAfter calls in a row in under
// half their time step the pace up once: the batch doubles, else the calls in flight rise by one,
// to the configured values. ok says it stepped up; full that it is back at the configured pace.
func (p *pace) answered(took, timeout time.Duration) (s step, ok bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastCall = took
	if took >= timeout/2 {
		p.inTime = 0
		return step{}, false
	}
	p.inTime++
	if p.inTime < growAfter || (p.batch == p.maxBatch && p.inFlight == p.maxSlots) {
		return step{}, false
	}
	p.inTime = 0
	if p.batch < p.maxBatch {
		p.batch = min(2*p.batch, p.maxBatch)
	} else {
		p.inFlight++
	}
	full := p.batch == p.maxBatch && p.inFlight == p.maxSlots && p.down
	if full {
		p.down = false
	}
	return step{batch: p.batch, inFlight: p.inFlight, full: full}, true
}

// paceState is the pace for an operator (Health).
type paceState struct {
	batch, inFlight int
	lastCall        time.Duration
	timeouts        int64
}

func (p *pace) state() paceState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return paceState{batch: p.batch, inFlight: p.inFlight, lastCall: p.lastCall, timeouts: p.timeouts}
}

// steppedDownOnly reports whether err is one or more batches that stepped the pace down, and
// nothing else.
func steppedDownOnly(err error) bool {
	if err == nil {
		return false
	}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs := j.Unwrap()
		if len(errs) == 0 {
			return false
		}
		for _, e := range errs {
			if !steppedDownOnly(e) {
				return false
			}
		}
		return true
	}
	return errors.Is(err, errSteppedDown)
}
