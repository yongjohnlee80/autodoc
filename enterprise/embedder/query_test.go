package embedder_test

import (
	"context"
	"errors"
	"math"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/vector"

	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
)

// A query is embedded with the worker's provider and answered with its model's fingerprint and
// the normalized vector, as the worker stores the texts'. Before the provider is set up, for a
// tenant the worker does not embed, and after the worker stops, it is search.ErrEmbedFailed, so
// a search answers by words. The mutants, named here: the vector not normalized; the queries
// still answered by the provider after Run returns.
func TestAQueryIsEmbeddedInTheWorkersModel(t *testing.T) {
	st := newStore()
	st.put("a.md", "alpha")
	p := newProvider("m")
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}})
	ctx := context.Background()
	if _, _, err := w.QueryEmbedder("t")(ctx, "q"); !errors.Is(err, search.ErrEmbedFailed) {
		t.Fatalf("before Run: %v, want ErrEmbedFailed", err)
	}
	stop := run(t, w)
	var model string
	var vec []float32
	waitFor(t, "a query embedded", func() bool {
		var err error
		model, vec, err = w.QueryEmbedder("t")(ctx, "a question")
		return err == nil
	})
	if model != fingerprint("m") {
		t.Errorf("model %q, want %q", model, fingerprint("m"))
	}
	if want := vector.Normalize(vec_("a question")); !slices.Equal(vec, want) {
		t.Errorf("vector %v, want the normalized %v", vec, want)
	}
	if n := norm(vec); math.Abs(n-1) > 1e-5 {
		t.Errorf("the vector's length is %v, want 1", n)
	}
	if _, _, err := w.QueryEmbedder("other")(ctx, "q"); !errors.Is(err, search.ErrEmbedFailed) {
		t.Errorf("another tenant: %v, want ErrEmbedFailed", err)
	}
	stop()
	if _, _, err := w.QueryEmbedder("t")(ctx, "q"); !errors.Is(err, search.ErrEmbedFailed) {
		t.Errorf("after Run returned: %v, want ErrEmbedFailed", err)
	}
}

func vec_(text string) []float32 { return vec(text, 4) }

func norm(v []float32) float64 {
	var s float64
	for _, x := range v {
		s += float64(x) * float64(x)
	}
	return math.Sqrt(s)
}

// While its tenant switches models, a query is search.ErrSwitching, without a provider call, so a
// search answers by words with semantic "switching"; once the switch flips, the query is embedded
// with the new model. The mutant, named here: the switch not published (the query embedded with
// the new model while the old one is still active).
func TestAQueryWhileItsTenantSwitchesIsSwitching(t *testing.T) {
	st := newStore()
	st.put("a.md", "alpha")
	st.active = "fake|old||4"
	p := newProvider("m")
	p.hold = make(chan struct{})
	w := embedder.New(built(p), []embedder.Tenant{{Name: "t", Store: st}})
	run(t, w)
	ctx := context.Background()
	waitFor(t, "the switch begun", func() bool { _, tg, _ := st.Models(ctx); return tg == fingerprint("m") })
	calls := p.callCount()
	if _, _, err := w.QueryEmbedder("t")(ctx, "q"); !errors.Is(err, search.ErrSwitching) {
		t.Fatalf("mid-switch: %v, want ErrSwitching", err)
	}
	if p.callCount() != calls {
		t.Error("a query mid-switch called the provider")
	}
	close(p.hold)
	waitFor(t, "the query embedded after the flip", func() bool {
		model, _, err := w.QueryEmbedder("t")(ctx, "q")
		return err == nil && model == fingerprint("m")
	})
	if a, tg, _ := st.Models(ctx); a != fingerprint("m") || tg != "" {
		t.Errorf("after the flip: %q %q", a, tg)
	}
}

// gated is a provider whose calls each wait for a token, and that says which text each call
// carried as it arrives.
type gated struct {
	*provider
	tokens  chan struct{}
	arrived chan string
}

func (g *gated) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	g.arrived <- texts[0]
	select {
	case <-g.tokens:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return g.provider.Embed(ctx, texts)
}

