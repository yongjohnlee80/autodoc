package embedder

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search/embed"

	"github.com/yongjohnlee80/autodoc/core/clock"
	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// The defaults of the options.
const (
	DefaultBatch      = 64               // texts in one provider call
	DefaultSlots      = 4                // provider calls in flight at once, across every tenant
	DefaultRetry      = time.Second      // the first wait after a failure; it doubles from there
	DefaultMaxRetry   = time.Minute      // the longest wait after failures
	DefaultPoll       = 30 * time.Second // how often the tenants are looked at with no wake
	DefaultRefusedFor = time.Hour        // how long a text the provider rejected is set aside
)

const (
	// batchTimeout bounds one provider call, and setupTimeout the provider's setup (its probe).
	batchTimeout = 2 * time.Minute
	setupTimeout = 30 * time.Second
	// roundBatches is how many batches a tenant's round reads from Pending at once, so a round
	// can keep every slot busy; the slots, not the read, bound the calls in flight.
	roundBatches = 4
	// pageSize is how many documents one read of Docs or Unready returns.
	pageSize = 500
)

// Store is a tenant's index as the worker reads and writes it: a *pgstore.Store.
type Store interface {
	Models(ctx context.Context) (active, target string, err error)
	SetModels(ctx context.Context, active, target string) error
	ReclaimModels(ctx context.Context) (int, error)
	Pending(ctx context.Context, q pgstore.PendingQuery) ([]pgstore.Text, pgstore.PendingKey, error)
	Embed(ctx context.Context, model string, vecs []pgstore.Embedding) error
	MarkReady(ctx context.Context, path, model string) (bool, error)
	Docs(ctx context.Context, after string, limit int) ([]pgstore.DocInfo, bool, error)
	Unready(ctx context.Context, after string, limit int) ([]pgstore.DocInfo, bool, error)
	DocsWithTexts(ctx context.Context, hashes [][32]byte) ([]string, error)
}

var _ Store = (*pgstore.Store)(nil)

// Tenant is one tenant the worker embeds.
type Tenant struct {
	Name  string
	Store Store
	// Confirm answers nil while this process may write the tenant: the server's ingester holds the
	// tenant's writer lock (ingest.Ingester.Confirm). The worker confirms it before each of its
	// writes, and leaves a tenant it is not confirmed for alone, so a second server waiting for
	// the tenant never switches its model or writes its vectors. nil: always this process's.
	Confirm func(ctx context.Context) error
}

// Build sets up the enterprise's embedding provider, embed.NewOpenAI or embed.NewOllama, which
// probe the model for its size. Run calls it until it succeeds.
type Build func(ctx context.Context) (embed.Provider, error)

// Option configures a Worker.
type Option func(*options)

type options struct {
	batch, slots     int
	retry, maxRetry  time.Duration
	poll, refusedFor time.Duration
	log              logger.Logger
	clock            clock.Clock
}

// WithBatch sets how many texts one provider call embeds (DefaultBatch). A value below 1 leaves
// the default.
func WithBatch(n int) Option { return func(o *options) { o.batch = n } }

// WithSlots sets how many provider calls may be in flight at once, across every tenant
// (DefaultSlots): the server's [embedding] slots. A value below 1 leaves the default.
func WithSlots(n int) Option { return func(o *options) { o.slots = n } }

// WithRetry sets the first wait after a failure and the longest (DefaultRetry, DefaultMaxRetry).
// A value of zero or less leaves its default.
func WithRetry(first, max time.Duration) Option {
	return func(o *options) { o.retry, o.maxRetry = first, max }
}

// WithPoll sets how often the tenants are looked at when nothing wakes the worker (DefaultPoll).
func WithPoll(d time.Duration) Option { return func(o *options) { o.poll = d } }

// WithRefusedFor sets how long a text the provider rejected is set aside before it is tried again
// (DefaultRefusedFor).
func WithRefusedFor(d time.Duration) Option { return func(o *options) { o.refusedFor = d } }

// WithLogger sets where the worker reports its model, its switches and its failures. nil: nowhere.
func WithLogger(l logger.Logger) Option { return func(o *options) { o.log = l } }

// WithClock sets the clock a rejected text's retry time is measured by. nil: time.Now.
func WithClock(c clock.Clock) Option { return func(o *options) { o.clock = c } }

