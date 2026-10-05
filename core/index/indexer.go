package index

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/parse/markdown"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// MaxFileSize is the largest file the indexer reads as text. A larger one leaves the index, and its
// job records the error and waits for the file to change: reading it again could only fail again.
// A derived format's file has its own bound, derived.MaxContainer, and its text derived.MaxText.
const MaxFileSize = 16 << 20

// The change log's retention (ADR 0204 §4.7): a row goes only when it is BOTH older than
// retainAge AND more than retainRows behind the head.
const (
	retainAge  = 7 * 24 * time.Hour
	retainRows = 100_000
)

// Options configures an Indexer. Zero values take the defaults.
type Options struct {
	Workers    int           // parallel readers and parsers (default NumCPU)
	BatchSize  int           // the writer commits up to this many results at once (default 50)
	BatchDelay time.Duration // or every this long (default 200 ms)
	// Match reports whether a path is eligible (the workspace's include and exclude). Nil: all.
	Match func(path string) bool
	// RetryDelay is how long a job whose file could not be read first waits before it runs again
	// (default 1 s); each failure after that doubles the wait, up to MaxRetryDelay (default 5 min).
	RetryDelay    time.Duration
	MaxRetryDelay time.Duration
	// Now is the clock (default time.Now); tests set it to age the change log.
	Now func() time.Time
	// afterRead, when set (tests only), runs in the worker once a path's content is read, before the
	// result goes to the writer: the seam that lets a test change and touch the file in between.
	afterRead func(path string)
	// beforePublish, when set (tests only), runs after a commit and before its code index is
	// published: the seam that holds a publish while queries run against the commit.
	beforePublish func()
	// noGC (tests only) keeps dead chunks, to show they are never alive before GC takes them.
	noGC bool
	// onCommit, when set (tests only), is told how long each batch's transaction held the writer.
	onCommit func(time.Duration)
	// Provider embeds chunks for semantic search (nil: lexical only). Its model is the target: the
	// one active, or, while another is active, the one filling to replace it. The model it replaces
	// is offline while it fills: nothing embeds with it, so the server holds one model at a time,
	// and a query is answered by words (SemanticSwitching) until the target covers every chunk.
	Provider embed.Provider
	Logger   logger.Logger
	// ExternalEmbedding lets the daemon own the embedding worker. Standalone indexers keep their own.
	ExternalEmbedding bool
	OnEmbeddingWork   func() // non-blocking notification after a document commit
	Workspace         string
	// Schema is the workspace's frontmatter schema now, and its fingerprint ("" and nil: none). It
	// is asked once per document, so a schema swapped while the indexer runs applies from the next.
	Schema func() (*schema.Schema, string)
	// TextExtensions are the workspace's own plain-text extensions (kind.Of); nil: none.
	TextExtensions func() []string
	// Registrations are the build's chunkers (ADR 0216): a file of a registered extension is cut
	// by its chunker, under its version. nil: none, the community build.
	Registrations *registrations.Table
	// NewSearcher builds the engine that answers the indexer's searches, over the workspace's
	// index: once by words alone (embed nil), and once with the semantic tier when there is a
	// provider. nil is golib's search engine with the search's constants. Any search.Searcher can
	// take its place.
	NewSearcher NewSearcher
	// Rank is the re-ranking stage both searchers are wrapped in, once (rank.go), for the queries
	// that ask for it; zero: none.
	Rank Rank
}

// Indexer is the store's one writer and the parallel workers that feed it. It implements
// core/follow's Queue (Touch) and, with its Store, core/follow's Store.
type Indexer struct {
	store *Store
	fsys  vfs.FS
	opts  Options

	mu        sync.Mutex
	touched   map[string]bool // path → forced; drained by the writer
	signal    chan struct{}
	rescanner Rescanner

	jobs           map[string]*job // the writer's own state: every path with work outstanding
	queue          []string        // paths to hand to a worker, oldest first
	delayed        []string        // paths waiting out a retry delay
	unpersisted    map[string]bool // paths whose job row must be (re)written
	nextSeq        int64
	results        chan *prepared
	work           chan workItem
	ops            chan op   // writes other than documents' (PurgeModel)
	sem            *semantic // nil without a provider
	embedPosition  atomic.Int64
	semanticPaused atomic.Bool
	// words and hybrid answer searches: by words alone, and with the semantic tier (zero without a
	// provider). Both are built once, by Options.NewSearcher, each with and without the rank stage.
	words, hybrid searchers
	ready         chan struct{} // model row committed before the queue embeds

	parses int64 // prepared documents that were parsed, for tests (atomic via mu)

	kinds kind.Registrations // Options.Registrations' extensions, read once
	holds holds              // the documents this daemon holds (ADR 0216 §1.4)
}

type job struct {
	seq      int64
	force    bool
	inflight bool      // a worker holds it
	queued   bool      // it is in queue
	notUntil time.Time // a failed job waits before it runs again
	attempts int
	lastErr  string
}

