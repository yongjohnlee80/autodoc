package embedder_test

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
)

// THE PACE (pace.go) — a provider too slow for the configured batch is asked for less at once,
// so some size answers within the call's time and the texts get embedded; calls that answer in
// well under their time step the pace back up. These cells run a real call timeout, short: a
// context's deadline is the provider's clock, which no fake clock drives.

// paceTimeout is the cells' call timeout.
const paceTimeout = 100 * time.Millisecond

// paceStore is a store of docs documents of per texts each, every text distinct; it returns the
// store, the paths and the texts.
func paceStore(docs, per int) (*store, []string, []string) {
	st := newStore()
	var paths, all []string
	for d := range docs {
		texts := make([]string, per)
		for i := range texts {
			texts[i] = fmt.Sprintf("document %d, text %d", d, i)
		}
		p := fmt.Sprintf("d%04d.md", d)
		st.put(p, texts...)
		paths, all = append(paths, p), append(all, texts...)
	}
	return st, paths, all
}

// logged is a worker's log, kept.
type logged struct {
	mu    sync.Mutex
	lines []string
}

func (l *logged) logger() logger.Logger {
	return logger.Adapt(func(sev logger.Severity, payload any) {
		l.mu.Lock()
		defer l.mu.Unlock()
		l.lines = append(l.lines, fmt.Sprintf("%s: %v", sev, payload))
	})
}

// with are the lines that hold s.
func (l *logged) with(s string) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []string
	for _, x := range l.lines {
		if strings.Contains(x, s) {
			out = append(out, x)
		}
	}
	return out
}

func allReady(st *store, paths []string) func() bool {
	return func() bool {
		for _, p := range paths {
			if !st.isReady(p) {
				return false
			}
		}
		return true
	}
}

// A provider that answers 8 texts in time and runs out of time on more: the worker, configured at
// 64 texts and 4 calls at once, steps down until calls answer, and every text ends embedded, none
// twice, with no failure taken (a failure would wait the hour of its retry). The state counts the
// calls that ran out; the log says so once, with what to change. The mutant, named here: a call
// that ran out of its time taken as the provider failing (no step-down: nothing is embedded).
func TestASlowProviderIsAskedForLessUntilItAnswersInTime(t *testing.T) {
	st, paths, texts := paceStore(40, 5)
	p := newProvider("m")
	p.slowAbove = 8
	var log logged
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(64), embedder.WithSlots(4),
		embedder.WithCallTimeout(paceTimeout), embedder.WithRetry(time.Hour, time.Hour), embedder.WithLogger(log.logger()))
	stop := run(t, w)
	waitFor(t, "every document ready", allReady(st, paths))
	stop()
	for _, x := range texts {
		if n := p.times(x); n != 1 {
			t.Errorf("%q embedded %d times, want once", x, n)
		}
	}
	if got := p.largestAnswered(); got > 8 {
		t.Errorf("a call of %d texts answered, from a provider that answers 8 at most", got)
	}
	h := w.Health()
	if h.State != embedder.ProviderUp {
		t.Errorf("the provider %s (%s), want up: a call that ran out of time is no failure while the pace can step down", h.State, h.Error)
	}
	if want := int64(p.slowCalls()); h.Timeouts != want || want == 0 {
		t.Errorf("the state counts %d calls that ran out of time, the provider %d", h.Timeouts, want)
	}
	if h.Batch > 16 || h.InFlight != 1 {
		t.Errorf("the pace at %d texts, %d at once; want 8 or 16 texts, one call", h.Batch, h.InFlight)
	}
	if got := log.with("took longer than"); len(got) != 1 || !strings.Contains(got[0], "set [embedding] batch or call_timeout") {
		t.Errorf("the step-down logged %q, want one line naming what to change", got)
	}
	if got := log.with("trying again"); len(got) != 0 {
		t.Errorf("the worker failed: %q", got)
	}
}