// Worker is the server's one embedding worker: it embeds every tenant's chunk texts that have no
// vector under the enterprise's model, once per distinct text, and marks the documents they
// complete ready for semantic search. A model other than the tenant's active one is a switch: the
// worker fills it while searches answer by words, then flips the tenant to it and reclaims the old
// vectors.
type Worker struct {
	build   Build
	tenants []*tenant
	o       options
	lim     *limiter // the provider's slots, shared with the queries (QueryEmbedder)
	wake    chan struct{}

	// p, fp and dims are the provider, its model's fingerprint and its size: set by Run once the
	// provider is set up, before any round reads them.
	p    embed.Provider
	fp   string
	dims int

	// qs is what the queries read: published by Run's goroutine whenever the provider or a
	// tenant's switch changes, nil before the provider is set up and after Run returns.
	qs atomic.Pointer[queryState]
	// health is the provider's state for an operator (Health), published by Run's goroutine
	health atomic.Pointer[Health]
}

// tenant is a Tenant with what the worker knows of it.
type tenant struct {
	Tenant
	// prepared is the tenant's models read and recorded for this worker's model; switching is
	// that model being filled to replace another active one. Run's goroutine alone uses them.
	prepared, switching bool
	// waiting are the documents, by path, at the generation found incomplete when nothing was
	// pending but texts set aside: only a set-aside text's time running out can complete them, so
	// they are not looked at until then. Run's goroutine alone uses it.
	waiting map[string]int64
	// cursor is where the tenant's next Pending resumes: each round reads on from the last, so a
	// fill reads the tenant once, not once a round. The zero key is the start. Run's goroutine
	// alone uses it.
	cursor pgstore.PendingKey

	mu sync.Mutex // guards refused: a round's batches set texts aside concurrently
	// refused are the texts the provider rejected, by hash, and when each may be tried again.
	refused map[[32]byte]time.Time
}

// New is a worker embedding tenants with the provider build sets up. It does nothing until Run.
func New(build Build, tenants []Tenant, opts ...Option) *Worker {
	o := options{batch: DefaultBatch, slots: DefaultSlots, retry: DefaultRetry, maxRetry: DefaultMaxRetry,
		poll: DefaultPoll, refusedFor: DefaultRefusedFor}
	def := o
	for _, opt := range opts {
		opt(&o)
	}
	for _, f := range []struct{ v, def *int }{{&o.batch, &def.batch}, {&o.slots, &def.slots}} {
		if *f.v < 1 {
			*f.v = *f.def
		}
	}
	for _, f := range []struct{ v, def *time.Duration }{{&o.retry, &def.retry}, {&o.maxRetry, &def.maxRetry},
		{&o.poll, &def.poll}, {&o.refusedFor, &def.refusedFor}} {
		if *f.v <= 0 {
			*f.v = *f.def
		}
	}
	o.maxRetry = max(o.maxRetry, o.retry)
	if o.log == nil {
		o.log = logger.Adapt(func(logger.Severity, any) {})
	}
	w := &Worker{build: build, o: o, lim: newLimiter(o.slots), wake: make(chan struct{}, 1)}
	for _, t := range tenants {
		w.tenants = append(w.tenants, &tenant{Tenant: t, waiting: map[string]int64{}, refused: map[[32]byte]time.Time{}})
	}
	return w
}

// Wake asks the worker to look at its tenants now: an ingester's commit has written chunks. It
// never blocks, and wakes that come while the worker is busy are one wake.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// sleep is the worker's timed wait: a test replaces it to see the waits without taking them.
var sleep = clock.Sleep

// Run sets up the provider, then embeds until ctx ends: each tenant in turn, round after round
// while there is work, then waiting for a wake or the poll. A failure (the provider down, rate
// limited or refusing the key; the database) is logged and the worker waits before it tries
// again, the wait doubling up to the longest. Nothing it meets stops it, and nothing it does
// holds up ingestion: searches answer by words until the vectors are there.
func (w *Worker) Run(ctx context.Context) {
	defer w.qs.Store(nil) // no query reaches a provider the worker has stopped with
	if !w.setUp(ctx) {
		return
	}
	// NOTE: an ingester's commit wakes the worker at once (Wake). The poll covers what no wake
	// announces: a wake lost while the worker was busy, a rejected text's retry time passing, and
	// a tenant another server held being taken over by this one.
	poll := time.NewTicker(w.o.poll)
	defer poll.Stop()
	wait := w.o.retry
	for ctx.Err() == nil {
		worked, err := w.sweep(ctx)
		switch {
		case ctx.Err() != nil:
			return
		case err != nil:
			logger.Warning(w.o.log, err, fmt.Sprintf("embedding: trying again in %v", wait))
			// NOTE: a provider or a database that failed is not asked again at once: the wait
			// doubles from the first to the longest, and ctx ends it.
			if !sleep(ctx, wait) {
				return
			}
			wait = min(2*wait, w.o.maxRetry)
			continue
		}
		wait = w.o.retry
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-w.wake:
		case <-poll.C:
		}
	}
}