type workItem struct {
	path  string
	seq   int64
	force bool
}

type prepared struct {
	path        string
	indexer     string
	claimedSeq  int64
	skip        bool
	delete      bool
	err         error
	tooLarge    bool // err is that the file is over its bound: its indexed text is no longer the file's
	refused     bool // err refuses a derived text the same bytes would deliver again (prepareDerived)
	hold        bool // a held document: nothing is written, and holdState is how its file stands
	holdState   string
	version     vfs.Version
	meta        chunk.Meta
	chunks      []chunkT
	links       []linkT
	frontmatter schema.Result // a Markdown file's facets and diagnostics under the schema
}

// NewIndexer returns the indexer for store over the workspace root fsys.
func NewIndexer(store *Store, fsys vfs.FS, opts Options) *Indexer {
	if opts.Workers <= 0 {
		opts.Workers = runtime.NumCPU()
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 50
	}
	if opts.BatchDelay <= 0 {
		opts.BatchDelay = 200 * time.Millisecond
	}
	if opts.RetryDelay <= 0 {
		opts.RetryDelay = time.Second
	}
	if opts.MaxRetryDelay <= 0 {
		opts.MaxRetryDelay = 5 * time.Minute
	}
	opts.MaxRetryDelay = max(opts.MaxRetryDelay, opts.RetryDelay)
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = logger.Nop{}
	}
	ix := &Indexer{store: store, fsys: fsys, opts: opts, kinds: opts.Registrations.Kinds(), touched: map[string]bool{},
		signal: make(chan struct{}, 1), jobs: map[string]*job{}, unpersisted: map[string]bool{},
		results: make(chan *prepared, 2*opts.Workers),
		work:    make(chan workItem), ops: make(chan op), ready: make(chan struct{})}
	if opts.Provider != nil {
		ix.sem = newSemantic(opts.Provider)
	}
	newSearcher := opts.NewSearcher
	if newSearcher == nil {
		newSearcher = defaultSearcher
	}
	ix.words = ix.searchers(newSearcher(searchStore{s: store}, nil))
	if ix.sem != nil {
		ix.hybrid = ix.searchers(newSearcher(searchStore{s: store, sem: ix.sem}, store.queryEmbedder(ix.sem)))
	}
	return ix
}

// Touch queues path to be re-read. It never blocks: the writer picks the path up in its next batch.
func (x *Indexer) Touch(path string) { x.touch(path, false) }

// HasEmbeddings reports whether this indexer has a provider to fill its vectors.
func (x *Indexer) HasEmbeddings() bool { return x.sem != nil }

// SetSemanticPaused keeps a policy-paused workspace lexical even if it has old vectors.
func (x *Indexer) SetSemanticPaused(paused bool) { x.semanticPaused.Store(paused) }

// EmbeddingReady reports that setupModels installed the model row needed by the writer.
func (x *Indexer) EmbeddingReady() bool {
	select {
	case <-x.ready:
		return true
	default:
		return false
	}
}

// Reindex queues path to be re-read and re-parsed even when its file is unchanged: the forced job
// bypasses the fast path. "" is the whole workspace: every indexed document, and, through the
// Rescanner, the files the index has not seen; the held among them are only checked against disk.
// A held path alone is refused (HeldError), naming the way out.
func (x *Indexer) Reindex(path string) error {
	if path != "" {
		h, err := x.holding(context.Background())
		if err != nil {
			return err
		}
		if d, ok := h.of(path); ok {
			return &HeldError{Path: path, Ext: d.ext, What: d.what}
		}
		x.touch(path, true)
		return nil
	}
	for _, p := range x.store.PathsUnder(".") {
		x.touch(p, true)
	}
	x.mu.Lock()
	r := x.rescanner
	x.mu.Unlock()
	if r != nil {
		r.Rescan()
	}
	return nil
}

// Rescanner finds the eligible files under the root that the index does not have: core/follow's
// Follower.
type Rescanner interface {
	Rescan()
}

// SetRescanner sets what Reindex("") scans the root with. The follower is built on the indexer, so
// it is set once both exist.
func (x *Indexer) SetRescanner(r Rescanner) {
	x.mu.Lock()
	x.rescanner = r
	x.mu.Unlock()
}

func (x *Indexer) touch(path string, force bool) {
	x.mu.Lock()
	x.touched[path] = x.touched[path] || force
	x.mu.Unlock()
	select {
	case x.signal <- struct{}{}:
	default:
	}
}

// Version and PathsUnder read the store: with Touch, the Indexer is what core/follow follows into.
func (x *Indexer) Version(path string) (vfs.Version, bool) { return x.store.Version(path) }
func (x *Indexer) PathsUnder(dir string) []string          { return x.store.PathsUnder(dir) }

