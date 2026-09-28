package index

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/parse/markdown"
	"github.com/yongjohnlee80/golib/vfs"
)

// MaxFileSize is the largest note the indexer reads. A larger one leaves the index, and its job
// records the error and waits for the file to change: reading it again could only fail again.
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
	// noGC (tests only) keeps dead chunks, to show they are never alive before GC takes them.
	noGC bool
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

	jobs        map[string]*job // the writer's own state: every path with work outstanding
	queue       []string        // paths to hand to a worker, oldest first
	delayed     []string        // paths waiting out a retry delay
	unpersisted map[string]bool // paths whose job row must be (re)written
	nextSeq     int64
	results     chan *prepared
	work        chan workItem

	parses int64 // prepared documents that were parsed, for tests (atomic via mu)
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
	path       string
	claimedSeq int64
	skip       bool
	delete     bool
	err        error
	tooLarge   bool // err is that the file is over MaxFileSize
	version    vfs.Version
	meta       docMeta
	chunks     []chunkT
	links      []linkT
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
	return &Indexer{store: store, fsys: fsys, opts: opts, touched: map[string]bool{},
		signal: make(chan struct{}, 1), jobs: map[string]*job{}, unpersisted: map[string]bool{},
		results: make(chan *prepared, 2*opts.Workers),
		work:    make(chan workItem)}
}

// Touch queues path to be re-read. It never blocks: the writer picks the path up in its next batch.
func (x *Indexer) Touch(path string) { x.touch(path, false) }

