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
	if p == nil {
		return nil
	}
	return &providerSlots{provider: p, slots: make(chan struct{}, limit)}
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