// Run writes the store until ctx ends. It first takes up the jobs a previous run left, and every
// document indexed under another IndexerVersion.
func (x *Indexer) Run(ctx context.Context) error {
	if err := x.loadJobs(ctx); err != nil {
		return err
	}
	if err := x.Revalidate(ctx); err != nil {
		return err
	}
	if x.sem != nil {
		if err := x.setupModels(ctx); err != nil {
			return fmt.Errorf("index: recording the embedding model: %w", err)
		}
	}
	close(x.ready)
	if x.opts.ExternalEmbedding && x.opts.OnEmbeddingWork != nil {
		x.opts.OnEmbeddingWork()
	}
	var wg sync.WaitGroup
	workCtx, stopWorkers := context.WithCancel(ctx)
	for i := 0; i < x.opts.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x.worker(workCtx)
		}()
	}
	if x.sem != nil && !x.opts.ExternalEmbedding {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x.embedLoop(workCtx)
		}()
	}
	defer func() { stopWorkers(); wg.Wait() }()
	return x.writer(ctx)
}

func (x *Indexer) loadJobs(ctx context.Context) error {
	s := x.store
	return s.read(ctx, func(tx *store.Tx) error {
		jobs, err := s.sc.Jobs(tx).Select(store.JobPath, store.JobSeq, store.JobReason, store.JobAttempts)
		if err != nil {
			return fmt.Errorf("index: loading pending jobs: %w", err)
		}
		for _, r := range jobs {
			j := &job{seq: r.Seq, attempts: int(r.Attempts), force: deref(r.Reason) == "reindex"}
			x.jobs[r.Path] = j
			x.enqueue(r.Path, j)
			x.nextSeq = max(x.nextSeq, j.seq)
		}
		return nil
	})
}

// writer is the one goroutine that writes the store. It batches touches and results, dispatches
// jobs to the workers, and garbage-collects when idle.
func (x *Indexer) writer(ctx context.Context) error {
	var batch []*prepared
	var ready []workItem
	var first time.Time
	gcDue := time.Now().Add(x.opts.BatchDelay)
	ticker := time.NewTicker(x.opts.BatchDelay / 2)
	defer ticker.Stop()
	for {
		ready = x.dispatchable(ready)
		var out chan workItem
		var next workItem
		if len(ready) > 0 {
			out, next = x.work, ready[0]
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case out <- next:
			ready = ready[1:]
		case <-x.signal:
			x.takeTouches()
			if first.IsZero() {
				first = time.Now()
			}
		case p := <-x.results:
			batch = append(batch, p)
			if first.IsZero() {
				first = time.Now()
			}
		case vb := <-x.vectorsIn():
			err := x.commitVectors(ctx, vb)
			vb.done <- err
			if err != nil && ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		case o := <-x.ops:
			o.done <- x.runOp(ctx, o.fn)
			continue
		case <-ticker.C:
		}
		pending := len(batch) > 0 || len(x.unpersisted) > 0
		if pending && (len(batch) >= x.opts.BatchSize || time.Since(first) >= x.opts.BatchDelay) {
			if err := x.commit(ctx, batch); err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				// nothing was written: every result's job runs again
				for _, p := range batch {
					if j := x.jobs[p.path]; j != nil {
						j.inflight = false
						x.enqueue(p.path, j)
					}
				}
			}
			batch, first = batch[:0], time.Time{}
			gcDue = time.Now().Add(x.opts.BatchDelay)
			continue
		}
		if !pending && len(ready) == 0 && !x.opts.noGC && time.Now().After(gcDue) {
			if more, err := x.gc(ctx); err == nil && more {
				gcDue = time.Now() // keep going while there is garbage and nothing else to do
			} else {
				gcDue = time.Now().Add(5 * x.opts.BatchDelay)
			}
		}
	}
}

// takeTouches turns the touched paths into jobs, each with a new seq. A path whose worker is still
// reading gets a new seq too, so that worker's result is discarded as stale.
func (x *Indexer) takeTouches() {
	x.mu.Lock()
	touched := x.touched
	x.touched = map[string]bool{}
	x.mu.Unlock()
	for p, force := range touched {
		j := x.jobs[p]
		if j == nil {
			j = &job{}
			x.jobs[p] = j
		}
		x.nextSeq++
		j.seq, j.force = x.nextSeq, j.force || force
		j.notUntil = time.Time{}
		x.unpersisted[p] = true
		x.enqueue(p, j) // a job in flight runs again once its stale result is back
	}
}

func (x *Indexer) enqueue(p string, j *job) {
	if !j.queued && !j.inflight {
		j.queued = true
		x.queue = append(x.queue, p)
	}
}

