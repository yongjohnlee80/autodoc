package embedder

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/vector"
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
		if err != nil {
			return "", nil, fmt.Errorf("%w: %w", search.ErrEmbedFailed, err)
		}
		if len(vecs) != 1 {
			return "", nil, fmt.Errorf("%w: the provider answered %d vectors for one query", search.ErrEmbedFailed, len(vecs))
		}
		return st.fp, vector.Normalize(vecs[0]), nil
	}
}