// With one slot, held by a batch, a query asked while the next batch waits for the slot is the
// next call: the query goes ahead of the worker's batches. The mutant, named here: the query
// waiting as a batch (the next batch's text arrives first).
func TestAQueryGoesAheadOfTheWaitingBatches(t *testing.T) {
	st := newStore()
	for _, d := range []string{"a", "b", "c"} {
		st.put(d+".md", "text of "+d)
	}
	g := &gated{provider: newProvider("m"), tokens: make(chan struct{}), arrived: make(chan string, 10)}
	w := embedder.New(func(context.Context) (embed.Provider, error) { return g, nil },
		[]embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(1), embedder.WithSlots(1))
	run(t, w)
	first := <-g.arrived // a batch holds the only slot; the next waits for it
	if first == "the question" {
		t.Fatal("the query arrived before any batch")
	}
	answered := make(chan error, 1)
	go func() {
		_, _, err := w.QueryEmbedder("t")(context.Background(), "the question")
		answered <- err
	}()
	time.Sleep(100 * time.Millisecond) // the query waits behind the held batch
	g.tokens <- struct{}{}             // the batch is done: its slot is handed on
	select {
	case next := <-g.arrived:
		if next != "the question" {
			t.Fatalf("after the held batch, %q was embedded first, before the waiting query", next)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("nothing arrived after the held batch")
	}
	g.tokens <- struct{}{}
	if err := <-answered; err != nil {
		t.Fatal(err)
	}
	go func() {
		for range g.arrived {
			g.tokens <- struct{}{}
		}
	}()
}

// Queries asked without pause across the worker's whole life (a provider that fails to set up
// twice, then a switch from another model, its fill, the flip, and the worker stopping) each
// answer coherently: ErrEmbedFailed, ErrSwitching, or the new model's normalized vector, never the
// new model's vector while the old one is active. Under -race, the race detector watches the
// published state. The mutant, named here: the provider set without publishing (a query reading
// the worker's fields).
func TestQueriesAcrossTheWorkersLifeAreCoherent(t *testing.T) {
	restore := embedder.SetSleep(func(ctx context.Context, _ time.Duration) bool { return ctx.Err() == nil })
	t.Cleanup(restore)
	st := newStore()
	for i := range 20 {
		st.put(string(rune('a'+i))+".md", "text "+string(rune('a'+i)))
	}
	st.active = "fake|old||4"
	p := newProvider("m")
	var builds atomic.Int32
	build := func(context.Context) (embed.Provider, error) {
		if builds.Add(1) <= 2 {
			return nil, errors.New("the fake provider is not up yet")
		}
		return p, nil
	}
	w := embedder.New(build, []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithBatch(2), embedder.WithSlots(2))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	var bad atomic.Value
	var embedded, switching atomic.Int64
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for ctx.Err() == nil {
				model, v, err := w.QueryEmbedder("t")(context.Background(), "q")
				switch {
				case errors.Is(err, search.ErrEmbedFailed):
				case errors.Is(err, search.ErrSwitching):
					switching.Add(1)
				case err != nil:
					bad.Store("an error that is neither: " + err.Error())
				case model != fingerprint("m") || len(v) != 4:
					bad.Store("the vector of " + model)
				default:
					if a, tg, _ := st.Models(context.Background()); a != fingerprint("m") && tg == fingerprint("m") {
						// the flip publishes after the record says the new model is active; a query
						// answered with it while the record still says switching would mix models
						bad.Store("the new model's vector while the old one is active")
					}
					embedded.Add(1)
				}
			}
		}()
	}
	stop := run(t, w)
	waitFor(t, "queries embedded after the flip", func() bool { return embedded.Load() > 10 })
	stop()
	cancel()
	wg.Wait()
	if b := bad.Load(); b != nil {
		t.Fatal(b)
	}
	if switching.Load() == 0 {
		t.Error("no query saw the switch: the life was not exercised")
	}
	if _, _, err := w.QueryEmbedder("t")(context.Background(), "q"); !errors.Is(err, search.ErrEmbedFailed) {
		t.Errorf("after the worker stopped: %v", err)
	}
}