// dispatchable moves queued jobs to ready, and delayed ones whose wait is over back into the queue.
func (x *Indexer) dispatchable(ready []workItem) []workItem {
	if len(x.delayed) > 0 {
		now := time.Now()
		keep := x.delayed[:0]
		for _, p := range x.delayed {
			if j := x.jobs[p]; j != nil && now.Before(j.notUntil) {
				keep = append(keep, p)
			} else if j != nil {
				x.enqueue(p, j)
			}
		}
		x.delayed = keep
	}
	for len(ready) < x.opts.Workers && len(x.queue) > 0 {
		p := x.queue[0]
		x.queue = x.queue[1:]
		j := x.jobs[p]
		if j == nil || !j.queued {
			continue
		}
		j.queued, j.inflight = false, true
		ready = append(ready, workItem{path: p, seq: j.seq, force: j.force})
	}
	return ready
}

func (x *Indexer) worker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case w := <-x.work:
			p := x.prepare(ctx, w)
			select {
			case x.results <- p:
			case <-ctx.Done():
				return
			}
		}
	}
}

// prepare reads, parses and chunks one path. It writes nothing.
func (x *Indexer) prepare(ctx context.Context, w workItem) *prepared {
	p := &prepared{path: w.path, claimedSeq: w.seq}
	tokens, err := x.store.sectionTokens(ctx)
	if err != nil {
		p.err = err
		return p
	}
	sch, schemaFP := x.schema()
	k := x.kindOf(w.path)
	p.indexer = x.versionOf(w.path, k, tokens, schemaFP)
	fi, err := x.fsys.Stat(ctx, w.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		p.delete = true
		return p
	case err != nil:
		p.err = err
		return p
	case !fi.IsRegular() || (x.opts.Match != nil && !x.opts.Match(w.path)):
		p.delete = true // a directory now, a symlink, or outside include / inside exclude
		return p
	}
	// a held document is never reinterpreted, forced or not; disk and the rules still delete it
	h, err := x.holding(ctx)
	if err != nil {
		p.err = err
		return p
	}
	if _, ok := h.of(w.path); ok {
		p.hold, p.holdState = true, HoldCurrent
		if v, ok := x.store.Version(w.path); !ok || v != fi.Version {
			p.holdState = HoldStale
		}
		return p
	}
	derives := k == kind.Pro && x.kinds.Readable(w.path)
	if k == kind.Pro && !derives {
		p.delete = true // a build without its deriver never reads a Pro document format, whatever a glob admits
		return p
	}
	if !w.force {
		// a derived document's identity is the frozen one: an unchanged file is derived again only
		// when that changed
		if v, ok := x.store.Version(w.path); ok && v == fi.Version && x.store.indexer(w.path) == p.indexer {
			p.skip = true
			return p
		}
	}
	limit := int64(MaxFileSize)
	if derives {
		limit = derived.MaxContainer
	}
	if fi.Size > limit {
		p.err, p.tooLarge = tooLarge(w.path, limit), true
		return p
	}
	if derives {
		return x.prepareDerived(ctx, p, fi, tokens)
	}
	r, err := x.fsys.Open(ctx, w.path, 0)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			p.delete = true
		} else {
			p.err = err
		}
		return p
	}
	src, err := io.ReadAll(io.LimitReader(r, MaxFileSize+1))
	_ = r.Close()
	if err != nil {
		p.err = err
		return p
	}
	if x.opts.afterRead != nil {
		x.opts.afterRead(w.path)
	}
	if len(src) > MaxFileSize { // it grew after the Stat
		p.err, p.tooLarge = tooLarge(w.path, MaxFileSize), true
		return p
	}
	p.version = fi.Version
	if k.IsText() {
		if !utf8.Valid(src) || bytes.IndexByte(src, 0) >= 0 {
			p.err = fmt.Errorf("%s: not UTF-8 text", w.path)
			return p
		}
	}
	switch k {
	case kind.Registered:
		p.meta.Title = strings.TrimSuffix(path.Base(w.path), path.Ext(w.path))
		c, v, _ := x.opts.Registrations.Chunker(w.path)
		cs, err := cutRegistered(c, v, search.Doc{Path: w.path, Title: p.meta.Title, Text: src, Tokens: tokens})
		if err != nil {
			p.err = fmt.Errorf("%s: %w", w.path, err)
			return p
		}
		p.chunks = hashedUnder(v, cs)
	case kind.Text:
		p.meta.Title = strings.TrimSuffix(path.Base(w.path), path.Ext(w.path))
		p.chunks = hashed(chunk.Text(src, p.meta.Title, tokens))
	case kind.YAML:
		var cs []search.Chunk
		p.meta, cs = chunk.YAML(src, w.path, tokens)
		p.chunks = hashed(cs)
	default:
		doc := markdown.Parse(src, markdown.GFM(), markdown.Obsidian())
		p.meta = chunk.ReadMeta(doc, w.path)
		p.chunks = hashed(chunk.Markdown(doc, p.meta.Title, tokens))
		p.links = extractLinks(doc, w.path, x.opts.Match)
		// the frontmatter is validated from the parse the file already had (ADR 0212 §5)
		if fm := doc.Root.FirstChild; fm != nil && fm.Kind == markdown.KindFrontmatter {
			p.frontmatter = sch.Validate(fm.Literal, true)
		} else {
			p.frontmatter = sch.Validate(nil, false)
		}
	}
	x.mu.Lock()
	x.parses++
	x.mu.Unlock()
	return p
}

