package embedder

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/vector"

	"github.com/yongjohnlee80/autodoc/enterprise/opcode"
)

// queryState is what a search's query needs of the worker, published whole by Run's goroutine and
// never modified after: the provider, its model's fingerprint, and each tenant's say on whether
// its active model is that one. Queries read only a published state, so the provider, the
// fingerprint and a tenant's switch are always one another's.
type queryState struct {
	p  embed.Provider
	fp string
	// switching is, by tenant, whether its queries answer by words: its models not read yet, or
	// the worker's model filling to replace another active one.
	switching map[string]bool
}

// publish stores the state Run's goroutine holds now for the queries to read. Run's goroutine
// alone calls it.
func (w *Worker) publish() {
	st := &queryState{p: w.p, fp: w.fp, switching: make(map[string]bool, len(w.tenants))}
	for _, t := range w.tenants {
		st.switching[t.Name] = !t.prepared || t.switching
	}
	w.qs.Store(st)
}

// QueryEmbedder embeds tenant's search queries with the worker's provider, in the worker's slots,
// ahead of its batches: the fingerprint and the normalized vector, as the worker stores the
// texts'. With the provider not set up it answers search.ErrEmbedFailed, and while the tenant
// switches models search.ErrSwitching, so a search answers by words; a tenant this worker does not
// embed is search.ErrEmbedFailed too. It may be called from any goroutine.
func (w *Worker) QueryEmbedder(tenant string) search.QueryEmbedder {
	return func(ctx context.Context, q string) (string, []float32, error) {
		st := w.qs.Load()
		if st == nil {
			return "", nil, fmt.Errorf("%w: the embedding provider is not set up", search.ErrEmbedFailed)
		}
		switching, ours := st.switching[tenant]
		switch {
		case !ours:
			return "", nil, fmt.Errorf("%w: tenant %s is not embedded by this server", search.ErrEmbedFailed, tenant)
		case switching:
			return "", nil, fmt.Errorf("%w: tenant %s is switching to %s", search.ErrSwitching, tenant, st.fp)
		}
		if err := w.lim.acquire(ctx, true); err != nil {
			return "", nil, err
		}
		defer w.lim.release()
		vecs, err := st.p.Embed(ctx, []string{q})
		if ctx.Err() == nil {
			w.answered(st.fp, err) // a query that ran out its own time says nothing of the provider
		}
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", search.ErrEmbedFailed, err)
		}
		if len(vecs) != 1 {
			return "", nil, fmt.Errorf("%w: the provider answered %d vectors for one query", search.ErrEmbedFailed, len(vecs))
		}
		return st.fp, vector.Normalize(vecs[0]), nil
	}
}

// The provider's states, as Health reports them.
const (
	ProviderStarting = "starting" // not set up yet
	ProviderUp       = "up"       // set up, and its last call succeeded
	ProviderDown     = "down"     // failing: searches answer by words, and the worker keeps trying
)

// Health is the provider's state as an operator sees it (enterprise/ops): starting, up or down, the
// model's fingerprint once it is set up, and the last failure's code, never its text.
type Health struct {
	State string
	Model string
	Error opcode.Code
}

// Health is the provider's state now. It may be called from any goroutine.
func (w *Worker) Health() Health {
	if h := w.health.Load(); h != nil {
		return *h
	}
	return Health{State: ProviderStarting}
}

// answered publishes how the provider's last call went: up when it answered (err nil), down with
// err's code when it failed. Only a provider call (its setup, a batch, a query) publishes, so a round
// that called nothing leaves the health as the last call left it. model is the fingerprint the
// caller holds ("" before the provider is set up); any goroutine may call it.
func (w *Worker) answered(model string, err error) {
	h := Health{State: ProviderUp, Model: model}
	if err != nil {
		h.State, h.Error = ProviderDown, providerCode(err)
	}
	w.health.Store(&h)
}

// providerCode is a worker's failure as an operator's code: the provider's three, the index's
// database (every store call is one), or other.
func providerCode(err error) opcode.Code {
	switch {
	case errors.Is(err, embed.ErrUnauthorized):
		return opcode.ProviderRefused
	case errors.Is(err, embed.ErrUnreachable):
		return opcode.ProviderUnreachable
	case errors.Is(err, embed.ErrRejected):
		return opcode.ProviderRejected
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return opcode.Other
	}
	var dbErr interface{ SQLState() string }
	var netErr *net.OpError
	if errors.As(err, &dbErr) || errors.As(err, &netErr) {
		return opcode.Database // the provider's own network failures are ErrUnreachable, above
	}
	return opcode.Other
}