// A provider slow at first, then fast: the pace steps down while it is slow, and once calls
// answer in under half their time it grows back, the batch first, to the configured 64 texts and
// 4 calls at once, logged once; every text ends embedded, none twice. The mutant, named here: a
// pace that never steps back up.
func TestAPaceSteppedDownGrowsBackOnceCallsAnswerInTime(t *testing.T) {
	st, paths, texts := paceStore(300, 10)
	p := newProvider("m")
	p.slowAbove = 8
	var log logged
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(64), embedder.WithSlots(4),
		embedder.WithCallTimeout(paceTimeout), embedder.WithRetry(time.Hour, time.Hour), embedder.WithLogger(log.logger()))
	stop := run(t, w)
	waitFor(t, "the pace stepped down to 16 texts or fewer", func() bool { return w.Health().Batch <= 16 })
	p.setSlowAbove(0)
	waitFor(t, "the pace back at 64 texts, 4 at once", func() bool {
		h := w.Health()
		return h.Batch == 64 && h.InFlight == 4
	})
	waitFor(t, "every document ready", allReady(st, paths))
	stop()
	if got := log.with("calls answer in time again: back to 64 texts, 4 at once"); len(got) != 1 {
		t.Errorf("the recovery logged %q, want once", got)
	}
	for _, x := range texts {
		if n := p.times(x); n != 1 {
			t.Fatalf("%q embedded %d times, want once", x, n)
		}
	}
	if got := p.mostInFlight(); got > 4 {
		t.Errorf("%d calls in flight, above the 4 slots", got)
	}
}

// A provider that never answers, even one text: the pace steps down to its floor, one text and one
// call, where a call that runs out of its time is the provider failing. The state says so (down,
// at the floor), and the worker waits out its retry: no call is made while it waits. The mutant,
// named here: no step-down (the provider is down at 64 texts, 4 at once, the floor never tried).
func TestAProviderThatNeverAnswersEndsDownWithNothingSpinning(t *testing.T) {
	st, _, _ := paceStore(10, 5)
	p := newProvider("m")
	p.slowAbove = -1
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(64), embedder.WithSlots(4),
		embedder.WithCallTimeout(paceTimeout), embedder.WithRetry(time.Hour, time.Hour))
	run(t, w)
	waitFor(t, "the provider down at the floor", func() bool {
		h := w.Health()
		return h.State == embedder.ProviderDown && h.Batch == 1 && h.InFlight == 1
	})
	calls := p.callCount()
	time.Sleep(5 * paceTimeout)
	if got := p.callCount(); got != calls {
		t.Errorf("%d calls while the worker waits out its retry, want none", got-calls)
	}
	if p.texts() != 0 {
		t.Errorf("%d texts embedded by a provider that never answers", p.texts())
	}
}

// A provider that answers 8 texts in time and never 16: once the pace has stepped down to 8, it
// asks for more than 8 again only after many calls at 8 have answered in time (the pace that ran
// out is remembered), not every few calls, each such call lost to its timeout; every text ends
// embedded, none twice. The mutant, named here: the pace that ran out forgotten (16 tried again
// after growAfter calls, a swing between 8 and 16 for as long as the load lasts).
func TestAPaceThatRanOutIsTriedAgainOnlyAfterManyCallsInTime(t *testing.T) {
	st, paths, texts := paceStore(120, 5)
	p := newProvider("m")
	p.slowAbove = 8
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(64), embedder.WithSlots(1),
		embedder.WithCallTimeout(paceTimeout), embedder.WithRetry(time.Hour, time.Hour))
	stop := run(t, w)
	waitFor(t, "every document ready", allReady(st, paths))
	stop()
	for _, x := range texts {
		if n := p.times(x); n != 1 {
			t.Fatalf("%q embedded %d times, want once", x, n)
		}
	}
	sizes := p.callSizes()
	at := slices.IndexFunc(sizes, func(n int) bool { return n <= 8 })
	if at < 0 {
		t.Fatalf("no call of 8 texts or fewer: %v", sizes)
	}
	run := 0 // calls of 8 or fewer since the pace reached 8, or since the last call above it
	for i, n := range sizes[at:] {
		if n <= 8 {
			run++
			continue
		}
		if run < embedder.CeilingAfter {
			t.Fatalf("call %d asked for %d texts after %d calls of 8 or fewer, want at least %d (the sizes from the step-down: %v)",
				at+i, n, run, embedder.CeilingAfter, sizes[at:])
		}
		run = 0
	}
}