func (s *Store) indexer(path string) string {
	var v string
	_ = s.read(context.Background(), func(tx *store.Tx) error {
		d, err := s.sc.Documents(tx).With(store.DocPath, path).Get(store.DocIndexer)
		if err == nil {
			v = d.Indexer
		}
		return nil
	})
	return v
}

// commit writes one batch in one transaction: the jobs touched since the last one, and the results
// whose job has not been touched again since its worker claimed it.
func (x *Indexer) commit(ctx context.Context, batch []*prepared) error {
	s := x.store
	now := x.opts.Now()
	type outcome struct {
		p     *prepared
		done  bool // the job is finished
		stale bool
	}
	var outcomes []outcome
	var changed []int64 // the documents written: their codes may differ
	began := time.Now()
	err := s.db.Write(ctx, func(tx *store.Tx) error {
		if _, err := s.bumpSeq(tx); err != nil {
			return err
		}
		for p := range x.unpersisted {
			j := x.jobs[p]
			if j == nil {
				continue
			}
			reason := "touch"
			if j.force {
				reason = "reindex"
			}
			// a touch of a queued path moves its seq and reason, and keeps when it was enqueued
			row := s.sc.Jobs(tx).Set(store.JobPath, p).Set(store.JobSeq, j.seq).Set(store.JobReason, reason).
				Set(store.JobEnqueuedAt, now.Unix()).Set(store.JobAttempts, int64(0))
			if err := dao.UpsertOnly(row, store.JobSeq, store.JobReason); err != nil {
				return err
			}
		}
		for _, p := range batch {
			j := x.jobs[p.path]
			if j == nil || j.seq != p.claimedSeq {
				outcomes = append(outcomes, outcome{p: p, stale: true})
				continue
			}
			switch {
			case p.err != nil:
				drop := p.tooLarge // its indexed text is no longer the file's
				if p.refused {
					// an unchanged file re-derived for a new identity alone still has a correct row:
					// only a file no longer the one indexed loses it
					d, err := s.sc.Documents(tx).With(store.DocPath, p.path).Get(store.DocVersion)
					if err != nil && !errors.Is(err, dao.ErrNoRows) {
						return err
					}
					drop = err == nil && vfs.Version(d.Version) != p.version
				}
				if drop {
					id, err := s.deleteDoc(tx, p.path, now)
					if err != nil {
						return err
					}
					changed = append(changed, id)
				}
				if err := s.sc.Jobs(tx).With(store.JobPath, p.path).Set(store.JobAttempts, dao.Incr(1)).
					Set(store.JobLastError, p.err.Error()).Update(); err != nil {
					return err
				}
				outcomes = append(outcomes, outcome{p: p})
				continue
			case p.delete:
				id, err := s.deleteDoc(tx, p.path, now)
				if err != nil {
					return err
				}
				changed = append(changed, id)
			case p.hold:
				// nothing is written: its chunks and vectors stay, searchable
			case !p.skip:
				id, err := s.upsertDoc(tx, p, now)
				if err != nil {
					return err
				}
				changed = append(changed, id)
			}
			if err := s.sc.Jobs(tx).With(store.JobPath, p.path).With(store.JobSeq, p.claimedSeq).Delete(); err != nil {
				return err
			}
			outcomes = append(outcomes, outcome{p: p, done: true})
		}
		return nil
	})
	if x.opts.onCommit != nil {
		x.opts.onCommit(time.Since(began))
	}
	if err != nil {
		return err
	}
	if err := x.publish(ctx, changed); err != nil {
		return err
	}
	if x.sem != nil && len(changed) > 0 {
		x.sem.signal() // new chunks may want vectors
		if x.opts.OnEmbeddingWork != nil {
			x.opts.OnEmbeddingWork()
		}
	}
	clear(x.unpersisted)
	for _, o := range outcomes {
		switch {
		case !o.done:
		case o.p.hold:
			x.holds.set(o.p.path, o.p.holdState)
		case o.p.delete:
			x.holds.drop(o.p.path)
		}
	}
	for _, o := range outcomes {
		j := x.jobs[o.p.path]
		switch {
		case j == nil:
		case o.done:
			delete(x.jobs, o.p.path)
		case o.stale:
			j.inflight = false // touched again while it was read: run it again with the new seq
			x.enqueue(o.p.path, j)
		default:
			j.inflight, j.attempts, j.lastErr = false, j.attempts+1, o.p.err.Error()
			if o.p.tooLarge || o.p.refused {
				continue // the next touch of the path runs it again
			}
			j.notUntil = time.Now().Add(x.retryDelay(j.attempts))
			x.delayed = append(x.delayed, o.p.path)
		}
	}
	return nil
}