// setUp builds the provider, trying again after each failure as Run does; false when ctx ended
// first.
func (w *Worker) setUp(ctx context.Context) bool {
	wait := w.o.retry
	for {
		sctx, cancel := context.WithTimeout(ctx, setupTimeout)
		p, err := w.build(sctx)
		cancel()
		if err == nil {
			w.p, w.fp, w.dims = p, p.Model().Fingerprint(), p.Model().Dims
			w.publish()
			w.answered(w.fp, nil)
			logger.Info(w.o.log, fmt.Sprintf("embedding with %s: %d dimensions, %d slots", w.fp, w.dims, w.o.slots))
			return true
		}
		if ctx.Err() != nil {
			return false
		}
		w.answered("", err)
		logger.Warning(w.o.log, err, fmt.Sprintf("embedding: the provider is not set up, so searches answer by words; trying again in %v", wait))
		// NOTE: a provider that cannot be set up (down, or refusing the key or the model) is
		// asked again after a wait that doubles to the longest, so an outage at start costs
		// semantic search only until the provider answers, and ctx ends the wait.
		if !sleep(ctx, wait) {
			return false
		}
		wait = min(2*wait, w.o.maxRetry)
	}
}

// sweep runs one round of every tenant: whether any did work, and every tenant's failure.
func (w *Worker) sweep(ctx context.Context) (bool, error) {
	worked := false
	var errs []error
	for _, t := range w.tenants {
		did, err := w.round(ctx, t)
		worked = worked || did
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", t.Name, err))
		}
	}
	return worked, errors.Join(errs...)
}

// confirm is t's Confirm, nil when it has none.
func (t *tenant) confirm(ctx context.Context) error {
	if t.Confirm == nil {
		return nil
	}
	return t.Confirm(ctx)
}

// round does one round of t's work, reporting whether there is more:
//   - nothing while the tenant is not this server's;
//   - its models prepared, the first time;
//   - nothing when no document is unready but the ones waiting on set-aside texts;
//   - else up to roundBatches batches of its pending texts, embedded and stored, and the
//     documents they complete marked ready;
//   - with nothing pending: a switch flips, and otherwise the unready documents whose texts are
//     all embedded are marked ready.
func (w *Worker) round(ctx context.Context, t *tenant) (bool, error) {
	if err := t.confirm(ctx); err != nil {
		// another server's tenant for now (it holds the lock), or this one's lost (its ingester
		// stops the server): its models are read again when it is this server's
		if t.prepared {
			t.prepared = false
			w.publish()
		}
		return false, nil
	}
	if !t.prepared {
		if err := w.prepare(ctx, t); err != nil {
			return false, err
		}
	}
	skip, expired := t.skip(w.o.clock.Now())
	if expired {
		// the set-aside texts are behind the cursor: the scan begins again
		t.waiting, t.cursor = map[string]int64{}, pgstore.PendingKey{}
	}
	if !t.switching && !expired {
		// a tenant whose documents are all ready has nothing pending: Pending is not asked
		unready, err := w.anyUnready(ctx, t)
		if err != nil || !unready {
			return false, err
		}
	}
	// Under the active model only unready documents can hold a pending text, so the scan reads
	// those alone; a switch's target is missing from ready documents too.
	from := t.cursor
	texts, next, err := t.Store.Pending(ctx, pgstore.PendingQuery{Model: w.fp, After: from, Unready: !t.switching,
		Skip: skip, Limit: w.o.batch * w.o.slots * roundBatches})
	if err != nil {
		return false, err
	}
	t.cursor = next
	if len(texts) == 0 {
		if !from.IsZero() {
			// the scan reached the end from part way: what was written behind it is looked for
			// from the start before anything is concluded
			return true, nil
		}
		if t.switching {
			return false, w.flip(ctx, t)
		}
		return false, w.settle(ctx, t)
	}
	stored, err := w.embed(ctx, t, texts)
	if err != nil {
		// a batch that failed is read again: its texts have no vector, and the next round
		// resumes where this one began
		t.cursor = from
	}
	if len(stored) > 0 && !t.switching {
		// while a switch fills, readiness is the active model's, which the flip marks again
		paths, merr := t.Store.DocsWithTexts(ctx, stored)
		if merr == nil {
			infos := make([]pgstore.DocInfo, len(paths))
			for i, p := range paths {
				infos[i] = pgstore.DocInfo{Path: p}
			}
			merr = w.mark(ctx, t, infos, false)
		}
		err = errors.Join(err, merr)
	}
	return err == nil, err
}

