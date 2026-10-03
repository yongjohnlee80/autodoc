package daemon

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/embed"
)

// providerSlots is one admission limit for document batches AND query embeds.
// The queue determines which workspace fills next; this wrapper also bounds a
// simultaneous search query that uses the same provider independently of it.
type providerSlots struct {
	provider embed.Provider
	slots    chan struct{}
}

func withProviderSlots(p embed.Provider, limit int) embed.Provider {
	return withSharedSlots(p, make(chan struct{}, limit))
}

// withSharedSlots wraps p in the daemon's one set of slots: a workspace's own provider and the
// daemon's draw from the same limit, so a second model never doubles the requests in flight.
func withSharedSlots(p embed.Provider, slots chan struct{}) embed.Provider {
	if p == nil {
		return nil
	}
	return &providerSlots{provider: p, slots: slots}
}

func (p *providerSlots) Name() string       { return p.provider.Name() }
func (p *providerSlots) Model() embed.Model { return p.provider.Model() }
func (p *providerSlots) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-p.slots }()
	return p.provider.Embed(ctx, texts)
}