func tooLarge(path string, limit int64) error {
	return fmt.Errorf("index: %s is over %d bytes", path, limit)
}

// prepareDerived makes the text of a Pro document through the build's deriver and chunks it as
// Markdown. Its text is bounded apart from its file (derived.MaxText), refused by its stated size
// before it is read. Two kinds of failure are told apart:
//   - the deriver's own (derived.ErrDeriverFailed: an error, a second miss, a panic, no text, or a
//     text that fails as it is read) may pass: it is the document's error, retried with the
//     backoff, and its row stays;
//   - a refusal of the text it delivered (derived.ErrRefused, derived.ErrTooLarge, not UTF-8) is
//     what the same bytes would deliver again: it waits for the file to change, and deletes the row
//     only if the file changed since it was indexed (refused).
//
// A file gone before it could be opened leaves the index, as any file does.
func (x *Indexer) prepareDerived(ctx context.Context, p *prepared, fi vfs.FileInfo, tokens int) *prepared {
	p.version = fi.Version // what a refusal compares with the row's
	d, err := derived.Text(ctx, x.opts.Registrations, x.fsys, p.path, fi.Size)
	switch {
	case err == nil:
	case errors.Is(err, derived.ErrRefused):
		p.err, p.refused = err, true
		return p
	case errors.Is(err, fs.ErrNotExist):
		p.delete = true
		return p
	default:
		p.err = err
		return p
	}
	src, err := derived.Read(d, derived.MaxText)
	if err != nil {
		p.err, p.refused = fmt.Errorf("index: %s: %w", p.path, err), errors.Is(err, derived.ErrTooLarge)
		return p
	}
	if x.opts.afterRead != nil {
		x.opts.afterRead(p.path)
	}
	if !utf8.Valid(src) || bytes.IndexByte(src, 0) >= 0 {
		p.err, p.refused = fmt.Errorf("%s: its derived text is not UTF-8", p.path), true
		return p
	}
	// the text is a document's, not a vault's: no wikilinks or #tags, no frontmatter to check
	doc := markdown.Parse(src, markdown.GFM())
	p.meta = chunk.ReadMeta(doc, p.path)
	if d.Info.Title != "" {
		p.meta.Title = d.Info.Title
	}
	p.chunks = hashed(chunk.Markdown(doc, p.meta.Title, tokens))
	x.mu.Lock()
	x.parses++
	x.mu.Unlock()
	return p
}

// retryDelay is the wait after a job's attempts-th failure in a row.
func (x *Indexer) retryDelay(attempts int) time.Duration {
	d := x.opts.RetryDelay
	for i := 1; i < attempts && d < x.opts.MaxRetryDelay; i++ {
		d *= 2
	}
	return min(d, x.opts.MaxRetryDelay)
}

type oldChunk struct {
	id                            int64
	hash                          []byte
	title, breadcrumb, tags, body string
}