// prepare reads t's models and records this worker's: the first model is active at once; the
// active one stays, a switch away from it that was under way being cancelled; another active
// model makes this one the target, filled while searches answer by words. The vectors of every
// other model are then reclaimed.
func (w *Worker) prepare(ctx context.Context, t *tenant) error {
	active, target, err := t.Store.Models(ctx)
	if err != nil {
		return err
	}
	if err := t.confirm(ctx); err != nil {
		return err
	}
	t.switching = false
	switch {
	case active == "":
		if err := t.Store.SetModels(ctx, w.fp, ""); err != nil {
			return err
		}
		logger.Info(w.o.log, fmt.Sprintf("tenant %s: embedding with %s", t.Name, w.fp))
	case active == w.fp && target != "":
		if target != w.fp {
			// a flip of the cancelled target may have marked documents by its vectors: every
			// document is marked again by this model's, while the record still says switching
			// and searches still answer by words
			if err := w.markAll(ctx, t, false); err != nil {
				return err
			}
			logger.Notice(w.o.log, fmt.Sprintf("tenant %s: the switch to %s is cancelled: %s stays active", t.Name, target, w.fp))
		}
		if err := t.Store.SetModels(ctx, w.fp, ""); err != nil {
			return err
		}
	case active != w.fp:
		if target != w.fp {
			if err := t.Store.SetModels(ctx, active, w.fp); err != nil {
				return err
			}
		}
		t.switching = true
		logger.Notice(w.o.log, fmt.Sprintf("tenant %s: switching from %s to %s: searches answer by words until every text is embedded", t.Name, active, w.fp))
	}
	if err := w.reclaim(ctx, t); err != nil {
		return err
	}
	t.waiting, t.cursor = map[string]int64{}, pgstore.PendingKey{}
	t.prepared = true
	w.publish()
	return nil
}

// reclaim deletes t's vectors under every model but its active one and its target.
func (w *Worker) reclaim(ctx context.Context, t *tenant) error {
	if err := t.confirm(ctx); err != nil {
		return err
	}
	n, err := t.Store.ReclaimModels(ctx)
	if n > 0 {
		logger.Info(w.o.log, fmt.Sprintf("tenant %s: %d vectors of models no longer in use reclaimed", t.Name, n))
	}
	return err
}

// flip makes the model a switch filled t's active one, once nothing is pending under it: every
// document is marked by its vectors first, while searches still answer by words, so a flip that
// fails part way is done again whole; then the record, and the old vectors are reclaimed.
func (w *Worker) flip(ctx context.Context, t *tenant) error {
	t.waiting = map[string]int64{}
	if err := w.markAll(ctx, t, true); err != nil {
		return err
	}
	if err := t.confirm(ctx); err != nil {
		return err
	}
	if err := t.Store.SetModels(ctx, w.fp, ""); err != nil {
		return err
	}
	t.switching, t.cursor = false, pgstore.PendingKey{}
	w.publish()
	logger.Notice(w.o.log, fmt.Sprintf("tenant %s: %s is active: every text is embedded", t.Name, w.fp))
	return w.reclaim(ctx, t)
}

// anyUnready reports whether t has an unready document that is not waiting on set-aside texts.
func (w *Worker) anyUnready(ctx context.Context, t *tenant) (bool, error) {
	after := ""
	for {
		page, more, err := t.Store.Unready(ctx, after, pageSize)
		if err != nil {
			return false, err
		}
		for _, d := range page {
			if g, ok := t.waiting[d.Path]; !ok || g != d.Generation {
				return true, nil
			}
			after = d.Path
		}
		if !more {
			return false, nil
		}
	}
}

// settle marks t's unready documents ready when nothing is pending: their texts are all embedded
// (a document written again unchanged, or a crash between storing vectors and marking), or set
// aside, and then they wait.
func (w *Worker) settle(ctx context.Context, t *tenant) error {
	after := ""
	for {
		page, more, err := t.Store.Unready(ctx, after, pageSize)
		if err != nil {
			return err
		}
		var docs []pgstore.DocInfo
		for _, d := range page {
			after = d.Path
			if g, ok := t.waiting[d.Path]; !ok || g != d.Generation {
				docs = append(docs, d)
			}
		}
		if err := w.mark(ctx, t, docs, true); err != nil {
			return err
		}
		if !more {
			return nil
		}
	}
}

