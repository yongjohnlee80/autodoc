package embedder_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/search/embed"

	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// The provider's health is published for an operator: starting before Run; down with its code
// while it cannot be set up, with no model yet; up with the model once it is; down with its code
// when its calls fail; up again when they succeed. The mutants, named here: a failure not
// published; a recovery not published.
func TestTheProvidersHealthIsPublished(t *testing.T) {
	holding := atomic.Bool{}
	holding.Store(true)
	release := make(chan struct{})
	restore := embedder.SetSleep(func(ctx context.Context, _ time.Duration) bool {
		if holding.Load() {
			select {
			case <-release:
			case <-ctx.Done():
				return false
			}
		}
		return ctx.Err() == nil
	})
	t.Cleanup(restore)
	st := newStore()
	st.put("a.md", "alpha")
	p := newProvider("m")
	var builds atomic.Int32
	build := func(context.Context) (embed.Provider, error) {
		if builds.Add(1) == 1 {
			return nil, fmt.Errorf("%w: not yet", embed.ErrUnreachable)
		}
		return p, nil
	}
	w := embedder.New(build, []embedder.Tenant{{Name: "t", Store: st}}, embedder.WithPoll(20*time.Millisecond))
	if h := w.Health(); h.State != embedder.ProviderStarting {
		t.Fatalf("before Run: %+v", h)
	}
	run(t, w)
	waitFor(t, "the first setup's failure", func() bool { return w.Health().State == embedder.ProviderDown })
	if h := w.Health(); h.Error != opcode.ProviderUnreachable || h.Model != "" {
		t.Errorf("a setup that failed: %+v", h)
	}
	holding.Store(false)
	close(release)
	waitFor(t, "up", func() bool {
		h := w.Health()
		return h.State == embedder.ProviderUp && h.Model == fingerprint("m") && h.Error == ""
	})

	p.mu.Lock()
	p.fail = 1 << 20
	p.mu.Unlock()
	st.put("b.md", "beta")
	w.Wake()
	waitFor(t, "down", func() bool {
		h := w.Health()
		return h.State == embedder.ProviderDown && h.Error == opcode.ProviderUnreachable
	})
	p.mu.Lock()
	p.fail = 0
	p.mu.Unlock()
	w.Wake()
	waitFor(t, "up again", func() bool { h := w.Health(); return h.State == embedder.ProviderUp && h.Error == "" })
}