// upsertDoc writes a document's new generation (ADR 0204 §4.2): chunks whose hash is in the active
// generation are reused in place (their position updated, no full-text write), new ones are
// inserted under the new generation, and the rest die at it. The flip of active_gen is in the same
// transaction, so a reader sees the old generation or the new one, never a mix. The full-text index
// follows the chunks by the schema's triggers.
func (s *Store) upsertDoc(tx *store.Tx, p *prepared, now time.Time) (int64, error) {
	var docID, gen int64
	var oldTitle string
	d, err := s.sc.Documents(tx).With(store.DocPath, p.path).Get(store.DocID, store.DocActiveGen, store.DocTitle)
	appeared := errors.Is(err, dao.ErrNoRows)
	switch {
	case appeared:
		if docID, err = s.sc.Documents(tx).Set(store.DocPath, p.path).Set(store.DocVersion, "").Set(store.DocActiveGen, int64(0)).
			Set(store.DocIndexer, p.indexer).Set(store.DocIndexedAt, now.Unix()).Insert(); err != nil {
			return 0, err
		}
	case err != nil:
		return 0, err
	default:
		docID, gen, oldTitle = d.ID, d.ActiveGen, deref(d.Title)
	}
	oldTags, err := s.tagsOf(tx, docID)
	if err != nil {
		return 0, err
	}
	next := gen + 1
	old, err := s.aliveChunks(tx, docID, gen)
	if err != nil {
		return 0, err
	}
	title, tags := p.meta.Title, strings.Join(p.meta.Tags, " ")
	metaChanged := title != oldTitle || tags != strings.Join(oldTags, " ")
	var reused []int64
	// the new chunks go in as one statement: the full-text trigger makes each insert a statement of
	// its own savepoint, and FTS5 writes what it holds to disk at every savepoint
	fresh := s.sc.ChunkBatch(tx)
	added := false
	for _, c := range p.chunks {
		key := string(c.hash)
		if occ := old[key]; len(occ) > 0 {
			id := occ[0]
			old[key] = occ[1:]
			if err := s.sc.Chunks(tx).With(store.ChunkID, id).Set(store.ChunkOrd, int64(c.Ord)).
				Set(store.ChunkByteStart, int64(c.ByteStart)).Set(store.ChunkByteEnd, int64(c.ByteEnd)).Update(); err != nil {
				return 0, err
			}
			reused = append(reused, id)
			continue
		}
		fresh.Add(map[store.ChunkField]any{store.ChunkDoc: docID, store.ChunkHash: c.hash, store.ChunkTextHash: c.textHash,
			store.ChunkGenFrom: next, store.ChunkOrd: int64(c.Ord), store.ChunkBreadcrumb: c.Breadcrumb,
			store.ChunkBody: c.Body, store.ChunkEmbed: c.Embed, store.ChunkTitle: title, store.ChunkTags: tags,
			store.ChunkByteStart: int64(c.ByteStart), store.ChunkByteEnd: int64(c.ByteEnd)})
		added = true
	}
	if added {
		if err := fresh.Flush(); err != nil {
			return 0, err
		}
	}
	var dead []int64
	for _, occ := range old {
		dead = append(dead, occ...)
	}
	// dead from the new generation on; their full-text entries go with their rows, at GC
	for part := range inParts(dead) {
		if err := s.sc.Chunks(tx).With(store.ChunkID, part...).Set(store.ChunkGenTo, next).Update(); err != nil {
			return 0, err
		}
	}
	if metaChanged {
		// the document's title and tags are on every chunk (FTS5 reads the columns it names): the
		// update trigger replaces each reused chunk's full-text entry, old values out, new in
		for part := range inParts(reused) {
			if err := s.sc.Chunks(tx).With(store.ChunkID, part...).Set(store.ChunkTitle, title).Set(store.ChunkTags, tags).Update(); err != nil {
				return 0, err
			}
		}
	}
	if err := s.replaceValues(tx, s.sc.Tags(tx), s.sc.TagBatch(tx), docID, p.meta.Tags); err != nil {
		return 0, err
	}
	if err := s.replaceValues(tx, s.sc.Aliases(tx), s.sc.AliasBatch(tx), docID, p.meta.Aliases); err != nil {
		return 0, err
	}
	if err := s.replaceFrontmatter(tx, docID, p.frontmatter); err != nil {
		return 0, err
	}
	// the names, the file's own links, then the links elsewhere whose target may have changed: those
	// under every name the file gained or lost, and, when it appeared, under its path
	changed, err := s.writeNames(tx, docID, namesOf(p.path, p.meta.Aliases))
	if err != nil {
		return 0, err
	}
	if appeared {
		changed = append(changed, markdownNames(p.path)...)
	}
	if err := s.writeLinks(tx, docID, next, p.links); err != nil {
		return 0, err
	}
	if err := s.reresolve(tx, changed, 0); err != nil {
		return 0, err
	}
	var fmJSON, fmErr any
	if p.meta.FrontmatterJSON != "" {
		fmJSON = p.meta.FrontmatterJSON
	}
	if p.meta.FrontmatterErr != "" {
		fmErr = p.meta.FrontmatterErr
	}
	if err := s.sc.Documents(tx).With(store.DocID, docID).Set(store.DocVersion, string(p.version)).Set(store.DocActiveGen, next).
		Set(store.DocTitle, title).Set(store.DocFrontmatterJSON, fmJSON).Set(store.DocFrontmatterError, fmErr).
		Set(store.DocIndexer, p.indexer).Set(store.DocIndexedAt, now.Unix()).Update(); err != nil {
		return 0, err
	}
	if err := s.logChange(tx, p.path, "upsert", next, now.Unix()); err != nil {
		return 0, err
	}
	// a new generation may hold chunks with no vector yet: the document waits for them
	return docID, s.setReady(tx, []int64{docID})
}