// markAll marks every one of t's documents by the worker's model, as mark does.
func (w *Worker) markAll(ctx context.Context, t *tenant, remember bool) error {
	after := ""
	for {
		page, more, err := t.Store.Docs(ctx, after, pageSize)
		if err != nil {
			return err
		}
		if err := w.mark(ctx, t, page, remember); err != nil {
			return err
		}
		if !more || len(page) == 0 {
			return nil
		}
		after = page[len(page)-1].Path
	}
}

// mark asks MarkReady about each of docs under the worker's model, once the tenant is confirmed
// this server's. With remember, nothing is pending, so a document still incomplete waits on
// set-aside texts alone, and is remembered at its generation (see tenant.waiting).
func (w *Worker) mark(ctx context.Context, t *tenant, docs []pgstore.DocInfo, remember bool) error {
	if len(docs) == 0 {
		return nil
	}
	if err := t.confirm(ctx); err != nil {
		return err
	}
	for _, d := range docs {
		ready, err := t.Store.MarkReady(ctx, d.Path, w.fp)
		switch {
		case errors.Is(err, pgstore.ErrWriteRace), errors.Is(err, dao.ErrNoRows):
			// written again while it was decided (the new generation is unready, and a later
			// round marks it), or deleted: nothing to mark
		case err != nil:
			return err
		case ready:
			delete(t.waiting, d.Path)
		case remember:
			t.waiting[d.Path] = d.Generation
		}
	}
	return nil
}

// skip is t's texts set aside now, as Pending takes them, and whether a set-aside text's time ran
// out since the last call: it is tried again, and the documents waiting on it are looked at.
func (t *tenant) skip(now time.Time) (map[[32]byte]bool, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make(map[[32]byte]bool, len(t.refused))
	expired := false
	for h, at := range t.refused {
		if !now.Before(at) {
			delete(t.refused, h)
			expired = true
			continue
		}
		out[h] = true
	}
	return out, expired
}

// embed embeds texts in batches, each in a slot, and stores each batch's vectors as it comes. It
// returns the hashes stored, and every batch's failure: a failed batch leaves the others stored.
func (w *Worker) embed(ctx context.Context, t *tenant, texts []pgstore.Text) ([][32]byte, error) {
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		stored [][32]byte
		errs   []error
	)
	for start := 0; start < len(texts); start += w.o.batch {
		batch := texts[start:min(start+w.o.batch, len(texts))]
		if err := w.lim.acquire(ctx, false); err != nil {
			wg.Wait()
			return stored, err
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer w.lim.release()
			hashes, err := w.embedBatch(ctx, t, batch)
			mu.Lock()
			defer mu.Unlock()
			stored = append(stored, hashes...)
			if err != nil {
				errs = append(errs, err)
			}
		}()
	}
	wg.Wait()
	return stored, errors.Join(errs...)
}

// embedBatch embeds one batch with embed.Bisect, which normalizes each vector and narrows a
// rejection by the provider down to the texts it rejects: they are set aside for refusedFor, and
// the rest are stored once the tenant is confirmed this server's.
func (w *Worker) embedBatch(ctx context.Context, t *tenant, batch []pgstore.Text) ([][32]byte, error) {
	in := make([]string, len(batch))
	for i, x := range batch {
		in[i] = x.Text
	}
	vecs, rejected, err := embed.Bisect(ctx, w.p, in, w.dims, batchTimeout)
	if ctx.Err() == nil {
		// the provider answered, or failed on its own: a texts' rejection is an answer, per text
		w.answered(w.fp, err)
	}
	if err != nil {
		return nil, err
	}
	if len(rejected) > 0 {
		retry := w.o.clock.Now().Add(w.o.refusedFor)
		t.mu.Lock()
		for _, r := range rejected {
			t.refused[batch[r.Index].TextHash] = retry
		}
		t.mu.Unlock()
		logger.Warning(w.o.log, rejected[0].Err, fmt.Sprintf("tenant %s: the provider rejected %d texts: they are set aside for %v, and their documents answer by words",
			t.Name, len(rejected), w.o.refusedFor))
	}
	out := make([]pgstore.Embedding, 0, len(vecs))
	hashes := make([][32]byte, 0, len(vecs))
	for i, v := range vecs {
		if v != nil {
			out = append(out, pgstore.Embedding{TextHash: batch[i].TextHash, Vec: v})
			hashes = append(hashes, batch[i].TextHash)
		}
	}
	if len(out) == 0 {
		return nil, nil
	}
	if err := t.confirm(ctx); err != nil {
		return nil, err
	}
	if err := t.Store.Embed(ctx, w.fp, out); err != nil {
		return nil, err
	}
	return hashes, nil
}