// Reindex queues path to be re-read and re-parsed even when its file is unchanged: the forced job
// bypasses the fast path. "" is the whole workspace: every indexed document, and, through the
// Rescanner, the files the index has not seen.
func (x *Indexer) Reindex(path string) {
	if path != "" {
		x.touch(path, true)
		return
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
	outdated, err := x.store.outdated(ctx)
	if err != nil {
		return fmt.Errorf("index: listing outdated documents: %w", err)
	}
	for _, p := range outdated {
		x.touch(p, false) // the indexer check rebuilds each: no file changed, the chunker did
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
	defer func() { stopWorkers(); wg.Wait() }()
	return x.writer(ctx)
}

func (x *Indexer) loadJobs(ctx context.Context) error {
	rows, err := x.store.w.QueryContext(ctx, "SELECT path, seq, COALESCE(reason, ''), attempts FROM index_job")
	if err != nil {
		return fmt.Errorf("index: loading pending jobs: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var p, reason string
		j := &job{}
		if err := rows.Scan(&p, &j.seq, &reason, &j.attempts); err != nil {
			return err
		}
		j.force = reason == "reindex"
		x.jobs[p] = j
		x.enqueue(p, j)
		x.nextSeq = max(x.nextSeq, j.seq)
	}
	return rows.Err()
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
	if !w.force {
		if v, ok := x.store.Version(w.path); ok && v == fi.Version && x.store.indexer(w.path) == IndexerVersion {
			p.skip = true
			return p
		}
	}
	if fi.Size > MaxFileSize {
		p.err, p.tooLarge = tooLarge(w.path), true
		return p
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
		p.err, p.tooLarge = tooLarge(w.path), true
		return p
	}
	doc := markdown.Parse(src, markdown.GFM(), markdown.Obsidian())
	p.version = fi.Version
	p.meta = readMeta(doc, w.path)
	p.chunks = chunkDoc(doc, p.meta.title)
	p.links = extractLinks(doc, w.path, x.opts.Match)
	x.mu.Lock()
	x.parses++
	x.mu.Unlock()
	return p
}

func (s *Store) indexer(path string) string {
	var v string
	_ = scanOne(context.Background(), s.r, &v, "SELECT indexer FROM document WHERE path = ?", path)
	return v
}

// commit writes one batch in one transaction: the jobs touched since the last one, and the results
// whose job has not been touched again since its worker claimed it.
func (x *Indexer) commit(ctx context.Context, batch []*prepared) error {
	tx, err := x.store.w.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := x.opts.Now()
	if _, err := tx.ExecContext(ctx, "UPDATE meta SET v = CAST(v AS INTEGER) + 1 WHERE k = 'commit_seq'"); err != nil {
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO index_job(path, seq, reason, enqueued_at, attempts) VALUES (?, ?, ?, ?, 0)
			ON CONFLICT(path) DO UPDATE SET seq = excluded.seq, reason = excluded.reason`, p, j.seq, reason, now.Unix()); err != nil {
			return err
		}
	}
	type outcome struct {
		p     *prepared
		done  bool // the job is finished
		stale bool
	}
	var outcomes []outcome
	for _, p := range batch {
		j := x.jobs[p.path]
		if j == nil || j.seq != p.claimedSeq {
			outcomes = append(outcomes, outcome{p: p, stale: true})
			continue
		}
		switch {
		case p.err != nil:
			if p.tooLarge {
				// its indexed text is no longer the file's
				if err := deleteDoc(ctx, tx, p.path, now); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, "UPDATE index_job SET attempts = attempts + 1, last_error = ? WHERE path = ?", p.err.Error(), p.path); err != nil {
				return err
			}
			outcomes = append(outcomes, outcome{p: p})
			continue
		case p.delete:
			if err := deleteDoc(ctx, tx, p.path, now); err != nil {
				return err
			}
		case !p.skip:
			if err := upsertDoc(ctx, tx, p, now); err != nil {
				return err
			}
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM index_job WHERE path = ? AND seq = ?", p.path, p.claimedSeq); err != nil {
			return err
		}
		outcomes = append(outcomes, outcome{p: p, done: true})
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	clear(x.unpersisted)
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
			if o.p.tooLarge {
				continue // the next touch of the path runs it again
			}
			j.notUntil = time.Now().Add(x.retryDelay(j.attempts))
			x.delayed = append(x.delayed, o.p.path)
		}
	}
	return nil
}

func tooLarge(path string) error {
	return fmt.Errorf("index: %s is over %d bytes", path, MaxFileSize)
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
// generation are reused in place (their position updated, no FTS write), new ones are inserted
// under the new generation, and the rest die at it. The flip of active_gen is in the same
// transaction, so a reader sees the old generation or the new one, never a mix.
func upsertDoc(ctx context.Context, tx dao.TxConn, p *prepared, now time.Time) error {
	var docID, gen int64
	var oldTitle string
	err := scanRow(ctx, tx, []any{&docID, &gen, &oldTitle}, "SELECT id, active_gen, COALESCE(title, '') FROM document WHERE path = ?", p.path)
	appeared := errors.Is(err, errNoRow)
	switch {
	case appeared:
		res, err := tx.ExecContext(ctx, "INSERT INTO document(path, version, active_gen, indexer, indexed_at) VALUES (?, '', 0, ?, ?)", p.path, IndexerVersion, now.Unix())
		if err != nil {
			return err
		}
		if docID, err = res.LastInsertId(); err != nil {
			return err
		}
	case err != nil:
		return err
	}
	oldTags, err := docTags(ctx, tx, docID)
	if err != nil {
		return err
	}
	next := gen + 1
	old, err := aliveChunks(ctx, tx, docID, gen)
	if err != nil {
		return err
	}
	title, tags := p.meta.title, strings.Join(p.meta.tags, " ")
	metaChanged := title != oldTitle || tags != strings.Join(oldTags, " ")
	var reused []oldChunk
	for _, c := range p.chunks {
		key := string(c.hash)
		if occ := old[key]; len(occ) > 0 {
			o := occ[0]
			old[key] = occ[1:]
			if _, err := tx.ExecContext(ctx, "UPDATE chunk SET ord = ?, byte_start = ?, byte_end = ? WHERE id = ?", c.ord, c.byteStart, c.byteEnd, o.id); err != nil {
				return err
			}
			reused = append(reused, o)
			continue
		}
		res, err := tx.ExecContext(ctx, `INSERT INTO chunk(doc_id, hash, text_hash, gen_from, ord, breadcrumb, body, title, tags, byte_start, byte_end)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, docID, c.hash, c.textHash, next, c.ord, c.breadcrumb, c.body, title, tags, c.byteStart, c.byteEnd)
		if err != nil {
			return err
		}
		id, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if err := ftsInsert(ctx, tx, id, title, c.breadcrumb, tags, c.body); err != nil {
			return err
		}
	}
	for _, occ := range old {
		for _, o := range occ {
			// dead from the new generation on; its FTS row goes at GC, which still needs its text
			if _, err := tx.ExecContext(ctx, "UPDATE chunk SET gen_to = ? WHERE id = ?", next, o.id); err != nil {
				return err
			}
		}
	}
	if metaChanged {
		// the document's title and tags are on every chunk (FTS5 reads the columns it names): a
		// reused chunk's FTS row is replaced, old values out, new values in
		for _, o := range reused {
			if err := ftsDelete(ctx, tx, o.id, o.title, o.breadcrumb, o.tags, o.body); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, "UPDATE chunk SET title = ?, tags = ? WHERE id = ?", title, tags, o.id); err != nil {
				return err
			}
			if err := ftsInsert(ctx, tx, o.id, title, o.breadcrumb, tags, o.body); err != nil {
				return err
			}
		}
	}
	if err := replaceSet(ctx, tx, "doc_tag", "tag", docID, p.meta.tags); err != nil {
		return err
	}
	if err := replaceSet(ctx, tx, "doc_alias", "alias", docID, p.meta.aliases); err != nil {
		return err
	}
	// the names, the note's own links, then the links elsewhere whose target may have changed: those
	// under every name the note gained or lost, and, when it appeared, under its path
	changed, err := writeNames(ctx, tx, docID, namesOf(p.path, p.meta.aliases))
	if err != nil {
		return err
	}
	if appeared {
		changed = append(changed, markdownNames(p.path)...)
	}
	if err := writeLinks(ctx, tx, docID, next, p.links); err != nil {
		return err
	}
	if err := reresolve(ctx, tx, changed, 0); err != nil {
		return err
	}
	var fmJSON, fmErr any
	if p.meta.frontmatterJSON != "" {
		fmJSON = p.meta.frontmatterJSON
	}
	if p.meta.frontmatterErr != "" {
		fmErr = p.meta.frontmatterErr
	}
	if _, err := tx.ExecContext(ctx, `UPDATE document SET version = ?, active_gen = ?, title = ?, frontmatter_json = ?, frontmatter_error = ?,
		indexer = ?, indexed_at = ? WHERE id = ?`, string(p.version), next, title, fmJSON, fmErr, IndexerVersion, now.Unix(), docID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO change(path, op, generation, at) VALUES (?, 'upsert', ?, ?)", p.path, next, now.Unix())
	return err
}

// deleteDoc removes a document, its chunks (FTS rows first: an external-content delete needs the
// text), tags, aliases and links, and logs the delete.
func deleteDoc(ctx context.Context, tx dao.TxConn, path string, now time.Time) error {
	var docID, gen int64
	err := scanRow(ctx, tx, []any{&docID, &gen}, "SELECT id, active_gen FROM document WHERE path = ?", path)
	if errors.Is(err, errNoRow) {
		return nil
	}
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, "SELECT id, title, breadcrumb, tags, body FROM chunk WHERE doc_id = ?", docID)
	if err != nil {
		return err
	}
	var all []oldChunk
	for rows.Next() {
		var o oldChunk
		if err := rows.Scan(&o.id, &o.title, &o.breadcrumb, &o.tags, &o.body); err != nil {
			rows.Close()
			return err
		}
		all = append(all, o)
	}
	rows.Close()
	for _, o := range all {
		if err := ftsDelete(ctx, tx, o.id, o.title, o.breadcrumb, o.tags, o.body); err != nil {
			return err
		}
	}
	names, err := storedNames(ctx, tx, docID)
	if err != nil {
		return err
	}
	for _, q := range []string{"DELETE FROM chunk WHERE doc_id = ?", "DELETE FROM doc_tag WHERE doc_id = ?",
		"DELETE FROM doc_alias WHERE doc_id = ?", "DELETE FROM doc_name WHERE doc_id = ?",
		"DELETE FROM link WHERE src_doc = ?", "DELETE FROM document WHERE id = ?"} {
		if _, err := tx.ExecContext(ctx, q, docID); err != nil {
			return err
		}
	}
	// the links that reached it, and those under any name it answered to, resolve again: to another
	// note, or to none
	affected := markdownNames(path)
	for n, isPath := range names {
		affected = append(affected, linkNames(n, isPath)...)
	}
	if err := reresolve(ctx, tx, affected, docID); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO change(path, op, generation, at) VALUES (?, 'delete', ?, ?)", path, gen+1, now.Unix())
	return err
}

// aliveChunks maps the hashes of a document's chunks alive at gen to their rows, in ord order
// (identical chunks repeat, so a hash may have several).
func aliveChunks(ctx context.Context, tx dao.TxConn, docID, gen int64) (map[string][]oldChunk, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, hash, title, breadcrumb, tags, body FROM chunk
		WHERE doc_id = ? AND gen_from <= ? AND (gen_to IS NULL OR gen_to > ?) ORDER BY ord`, docID, gen, gen)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]oldChunk{}
	for rows.Next() {
		var o oldChunk
		if err := rows.Scan(&o.id, &o.hash, &o.title, &o.breadcrumb, &o.tags, &o.body); err != nil {
			return nil, err
		}
		out[string(o.hash)] = append(out[string(o.hash)], o)
	}
	return out, rows.Err()
}

func docTags(ctx context.Context, tx dao.TxConn, docID int64) ([]string, error) {
	rows, err := tx.QueryContext(ctx, "SELECT tag FROM doc_tag WHERE doc_id = ? ORDER BY tag", docID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func replaceSet(ctx context.Context, tx dao.TxConn, table, col string, docID int64, values []string) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE doc_id = ?", docID); err != nil {
		return err
	}
	for _, v := range values {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO "+table+"(doc_id, "+col+") VALUES (?, ?)", docID, v); err != nil {
			return err
		}
	}
	return nil
}

func ftsInsert(ctx context.Context, tx dao.TxConn, id int64, title, crumb, tags, body string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO chunk_fts(rowid, title, breadcrumb, tags, body) VALUES (?, ?, ?, ?, ?)", id, title, crumb, tags, body)
	return err
}

func ftsDelete(ctx context.Context, tx dao.TxConn, id int64, title, crumb, tags, body string) error {
	_, err := tx.ExecContext(ctx, "INSERT INTO chunk_fts(chunk_fts, rowid, title, breadcrumb, tags, body) VALUES ('delete', ?, ?, ?, ?, ?)", id, title, crumb, tags, body)
	return err
}

func scanRow(ctx context.Context, q dao.Querier, dst []any, query string, args ...any) error {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return err
		}
		return errNoRow
	}
	if err := rows.Scan(dst...); err != nil {
		return err
	}
	return rows.Err()
}

// gc deletes up to 1000 dead chunks (their FTS rows first) and prunes the change log, reporting
// whether dead chunks remain. Dead rows still count in BM25's statistics until they go, so this
// runs whenever the writer is idle.
func (x *Indexer) gc(ctx context.Context) (bool, error) {
	tx, err := x.store.w.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	rows, err := tx.QueryContext(ctx, `SELECT c.id, c.title, c.breadcrumb, c.tags, c.body FROM chunk c JOIN document d ON d.id = c.doc_id
		WHERE c.gen_to IS NOT NULL AND c.gen_to <= d.active_gen LIMIT 1001`)
	if err != nil {
		return false, err
	}
	var dead []oldChunk
	for rows.Next() {
		var o oldChunk
		if err := rows.Scan(&o.id, &o.title, &o.breadcrumb, &o.tags, &o.body); err != nil {
			rows.Close()
			return false, err
		}
		dead = append(dead, o)
	}
	rows.Close()
	more := len(dead) > 1000
	if more {
		dead = dead[:1000]
	}
	for _, o := range dead {
		if err := ftsDelete(ctx, tx, o.id, o.title, o.breadcrumb, o.tags, o.body); err != nil {
			return false, err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM chunk WHERE id = ?", o.id); err != nil {
			return false, err
		}
	}
	if err := pruneChanges(ctx, tx, x.opts.Now()); err != nil {
		return false, err
	}
	if len(dead) > 0 {
		if _, err := tx.ExecContext(ctx, "UPDATE meta SET v = CAST(v AS INTEGER) + 1 WHERE k = 'commit_seq'"); err != nil {
			return false, err
		}
	}
	return more, tx.Commit()
}

// pruneChanges drops the change rows that are both older than retainAge and more than retainRows
// behind the head: a recovery listing always has a week and 100 000 changes of headroom.
func pruneChanges(ctx context.Context, tx dao.TxConn, now time.Time) error {
	h, err := head(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM change WHERE at < ? AND seq <= ?", now.Add(-retainAge).Unix(), h-retainRows)
	return err
}

// Parses reports how many documents workers have parsed, for tests of the fast path.
func (x *Indexer) Parses() int64 {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.parses
}