// deleteDoc removes a document and logs the delete. Its chunks, tags, aliases, names and links go
// with it by the schema's cascade, and its chunks' full-text entries by the delete trigger.
func (s *Store) deleteDoc(tx *store.Tx, path string, now time.Time) (int64, error) {
	d, err := s.sc.Documents(tx).With(store.DocPath, path).Get(store.DocID, store.DocActiveGen)
	if errors.Is(err, dao.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	names, err := s.storedNames(tx, d.ID)
	if err != nil {
		return 0, err
	}
	if err := s.sc.Documents(tx).With(store.DocID, d.ID).Delete(); err != nil {
		return 0, err
	}
	// the links that reached it, and those under any name it answered to, resolve again: to another
	// file, or to none
	affected := markdownNames(path)
	for n, isPath := range names {
		affected = append(affected, linkNames(n, isPath)...)
	}
	if err := s.reresolve(tx, affected, d.ID); err != nil {
		return 0, err
	}
	return d.ID, s.logChange(tx, path, "delete", d.ActiveGen+1, now.Unix())
}

// aliveChunks maps the hashes of a document's chunks alive at gen to their ids, in ord order
// (identical chunks repeat, so a hash may have several).
func (s *Store) aliveChunks(tx *store.Tx, docID, gen int64) (map[string][]int64, error) {
	rows, err := s.sc.Chunks(tx).With(store.ChunkDoc, docID).WithPredicate(dao.Lte(`"chunk"."gen_from"`, gen)).
		WithPredicate(dao.Or(dao.IsNull(`"chunk"."gen_to"`), dao.Gt(`"chunk"."gen_to"`, gen))).
		OrderBy(dao.Asc(store.ChunkByOrd)).Select(store.ChunkID, store.ChunkHash)
	if err != nil {
		return nil, err
	}
	out := map[string][]int64{}
	for _, r := range rows {
		out[string(r.Hash)] = append(out[string(r.Hash)], r.ID)
	}
	return out, nil
}

// replaceValues replaces a document's tags or aliases (set, through its batch) with values.
func (s *Store) replaceValues(tx *store.Tx, set dao.DAO[*store.DocValue, store.DocValueField, int64],
	b store.Batch[*store.DocValue, store.DocValueField], docID int64, values []string) error {
	if err := set.With(store.DocValueDoc, docID).Delete(); err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	b = b.SkipConflicts()
	for _, v := range values {
		b.Add(map[store.DocValueField]any{store.DocValueDoc: docID, store.DocValueValue: v})
	}
	return b.Flush()
}

// gc deletes up to 1000 dead chunks (their full-text entries with them, by the delete trigger)
// and prunes the change log, reporting whether dead chunks remain. Dead rows still count in BM25's
// statistics until they go, so this runs whenever the writer is idle.
func (x *Indexer) gc(ctx context.Context) (bool, error) {
	s := x.store
	var dead []int64
	more := false
	err := s.db.Write(ctx, func(tx *store.Tx) error {
		rows, err := s.sc.Chunks(tx).Join(store.JoinDocument).WithPredicate(dao.IsNotNull(`"chunk"."gen_to"`)).
			WithPredicate(dao.Cmp(dao.T("chunk", "gen_to"), dao.OpLte, dao.T("document", "active_gen"))).
			Limit(1001).Select(store.ChunkID)
		if err != nil {
			return err
		}
		for _, r := range rows {
			dead = append(dead, r.ID)
		}
		if more = len(dead) > 1000; more {
			dead = dead[:1000]
		}
		for part := range inParts(dead) {
			if err := s.sc.Chunks(tx).With(store.ChunkID, part...).Delete(); err != nil {
				return err
			}
		}
		if err := s.pruneChanges(tx, x.opts.Now()); err != nil {
			return err
		}
		if len(dead) > 0 {
			_, err = s.bumpSeq(tx)
		}
		return err
	})
	if err != nil {
		return false, err
	}
	if len(dead) > 0 {
		// dead chunks are in no snapshot: the codes stand, under the new commit_seq
		if err := x.publish(ctx, []int64{}); err != nil {
			return false, err
		}
	}
	return more, nil
}

// pruneChanges drops the change rows that are both older than retainAge and more than retainRows
// behind the head: a recovery listing always has a week and 100 000 changes of headroom.
func (s *Store) pruneChanges(tx *store.Tx, now time.Time) error {
	h, err := s.head(tx)
	if err != nil {
		return err
	}
	return s.sc.Changes(tx).WithPredicate(dao.Lt(`"change"."at"`, now.Add(-retainAge).Unix())).
		WithPredicate(dao.Lte(`"change"."seq"`, h-retainRows)).Delete()
}

// Parses reports how many documents workers have parsed, for tests of the fast path.
func (x *Indexer) Parses() int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.parses
}

// op is a write other than a document's, run by the writer in a transaction of its own.
type op struct {
	fn   func(context.Context, *store.Tx) error
	done chan error
}

// do runs fn in the writer, as its own transaction, and waits for it.
func (x *Indexer) do(ctx context.Context, fn func(context.Context, *store.Tx) error) error {
	o := op{fn: fn, done: make(chan error, 1)}
	select {
	case x.ops <- o:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-o.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (x *Indexer) runOp(ctx context.Context, fn func(context.Context, *store.Tx) error) error {
	s := x.store
	err := s.db.Write(ctx, func(tx *store.Tx) error {
		if _, err := s.bumpSeq(tx); err != nil {
			return err
		}
		return fn(ctx, tx)
	})
	if err != nil {
		return err
	}
	return x.publish(ctx, []int64{})
}

// vectorsIn is the embedding worker's handoff, or nil (never ready) without a provider.
func (x *Indexer) vectorsIn() chan vecBatch {
	if x.sem == nil {
		return nil
	}
	return x.sem.vectors
}

// Store is the store the indexer writes: its readers serve listings, the change log and the graph.
func (x *Indexer) Store() *Store { return x.store }
