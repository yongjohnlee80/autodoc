package daemon

import (
	"context"

	"github.com/yongjohnlee80/golib/search/embed"
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
func (p *providerSlots) Embed(ctx context.Context, texts []string) (vecs [][]float32, err error) {
	err = holdSlot(ctx, p.slots, func() error {
		vecs, err = p.provider.Embed(ctx, texts)
		return err
	})
	return vecs, err
}

// holdSlot runs fn holding one of slots, waiting for it until ctx ends.
func holdSlot(ctx context.Context, slots chan struct{}, fn func() error) error {
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-slots }()
	return fn()
}
