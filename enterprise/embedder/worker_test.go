package embedder_test

import (
	"context"
	"errors"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search/embed"

	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
)

// run runs w until the test ends, or until the stop it returns is called.
func run(t *testing.T, w *embedder.Worker) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.Run(ctx)
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}

// The slots bound the provider calls in flight, though a round reads more batches than there are
// slots: two of eight held calls arrive, never a third, and all are embedded once let go. The
// mutant, named here: a batch started without taking a slot (eight arrive at once).
func TestTheSlotsBoundTheCallsInFlight(t *testing.T) {
	st := newStore()
	for _, p := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
		st.put(p+".md", "text of "+p)
	}
	p := newProvider("m")
	p.hold, p.arrived = make(chan struct{}), make(chan struct{}, 100)
	run(t, embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(1), embedder.WithSlots(2)))
	for range 2 {
		select {
		case <-p.arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("two calls did not arrive")
		}
	}
	select {
	case <-p.arrived:
		t.Fatal("a third call arrived with two slots, both held")
	case <-time.After(200 * time.Millisecond):
	}
	close(p.hold)
	waitFor(t, "every document ready", func() bool {
		for _, d := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i", "j"} {
			if !st.isReady(d + ".md") {
				return false
			}
		}
		return true
	})
	if got := p.mostInFlight(); got != 2 {
		t.Errorf("at most %d calls in flight, want the 2 slots", got)
	}
}

// A provider that cannot be set up, and then one that fails, is asked again after a wait that
// doubles from the first to the longest; once it answers, the texts are embedded. The mutants,
// named here: a wait that does not double, and one that is not capped at the longest.
func TestAFailingProviderIsAskedAgainAfterADoublingWait(t *testing.T) {
	var mu sync.Mutex
	var waits []time.Duration
	t.Cleanup(embedder.SetSleep(func(ctx context.Context, d time.Duration) bool {
		mu.Lock()
		waits = append(waits, d)
		mu.Unlock()
		return ctx.Err() == nil
	}))
	st := newStore()
	st.put("a.md", "alpha")
	p := newProvider("m")
	p.fail = 4
	builds := 0
	build := func(context.Context) (embed.Provider, error) {
		if builds++; builds <= 3 {
			return nil, embed.ErrUnreachable
		}
		return p, nil
	}
	run(t, embedder.New(build, []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithRetry(time.Second, 4*time.Second)))
	waitFor(t, "a.md ready", func() bool { return st.isReady("a.md") })
	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, // setting up
		time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} // embedding
	if !slices.Equal(waits, want) {
		t.Errorf("the waits %v, want %v", waits, want)
	}
	if p.times("alpha") != 1 {
		t.Errorf("alpha embedded %d times", p.times("alpha"))
	}
}

// A text the provider rejects is set aside, and the rest of its batch and its document's other
// texts are stored; its document waits, unready, until the text's time is up, when it is tried
// again and the document completes. The mutant, named here: a rejection not set aside (the text
// is asked for again at once, and its document completes before its time).
func TestARejectedTextIsSetAsideUntilItsTime(t *testing.T) {
	var mu sync.Mutex
	now := time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)
	clock := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	st := newStore()
	st.put("a.md", "good one", "bad text")
	st.put("b.md", "good two")
	p := newProvider("m")
	p.reject["bad text"] = true // once: asked again, it answers
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithClock(clock),
		embedder.WithRefusedFor(time.Hour), embedder.WithPoll(20*time.Millisecond))
	run(t, w)
	waitFor(t, "b.md ready", func() bool { return st.isReady("b.md") })
	waitFor(t, "the rejected text skipped", func() bool { return st.lastSkip()[hashOf("bad text")] })
	if !st.has(fingerprint("m"), "good one") || st.isReady("a.md") || p.times("bad text") != 0 {
		t.Fatalf("after the rejection: good one stored %v, a.md ready %v, bad text embedded %d times",
			st.has(fingerprint("m"), "good one"), st.isReady("a.md"), p.times("bad text"))
	}
	time.Sleep(100 * time.Millisecond) // polls pass: the text stays aside
	if st.isReady("a.md") {
		t.Fatal("a.md completed before its text's time was up")
	}
	mu.Lock()
	now = now.Add(time.Hour + time.Minute)
	mu.Unlock()
	w.Wake()
	waitFor(t, "a.md ready once the text's time is up", func() bool { return st.isReady("a.md") })
	if p.times("bad text") != 1 {
		t.Errorf("the rejected text embedded %d times", p.times("bad text"))
	}
}

// A tenant this process does not hold is left alone: no model recorded and nothing embedded or
// written while Confirm refuses; once it confirms, the tenant is embedded. The mutant, named here:
// the round not confirming the tenant first.
func TestATenantNotThisServersIsLeftAlone(t *testing.T) {
	st := newStore()
	st.put("a.md", "alpha")
	p := newProvider("m")
	var held atomic.Bool
	notHeld := errors.New("the tenant's writer lock is not held")
	confirm := func(context.Context) error {
		if !held.Load() {
			return notHeld
		}
		return nil
	}
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st, Confirm: confirm}}, embedder.WithPoll(20*time.Millisecond))
	run(t, w)
	time.Sleep(200 * time.Millisecond) // polls pass
	if st.writeCount() != 0 || p.callCount() != 0 {
		t.Fatalf("a tenant not held: %d writes, %d provider calls", st.writeCount(), p.callCount())
	}
	held.Store(true)
	waitFor(t, "a.md ready once held", func() bool { return st.isReady("a.md") })
	if a, tg, _ := st.Models(context.Background()); a != fingerprint("m") || tg != "" {
		t.Errorf("models %q %q", a, tg)
	}
}

// A document is marked ready as soon as its texts are stored, not when the whole tenant is: with
// the fifth call held, the four documents of the first round are ready and the rest are not. The
// mutant, named here: no marking after a round's vectors are stored (nothing is ready until
// nothing is pending).
func TestADocumentIsReadyOnceItsTextsAreStored(t *testing.T) {
	st := newStore()
	docs := []string{"a.md", "b.md", "c.md", "d.md", "e.md", "f.md"}
	for _, d := range docs {
		st.put(d, "text of "+d)
	}
	p := newProvider("m")
	p.arrived = make(chan struct{}) // each call waits until the test lets it in
	run(t, embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(1), embedder.WithSlots(1)))
	for range 4 {
		select {
		case <-p.arrived:
		case <-time.After(5 * time.Second):
			t.Fatal("the first round's calls did not arrive")
		}
	}
	waitFor(t, "the first round's documents ready", func() bool {
		return st.isReady("a.md") && st.isReady("b.md") && st.isReady("c.md") && st.isReady("d.md")
	})
	if st.isReady("e.md") || st.isReady("f.md") {
		t.Fatal("a document whose text was not embedded is ready")
	}
	drained := make(chan struct{})
	t.Cleanup(func() { close(drained) })
	go func() {
		for {
			select {
			case <-p.arrived:
			case <-drained:
				return
			}
		}
	}()
	waitFor(t, "every document ready", func() bool { return st.isReady("e.md") && st.isReady("f.md") })
}
