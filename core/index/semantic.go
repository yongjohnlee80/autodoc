package index

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/vector"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// Semantic states (Result.Semantic) and the hybrid mode (Result.ModeUsed), beside SemanticOff and
// the query modes.
const (
	SemanticReady   = "ready"
	SemanticPartial = "partial"
	SemanticError   = "error"
	// SemanticSwitching is a new model filling to replace the active one: the old model is
	// offline, so the query is answered by words until the new one covers every chunk.
	SemanticSwitching = "switching"
	ModeHybrid        = "hybrid"
)

// ErrModelInUse is a model PurgeModel cannot remove: the active one, or the target.
var ErrModelInUse = errs.Sentinel(errs.ErrPrecondition, "index: the model is in use")

// ErrEmbedFailed is a semantic query whose text the provider could not embed (EmbedFailed,
// -32067): retry later, or search lexically.
var ErrEmbedFailed = errors.New("index: the query could not be embedded")

// ErrSwitching is a semantic query while a new model fills: the old model is offline, and the new
// one does not cover every chunk yet (Switching, -32069). Search lexically until it does.
var ErrSwitching = errors.New("index: a new model is filling; search by words until it is ready")

// errOffline is embedding with a model other than the target's: the one a filling target
// replaces, offline for the switch.
var errOffline = errors.New("index: the model is offline while a new one fills")

// The semantic tier's constants (ADR 0204 §4.4, §4.5).
const (
	embedBatch   = 64               // texts per provider call
	refusedFor   = time.Hour        // how long a rejected text is set aside before it is tried again
	batchTimeout = 2 * time.Minute  // one provider call for a batch
	queryTimeout = 30 * time.Second // one provider call for a query
	hammingTop   = 200              // candidates the 1-bit scan passes to float32 rescoring, per window
)

// code is one chunk's 1-bit code: the signs of its vector's dimensions.
type code = vector.Code[int64]

// codeIndex is the code index of one model at one commit (its watermark is the commit_seq): the
// codes of the alive chunks of the semantic-ready documents, by document. It is never changed once
// published; the next commit publishes another, sharing the documents that did not change.
type codeIndex = vector.Index[int64, int64]

// vecItem is one vector for the writer to store.
type vecItem struct {
	textHash []byte
	vec      []float32
}

// vecBatch is the embedding worker's handoff: the vectors of one model, or none, which asks the
// writer whether the model now covers every alive chunk.
type vecBatch struct {
	fp    string
	items []vecItem
	done  chan error
}

type refusalEntry struct {
	err           error
	retryAt       time.Time
	bytes, tokens int
}

// RefusedText is an alive text a provider rejected; one hash may appear in several paths.
type RefusedText struct {
	Path, Breadcrumb, Error string
	Hash                    string // internal correlation; never sent over RPC
	Bytes, Tokens           int
	RetryAt                 time.Time
}

// semantic is the Indexer's embedding tier: nil without a provider.
type semantic struct {
	target  embed.Provider
	snap    atomic.Pointer[codeIndex]
	vectors chan vecBatch
	wake    chan struct{}

	mu       sync.Mutex
	activeFP string // the writer's view: set at start and at the flip
	lastErr  error  // the worker's last provider failure, nil after a success
	// refused holds the texts a provider rejected (fingerprint, then text hash), and until when they
	// are set aside: their documents answer lexically, and the other texts go on. The worker's own.
	refused     map[string]refusalEntry
	fillStart   time.Time
	fillTexts   int64
	fillTotal   int64
	fillBatches int64
	fillDone    bool

	snapshotScans, fallbackScans atomic.Int64 // for tests: which path queries took
	// beforeFetch, when set (tests only), runs inside a semantic query's read transaction before it
	// validates and rescores its first candidates.
	beforeFetch func()
}

func newSemantic(target embed.Provider) *semantic {
	return &semantic{target: target, vectors: make(chan vecBatch), wake: make(chan struct{}, 1),
		refused: map[string]refusalEntry{}}
}

func (m *semantic) active() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeFP
}

// switching is a target filling to replace another active model, which is offline meanwhile. An
// active model not yet read (the indexer starting) is no switch.
func (m *semantic) switching() bool {
	a := m.active()
	return a != "" && a != m.target.Model().Fingerprint()
}

// provider is the one that embeds with model fp: only the target's, since the model a filling
// target replaces is offline.
func (m *semantic) provider(fp string) (embed.Provider, error) {
	if fp != m.target.Model().Fingerprint() {
		return nil, errOffline
	}
	return m.target, nil
}

func (m *semantic) signal() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// setupModels records the target model and, when none is active yet, makes it active: the first
// model has nothing to keep answering. Another active model keeps answering until the target
// covers every chunk (commitVectors flips then). The target is recorded as such, and a model that
// was the target and is no longer, and is not active, is reclaimed: a cancelled switch's, or one
// switched past (ADR 1791284787 §2.3).
func (x *Indexer) setupModels(ctx context.Context) error {
	m := x.sem
	target := m.target.Model()
	s := x.store
	var active string
	var untargeted []string
	err := s.db.Write(ctx, func(tx *store.Tx) error {
		row := s.sc.Models(tx).Set(store.ModelFP, target.Fingerprint()).Set(store.ModelProvider, target.Provider).
			Set(store.ModelName, target.Name).Set(store.ModelDims, int64(target.Dims)).Set(store.ModelActive, int64(0))
		if err := dao.UpsertOnly(row); err != nil { // kept as it is when the model is known
			return err
		}
		was, err := s.sc.Models(tx).With(store.ModelTarget, int64(1)).Excluding(store.ModelFP, target.Fingerprint()).
			Select(store.ModelFP, store.ModelActive)
		if err != nil {
			return err
		}
		for _, w := range was {
			if w.Active == 0 {
				untargeted = append(untargeted, w.FP)
			}
		}
		if err := s.sc.Models(tx).With(store.ModelTarget, int64(1)).Set(store.ModelTarget, int64(0)).Update(); err != nil {
			return err
		}
		if err := s.sc.Models(tx).With(store.ModelFP, target.Fingerprint()).Set(store.ModelTarget, int64(1)).Update(); err != nil {
			return err
		}
		if active, err = s.activeModel(tx); err != nil {
			return err
		}
		if active == "" {
			active = target.Fingerprint()
			if err := s.sc.Models(tx).With(store.ModelFP, active).Set(store.ModelActive, int64(1)).Update(); err != nil {
				return err
			}
			if err := s.setReady(tx, nil); err != nil {
				return err
			}
		}
		_, err = s.bumpSeq(tx)
		return err
	})
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.activeFP = active
	m.mu.Unlock()
	for _, fp := range untargeted {
		x.reclaimLater(fp)
	}
	return x.publish(ctx, nil)
}

// bumpSeq advances commit_seq, as every writer transaction does, and returns the new value.
func (s *Store) bumpSeq(tx *store.Tx) (int64, error) {
	if err := s.sc.Self(tx).Set(store.WorkspaceCommitSeq, dao.Incr(1)).Update(); err != nil {
		return 0, err
	}
	return s.commitSeq(tx)
}

// commitSeq is the workspace's commit_seq in tx.
func (s *Store) commitSeq(tx *store.Tx) (int64, error) {
	w, err := s.sc.Self(tx).Get(store.WorkspaceCommitSeq)
	if err != nil {
		return 0, err
	}
	return w.CommitSeq, nil
}

// setReady recomputes semantic_ready for docs (nil: every document): a document is ready when
// there is an active model and every chunk alive at its active generation has a vector under it.
func (s *Store) setReady(tx *store.Tx, docs []int64) error {
	if docs != nil && len(docs) == 0 {
		return nil
	}
	all := docs == nil
	if all {
		all, err := s.sc.Documents(tx).Select(store.DocID)
		if err != nil {
			return err
		}
		for _, d := range all {
			docs = append(docs, d.ID)
		}
	}
	fp, err := s.activeModel(tx)
	if err != nil {
		return err
	}
	var ready, unready []int64
	if fp == "" {
		unready = docs
	} else {
		var chunks []*store.Chunk
		for part := range inParts(docs) {
			rows, err := alive(s.sc.Chunks(tx)).With(store.ChunkDoc, part...).Select(store.ChunkDoc, store.ChunkTextHash)
			if err != nil {
				return err
			}
			chunks = append(chunks, rows...)
		}
		// every document: the model's every vector, read once; a few documents (a batch of vectors):
		// only their texts, looked up by key, so the cost follows the batch, not the workspace's
		// vectors (ADR 1791329335 §2.6)
		var have map[string]bool
		if all {
			have, err = s.embeddedTexts(tx, fp)
		} else {
			hashes := make([][]byte, len(chunks))
			for i, c := range chunks {
				hashes[i] = c.TextHash
			}
			have, err = s.embeddedAmong(tx, fp, hashes)
		}
		if err != nil {
			return err
		}
		missing := map[int64]bool{}
		for _, r := range chunks {
			if !have[string(r.TextHash)] {
				missing[r.DocID] = true
			}
		}
		for _, d := range docs {
			if missing[d] {
				unready = append(unready, d)
			} else {
				ready = append(ready, d)
			}
		}
	}
	for v, ids := range map[int64][]int64{1: ready, 0: unready} {
		for part := range inParts(ids) {
			if err := s.sc.Documents(tx).With(store.DocID, part...).Set(store.DocSemanticReady, v).Update(); err != nil {
				return err
			}
		}
	}
	return nil
}

// embeddedAmong is the subset of hashes with a vector under model fp, each looked up by key.
func (s *Store) embeddedAmong(tx *store.Tx, fp string, hashes [][]byte) (map[string]bool, error) {
	out := make(map[string]bool, len(hashes))
	for part := range hashParts(hashes) {
		rows, err := s.sc.Embeddings(tx).With(store.EmbModel, fp).With(store.EmbTextHash, part...).Select(store.EmbTextHash)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[string(r.TextHash)] = true
		}
	}
	return out, nil
}

// embeddedTexts is the set of text hashes with a vector under model fp.
func (s *Store) embeddedTexts(tx *store.Tx, fp string) (map[string]bool, error) {
	rows, err := s.sc.Embeddings(tx).With(store.EmbModel, fp).Select(store.EmbTextHash)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[string(r.TextHash)] = true
	}
	return out, nil
}

// inParts yields ids in parts small enough for one IN list.
func inParts(ids []int64) func(yield func([]any) bool) {
	return func(yield func([]any) bool) {
		for i := 0; i < len(ids); i += inPart {
			part := make([]any, 0, min(inPart, len(ids)-i))
			for _, id := range ids[i:min(i+inPart, len(ids))] {
				part = append(part, id)
			}
			if !yield(part) {
				return
			}
		}
	}
}

// inPart bounds an IN list well under SQLite's bind limit.
const inPart = 500

// commitVectors stores a batch of vectors in one transaction. Under the active model it makes the
// documents they complete ready; under the target it flips the active model once the target covers
// every alive chunk, and every document's readiness follows the new model. Under any other model
// (none embeds with one, but a batch is the writer's to check, not to trust) it drops them: such a
// model is reclaimed, and the choice of model is the target's to make, never a late batch's.
func (x *Indexer) commitVectors(ctx context.Context, vb vecBatch) error {
	m := x.sem
	s := x.store
	started := time.Now()
	changed := []int64{} // target batches change no active-model codes
	flipped := false
	superseded := "" // the model the flip retires: reclaimed once the new one is published
	err := s.db.Write(ctx, func(tx *store.Tx) error {
		if _, err := s.bumpSeq(tx); err != nil {
			return err
		}
		if vb.fp != m.active() && vb.fp != m.target.Model().Fingerprint() {
			return nil // a late batch of a retired model: reclaimed, never stored again
		}
		// a text no chunk has any more was replaced while the provider embedded it: its vector would
		// only be an orphan (ADR 1791329335)
		items, err := s.withChunks(tx, vb.items)
		if err != nil {
			return err
		}
		if len(items) > 0 {
			b := s.sc.EmbeddingBatch(tx).SkipConflicts()
			for _, it := range items {
				b.Add(map[store.EmbeddingField]any{store.EmbTextHash: it.textHash, store.EmbModel: vb.fp,
					store.EmbBits: vector.EncodeBits(vector.SignBits(it.vec)), store.EmbF32: vector.EncodeFloats(it.vec)})
			}
			if err := b.Flush(); err != nil {
				return err
			}
		}
		active := m.active()
		switch {
		case vb.fp == active && len(items) > 0:
			var err error
			if changed, err = s.docsWithText(tx, items); err != nil {
				return err
			}
			return s.setReady(tx, changed)
		case vb.fp == m.target.Model().Fingerprint():
			missing, err := s.pendingUnrefused(tx, vb.fp, m.skip(vb.fp))
			if err != nil {
				return err
			}
			if missing == 0 {
				var err error
				if superseded, err = s.activeModel(tx); err != nil {
					return err
				}
				if err := s.sc.Models(tx).With(store.ModelActive, int64(1)).Set(store.ModelActive, int64(0)).Update(); err != nil {
					return err
				}
				if err := s.sc.Models(tx).With(store.ModelFP, vb.fp).Set(store.ModelActive, int64(1)).Update(); err != nil {
					return err
				}
				if err := s.setReady(tx, nil); err != nil {
					return err
				}
				flipped = true
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	committed := time.Since(started)
	if flipped {
		m.mu.Lock()
		m.activeFP = vb.fp
		fillStart, fillTexts, fillTotal := m.fillStart, m.fillTexts+int64(len(vb.items)), m.fillTotal
		m.mu.Unlock()
		if fillStart.IsZero() {
			fillStart = time.Now()
		}
		err := x.publishTimed(ctx, vb.fp, nil)
		if err == nil {
			m.mu.Lock()
			m.fillDone = true
			m.mu.Unlock()
			if superseded != vb.fp {
				// published: no query starts on it now, and one under way holds its own snapshot
				x.reclaimLater(superseded)
			}
			duration := time.Since(fillStart)
			logger.Info(x.opts.Logger, logger.Fields{"event": "embedding.fill.complete", "model": vb.fp,
				"texts": fillTexts, "sections": fillTotal, "refused": len(m.skip(vb.fp)),
				"duration_ms": duration.Milliseconds(), "texts_per_sec": float64(fillTexts) / max(duration.Seconds(), 0.001)})
		}
		return err
	}
	pubStart := time.Now()
	err = x.publishTimed(ctx, vb.fp, changed)
	logger.Debug(x.opts.Logger, logger.Fields{"event": "embedding.batch.write", "model": vb.fp, "commit_ms": committed.Milliseconds(), "publish_ms": time.Since(pubStart).Milliseconds()})
	return err
}

func (x *Indexer) publishTimed(ctx context.Context, fp string, changed []int64) error {
	start := time.Now()
	err := x.publish(ctx, changed)
	if elapsed := time.Since(start); elapsed > time.Second {
		logger.Warning(x.opts.Logger, err, logger.Fields{"event": "embedding.publish.slow", "model": fp, "duration_ms": elapsed.Milliseconds()})
	}
	return err
}

func (s *Store) docsWithText(tx *store.Tx, items []vecItem) ([]int64, error) {
	hashes := make([]any, len(items))
	for i, it := range items {
		hashes[i] = it.textHash
	}
	// not SELECT DISTINCT doc_id: without statistics SQLite would take chunk_doc for its order, and
	// scan the workspace's chunks; a plain select looks each text up in chunk_text (ADR 1791329335)
	rows, err := s.sc.Chunks(tx).With(store.ChunkTextHash, hashes...).Select(store.ChunkDoc)
	if err != nil {
		return nil, err
	}
	out := make([]int64, 0, len(rows))
	seen := make(map[int64]bool, len(rows))
	for _, r := range rows {
		if !seen[r.DocID] {
			seen[r.DocID] = true
			out = append(out, r.DocID)
		}
	}
	return out, nil
}

// pendingTexts counts the distinct texts of alive chunks with no vector under model fp.
func (s *Store) pendingTexts(tx *store.Tx, fp string) (int64, error) {
	_, missing, err := s.textCoverage(tx, fp)
	if err != nil {
		return 0, err
	}
	return missing[0], nil
}

// pendingUnrefused counts target hashes with neither a vector nor a current refusal.
func (s *Store) pendingUnrefused(tx *store.Tx, fp string, skip map[string]bool) (int64, error) {
	have, err := s.embeddedTexts(tx, fp)
	if err != nil {
		return 0, err
	}
	rows, err := alive(s.sc.Chunks(tx)).Select(store.ChunkTextHash)
	if err != nil {
		return 0, err
	}
	seen := map[string]bool{}
	var missing int64
	for _, r := range rows {
		h := string(r.TextHash)
		if !seen[h] && !have[h] && !skip[h] {
			missing++
		}
		seen[h] = true
	}
	return missing, nil
}

// textCoverage is the distinct texts of the alive chunks, and how many of them each model fps
// has no vector for, from one listing of the chunks.
func (s *Store) textCoverage(tx *store.Tx, fps ...string) (total int64, missing []int64, err error) {
	have := make([]map[string]bool, len(fps))
	for i, fp := range fps {
		if have[i], err = s.embeddedTexts(tx, fp); err != nil {
			return 0, nil, err
		}
	}
	rows, err := alive(s.sc.Chunks(tx)).Select(store.ChunkTextHash)
	if err != nil {
		return 0, nil, err
	}
	texts := map[string]bool{}
	for _, r := range rows {
		texts[string(r.TextHash)] = true
	}
	missing = make([]int64, len(fps))
	for h := range texts {
		for i := range fps {
			if !have[i][h] {
				missing[i]++
			}
		}
	}
	return int64(len(texts)), missing, nil
}

// publish swaps in the code snapshot of the commit just made: changed lists the documents whose
// codes may differ (nil: rebuild them all). A commit that changed none still publishes, a new
// header over the same codes, so every commit_seq has its snapshot. Only the writer publishes,
// after each commit and before its next batch, so the read transaction here sees that commit.
func (x *Indexer) publish(ctx context.Context, changed []int64) error {
	m := x.sem
	if m == nil {
		return nil
	}
	if x.opts.beforePublish != nil {
		x.opts.beforePublish()
	}
	s := x.store
	return s.read(ctx, func(tx *store.Tx) error {
		watermark, err := s.commitSeq(tx)
		if err != nil {
			return err
		}
		fp := m.active()
		cur := m.snap.Load()
		var next *codeIndex
		switch {
		case cur == nil || cur.Model() != fp || changed == nil:
			codes, err := s.loadCodes(tx, fp, nil)
			if err != nil {
				return err
			}
			next = vector.NewIndex(fp, watermark, codes)
		default:
			var fresh map[int64][]code
			if len(changed) > 0 {
				if fresh, err = s.loadCodes(tx, fp, changed); err != nil {
					return err
				}
			}
			next = cur.Next(watermark, changed, fresh)
		}
		m.snap.Store(next)
		return nil
	})
}

// withVectors narrows an alive-chunk query to the chunks of ready documents that have a vector
// under model fp, joining the vector.
func withVectors(d dao.DAO[*store.Chunk, store.ChunkField, int64], fp string) dao.DAO[*store.Chunk, store.ChunkField, int64] {
	return d.Join(store.JoinEmbedding).WithPredicate(dao.Eq(`"embedding"."model_fp"`, fp)).WithPredicate(dao.Eq(`"document"."semantic_ready"`, 1))
}

// loadCodes reads the codes of the alive chunks of ready documents under model fp: of docs, or of
// every document when docs is nil.
func (s *Store) loadCodes(tx *store.Tx, fp string, docs []int64) (map[int64][]code, error) {
	out := map[int64][]code{}
	add := func(d dao.DAO[*store.Chunk, store.ChunkField, int64]) error {
		rows, err := withVectors(alive(d), fp).Select(store.ChunkDoc, store.ChunkID, store.ChunkEmbBits)
		for _, r := range rows {
			out[r.DocID] = append(out[r.DocID], code{Chunk: r.ID, Bits: vector.DecodeBits(r.EmbBits)})
		}
		return err
	}
	if docs == nil {
		return out, add(s.sc.Chunks(tx))
	}
	for part := range inParts(docs) {
		if err := add(s.sc.Chunks(tx).With(store.ChunkDoc, part...)); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// embedLoop is the embedding worker: it embeds the alive chunks that lack a vector under the
// target (the active model, or the one filling to replace it), 64 distinct texts at a time, and
// hands the vectors to the writer. A provider failure waits and tries again, doubling the wait up
// to a minute.
func (x *Indexer) embedLoop(ctx context.Context) {
	m := x.sem
	wait := x.opts.RetryDelay
	poll := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	for ctx.Err() == nil {
		worked, err := x.embedOnce(ctx)
		m.mu.Lock()
		m.lastErr = err
		m.mu.Unlock()
		switch {
		case err != nil:
			if ctx.Err() == nil {
				logger.Warning(x.opts.Logger, err, logger.Fields{"event": "embedding.batch.failed", "model": m.target.Model().Fingerprint(), "retry_after_ms": wait.Milliseconds()})
			}
			if !sleepCtx(ctx, wait) {
				return
			}
			wait = min(2*wait, time.Minute)
			continue
		case worked:
			wait = x.opts.RetryDelay
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-poll.C:
		}
	}
}

// EmbedBatch is the daemon queue's one unit of work. It uses the same writer
// handoff as the standalone worker, and keeps provider failures in status.
func (x *Indexer) EmbedBatch(ctx context.Context, position int) (bool, error) {
	if x.sem == nil {
		return false, nil
	}
	x.embedPosition.Store(int64(position))
	worked, err := x.embedOnce(ctx)
	x.sem.mu.Lock()
	x.sem.lastErr = err
	x.sem.mu.Unlock()
	return worked, err
}

// embedOnce embeds one batch for the target, reporting whether there was one.
func (x *Indexer) embedOnce(ctx context.Context) (bool, error) {
	m := x.sem
	p := m.target
	fp, dims := p.Model().Fingerprint(), p.Model().Dims
	started := time.Now()
	hashes, texts, err := x.store.unembedded(ctx, fp, m.skip(fp))
	scanned := time.Since(started)
	if err != nil {
		return false, err
	}
	if len(texts) == 0 {
		if m.switching() {
			// nothing left for the target: the writer checks coverage, and flips
			return false, x.handVectors(ctx, vecBatch{fp: fp})
		}
		m.mu.Lock()
		start, count, total, done := m.fillStart, m.fillTexts, m.fillTotal, m.fillDone
		m.fillDone = true
		m.mu.Unlock()
		if !done && !start.IsZero() {
			duration := time.Since(start)
			logger.Info(x.opts.Logger, logger.Fields{"event": "embedding.fill.complete", "model": fp,
				"texts": count, "sections": total, "refused": len(m.skip(fp)),
				"duration_ms": duration.Milliseconds(), "texts_per_sec": float64(count) / max(duration.Seconds(), 0.001)})
		}
		return false, nil
	}
	m.mu.Lock()
	first := m.fillStart.IsZero() || m.fillDone
	m.mu.Unlock()
	if first {
		var total int64
		_ = x.store.read(ctx, func(tx *store.Tx) error {
			var err error
			total, _, err = x.store.textCoverage(tx, fp)
			return err
		})
		m.mu.Lock()
		m.fillStart = time.Now()
		m.fillTotal = total
		m.fillTexts, m.fillBatches, m.fillDone = 0, 0, false
		m.mu.Unlock()
		logger.Info(x.opts.Logger, logger.Fields{"event": "embedding.fill.start", "model": fp, "sections": total})
	}
	before := m.skip(fp)
	vb := vecBatch{fp: fp}
	called := time.Now()
	if err := m.embedSome(ctx, p, fp, dims, hashes, texts, &vb); err != nil {
		return false, err
	}
	providerTime := time.Since(called)
	for hash := range m.skip(fp) {
		if before[hash] {
			continue
		}
		refused, lookupErr := x.refusedTexts(ctx, fp, hash)
		if lookupErr != nil {
			logger.Warning(x.opts.Logger, lookupErr, logger.Fields{"event": "embedding.refusal.paths.failed", "model": fp})
		}
		for _, r := range refused {
			if r.Hash != hash {
				continue
			}
			logger.Warning(x.opts.Logger, nil, logger.Fields{"event": "embedding.text.refused", "model": fp,
				"path": r.Path, "breadcrumb": r.Breadcrumb, "error": r.Error, "bytes": r.Bytes,
				"estimated_tokens": r.Tokens, "retry_at": r.RetryAt.Format(time.RFC3339)})
		}
	}
	committed := time.Now()
	if len(vb.items) > 0 {
		if err := x.handVectors(ctx, vb); err != nil {
			return false, err
		}
	}
	logger.Debug(x.opts.Logger, logger.Fields{"event": "embedding.batch", "workspace": x.opts.Workspace,
		"queue_position": x.embedPosition.Load(), "model": fp, "texts": len(vb.items),
		"estimated_tokens": estimatedTokens(texts), "scan_ms": scanned.Milliseconds(), "provider_ms": providerTime.Milliseconds(), "commit_publish_ms": time.Since(committed).Milliseconds()})
	m.mu.Lock()
	if !m.fillDone {
		m.fillTexts += int64(len(vb.items))
	}
	m.fillBatches++
	fillTexts, fillBatches, fillStart := m.fillTexts, m.fillBatches, m.fillStart
	m.mu.Unlock()
	if fillBatches%10 == 0 {
		logger.Info(x.opts.Logger, logger.Fields{"event": "embedding.fill.rate", "model": fp,
			"texts": fillTexts, "texts_per_sec": float64(fillTexts) / max(time.Since(fillStart).Seconds(), 0.001)})
	}
	return true, nil
}

func estimatedTokens(texts []string) int {
	total := 0
	for _, text := range texts {
		total += chunk.Tokens([]byte(text))
	}
	return total
}

// embedSome embeds texts into vb (embed.Bisect). A provider rejecting the input (not failing as a
// whole) is narrowed down by halves to the texts it rejects, which are set aside for refusedFor;
// the rest are embedded. Any other failure fails the batch, to be tried again after a wait.
func (m *semantic) embedSome(ctx context.Context, p embed.Provider, fp string, dims int, hashes [][]byte, texts []string, vb *vecBatch) error {
	vecs, rejected, err := embed.Bisect(ctx, p, texts, dims, batchTimeout)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range rejected {
		t := texts[r.Index]
		m.refused[fp+"\x00"+string(hashes[r.Index])] = refusalEntry{err: r.Err, retryAt: time.Now().Add(refusedFor), bytes: len(t), tokens: chunk.Tokens([]byte(t))}
	}
	for i, v := range vecs {
		if v == nil {
			continue
		}
		vb.items = append(vb.items, vecItem{textHash: hashes[i], vec: v})
		delete(m.refused, fp+"\x00"+string(hashes[i]))
	}
	return nil
}

// skip is the texts of model fp set aside now, as text hashes.
func (m *semantic) skip(fp string) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := map[string]bool{}
	for k, entry := range m.refused {
		if now.After(entry.retryAt) {
			delete(m.refused, k)
			continue
		}
		if f, h, ok := strings.Cut(k, "\x00"); ok && f == fp {
			out[h] = true
		}
	}
	return out
}

func (x *Indexer) handVectors(ctx context.Context, vb vecBatch) error {
	vb.done = make(chan error, 1)
	select {
	case x.sem.vectors <- vb:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-vb.done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// unembedded lists up to embedBatch distinct texts of alive chunks with no vector under fp, but
// those set aside, in the order their first chunks were written. The text is the row's embed, or,
// when that is "" (the built-in chunkers'), breadcrumb, a line feed and body: exactly what
// text_hash names (search.Chunk.EmbedText), so every chunk of one hash has the same text.
func (s *Store) unembedded(ctx context.Context, fp string, skip map[string]bool) ([][]byte, []string, error) {
	var hashes [][]byte
	var texts []string
	err := s.read(ctx, func(tx *store.Tx) error {
		have, err := s.embeddedTexts(tx, fp)
		if err != nil {
			return err
		}
		seen := map[string]bool{}
		var after int64
		for len(texts) < embedBatch {
			rows, err := alive(s.sc.Chunks(tx)).WithPredicate(dao.Gt(`"chunk"."id"`, after)).
				OrderBy(dao.Asc(store.ChunkByID)).Limit(inPart).
				Select(store.ChunkID, store.ChunkTextHash, store.ChunkBreadcrumb, store.ChunkBody, store.ChunkEmbed)
			if err != nil {
				return err
			}
			for _, r := range rows {
				after = r.ID
				h := string(r.TextHash)
				if have[h] || seen[h] || skip[h] || len(texts) == embedBatch {
					continue
				}
				seen[h] = true
				c := search.Chunk{Breadcrumb: r.Breadcrumb, Body: r.Body, Embed: r.Embed}
				hashes, texts = append(hashes, r.TextHash), append(texts, c.EmbedText())
			}
			if len(rows) < inPart {
				break
			}
		}
		return nil
	})
	return hashes, texts, err
}

// The states of a workspace's models (ModelInfo.State).
const (
	ModelActive = "active" // its vectors answer queries
	ModelTarget = "target" // filling to replace the active one
	ModelUnused = "unused" // neither: its vectors are kept until purged
)

// ModelInfo is one of a workspace's models and the room its vectors take (index.models). The sizes
// are of what each row holds: F32Bytes the float32 vectors (dims × 4 each), BitsBytes the 1-bit
// codes (one bit a dimension, in 64-bit words), KeyBytes the row's key (the 32-byte text hash, the
// fingerprint, and the workspace's id, a byte for any short of 128), before SQLite's own overhead.
type ModelInfo struct {
	FP, Provider, Name string
	Dims               int
	State              string
	Vectors            int64
	F32Bytes           int64
	BitsBytes          int64
	KeyBytes           int64
}

// Models are the workspace's models: the active one first, then the target, then the unused.
func (x *Indexer) Models(ctx context.Context) ([]ModelInfo, error) {
	target := ""
	if x.sem != nil {
		target = x.sem.target.Model().Fingerprint()
	}
	var out []ModelInfo
	s := x.store
	err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := s.sc.Models(tx).Select(store.ModelFP, store.ModelProvider, store.ModelName, store.ModelDims, store.ModelActive)
		if err != nil {
			return err
		}
		for _, r := range rows {
			count, err := s.sc.Embeddings(tx).With(store.EmbModel, r.FP).Count()
			if err != nil {
				return err
			}
			n := int64(count)
			dims := int(derefInt(r.Dims))
			m := ModelInfo{FP: r.FP, Provider: deref(r.Provider), Name: deref(r.Name), Dims: dims, State: ModelUnused,
				Vectors: n, F32Bytes: n * int64(dims) * 4, BitsBytes: n * int64((dims+63)/64) * 8,
				KeyBytes: n * int64(32+len(r.FP)+1)}
			switch {
			case r.Active == 1:
				m.State = ModelActive
			case r.FP == target:
				m.State = ModelTarget
			}
			out = append(out, m)
		}
		return nil
	})
	rank := map[string]int{ModelActive: 0, ModelTarget: 1, ModelUnused: 2}
	sort.SliceStable(out, func(i, j int) bool { return rank[out[i].State] < rank[out[j].State] })
	return out, err
}

// PurgeModel reclaims a model no longer used, at once rather than at the next start
// (index.purge_model; ADR 1791284787 §2.4). The active model and the target are refused, saying
// what to do instead: a workspace's active model goes with its provider, in AI models, and a
// switch's target with the switch's cancel.
func (x *Indexer) PurgeModel(ctx context.Context, fp string) error {
	var active, target string
	if x.sem != nil {
		active, target = x.sem.active(), x.sem.target.Model().Fingerprint()
	}
	err := x.store.read(ctx, func(tx *store.Tx) error {
		m, err := x.store.sc.Models(tx).With(store.ModelFP, fp).Get(store.ModelActive, store.ModelTarget)
		if errors.Is(err, dao.ErrNoRows) {
			return fmt.Errorf("index: no model %s: %w", fp, ErrNoDocument)
		}
		if err != nil {
			return err
		}
		switch {
		case m.Active == 1 || fp == active:
			return fmt.Errorf("%w: %s is the active model: choose another model, or remove its provider in AI models",
				ErrModelInUse, embed.ModelName(fp))
		case m.Target == 1 || fp == target:
			return fmt.Errorf("%w: %s is the switch's target: cancel the switch", ErrModelInUse, embed.ModelName(fp))
		}
		return nil
	})
	if err != nil {
		return err
	}
	r, err := x.store.reclaim(ctx, fp, x.do)
	if err == nil && !r.Gone {
		return fmt.Errorf("%w: %s became the target while it was purged", ErrModelInUse, embed.ModelName(fp))
	}
	return err
}

// semanticHits is the semantic retriever, inside the query's transaction: the 1-bit scan over the
// snapshot of model fp at the transaction's commit_seq (or, when there is none, the same scan over
// embedding rows in SQL), then windows of hammingTop candidates validated in the transaction (alive,
// ready, the filters) and rescored by dot product, until retrieverTop survive.
func (s *Store) semanticHits(tx *store.Tx, m *semantic, fp string, qvec []float32, opts QueryOpts, n int) ([]search.Candidate[int64], error) {
	watermark, err := s.commitSeq(tx)
	if err != nil {
		return nil, err
	}
	var codes iter.Seq[code]
	if snap := m.snap.Load(); snap.Usable(fp, watermark) {
		m.snapshotScans.Add(1)
		codes = snap.Codes()
	} else {
		m.fallbackScans.Add(1)
		stored, err := s.loadCodes(tx, fp, nil)
		if err != nil {
			return nil, err
		}
		codes = vector.NewIndex(fp, watermark, stored).Codes()
	}
	order := vector.Nearest(codes, vector.SignBits(qvec), cmp.Compare[int64])
	fetch := func(ids []int64) ([]vector.Vec[search.Candidate[int64]], error) {
		if m.beforeFetch != nil {
			m.beforeFetch()
		}
		in := make([]any, len(ids))
		for i, id := range ids {
			in[i] = id
		}
		d, ok, err := s.filtered(tx, withVectors(alive(s.sc.Chunks(tx)), fp).With(store.ChunkID, in...), opts)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, vector.SkipRest // no document has every filter: no window can hold a hit
		}
		rows, err := d.Select(store.ChunkDoc, store.ChunkOrd, store.ChunkDocPath, store.ChunkBreadcrumb, store.ChunkByteStart,
			store.ChunkByteEnd, store.ChunkDocActiveGen, store.ChunkBody, store.ChunkEmbF32)
		if err != nil {
			return nil, fmt.Errorf("index: semantic search: %w", err)
		}
		out := make([]vector.Vec[search.Candidate[int64]], 0, len(rows))
		for _, r := range rows {
			out = append(out, vector.Vec[search.Candidate[int64]]{F32: vector.DecodeFloats(r.EmbF32), Item: candidateOf(r, chunk.Snippet(r.Body))})
		}
		return out, nil
	}
	byPath := func(a, b search.Candidate[int64]) bool {
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Ord < b.Ord
	}
	best, err := vector.TwoStage(order, qvec, hammingTop, n, fetch, byPath)
	if err != nil {
		return nil, err
	}
	out := make([]search.Candidate[int64], len(best))
	for i, v := range best {
		out[i] = v.Item
	}
	return out, nil
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// EmbeddingStatus is index.status's embeddings (ADR 0204 §4.7).
type EmbeddingStatus struct {
	Provider string // the target's provider
	Model    string // the active model's fingerprint
	Target   string // the model being filled to replace it; "" when it is the active one
	Texts    int64  // distinct texts of the alive chunks: what a model embeds to cover them all
	Pending  int64  // those with no vector under the active model
	Semantic string // SemanticSwitching while Target fills; else SemanticReady, or SemanticPartial while any document is not ready
	LastErr  string // the embedding workers' last provider failure; "" after a success
	Refused  int    // texts the provider rejected under the active model, set aside for now
	// TargetPending counts missing vectors including refusals; TargetRefused is the
	// subset set aside now. A refusal does not hold the switch back.
	TargetPending int64
	TargetRefused int
	RefusedTexts  []RefusedText
}

// Status is the store's status, with the embedding tier's when the indexer has a provider.
func (x *Indexer) Status(ctx context.Context) (Status, error) {
	start := time.Now()
	defer func() {
		if elapsed := time.Since(start); elapsed > 250*time.Millisecond && x.opts.Logger != nil {
			logger.Warning(x.opts.Logger, nil, logger.Fields{"event": "embedding.status.slow", "duration_ms": elapsed.Milliseconds()})
		}
	}()
	h, err := x.holding(ctx)
	if err != nil {
		return Status{}, err
	}
	m := x.sem
	var es EmbeddingStatus
	measured := "" // the model the coverage is of: the target while it fills, else the active one
	if m != nil {
		es = EmbeddingStatus{Provider: m.target.Name(), Model: m.active(), Semantic: SemanticReady}
		if m.switching() {
			es.Target = m.target.Model().Fingerprint()
		}
		measured = es.Model
		if es.Target != "" {
			measured = es.Target
		}
	}
	snap, err := x.statusSnapshot(ctx, measured)
	if err != nil {
		return Status{}, err
	}
	st := snap.st
	// the kept snapshot is shared: its lists are copied, never handed out
	st.UnparsedFrontmatter, st.Failing = slices.Clone(st.UnparsedFrontmatter), slices.Clone(st.Failing)
	current, stale, unchecked := h.counts()
	st.Held, st.HeldStale, st.HeldUnchecked = current+stale+unchecked, stale, unchecked
	if m == nil {
		return st, nil
	}
	refusedFP := measured
	es.Texts = snap.texts
	if es.Target != "" {
		es.TargetPending, es.TargetRefused = snap.missing, len(m.skip(es.Target))
	} else {
		es.Pending = snap.missing
	}
	switch {
	case es.Target != "":
		es.Semantic = SemanticSwitching
	case snap.unready:
		es.Semantic = SemanticPartial
	}
	m.mu.Lock()
	if m.lastErr != nil {
		es.LastErr = m.lastErr.Error()
	}
	m.mu.Unlock()
	es.Refused = len(m.skip(es.Model))
	es.RefusedTexts, err = x.refusedTexts(ctx, refusedFP, "")
	if err != nil {
		return st, err
	}
	if x.semanticPaused.Load() {
		es.Semantic = SemanticOff
	}
	st.Embeddings = &es
	return st, nil
}

// refusedTexts resolves only the first twenty refused hashes to alive source paths.
func (x *Indexer) refusedTexts(ctx context.Context, fp, onlyHash string) ([]RefusedText, error) {
	m := x.sem
	m.mu.Lock()
	entries := make(map[string]refusalEntry)
	for key, entry := range m.refused {
		if f, hash, ok := strings.Cut(key, "\x00"); ok && f == fp && (onlyHash == "" || onlyHash == hash) && time.Now().Before(entry.retryAt) {
			entries[hash] = entry
		}
	}
	m.mu.Unlock()
	if len(entries) == 0 {
		return nil, nil
	}
	var out []RefusedText
	err := x.store.read(ctx, func(tx *store.Tx) error {
		checked := 0
		for hash, entry := range entries {
			if len(out) == 20 || checked == 20 {
				break
			}
			checked++
			rows, err := alive(x.store.sc.Chunks(tx)).Join(store.JoinDocument).With(store.ChunkTextHash, []byte(hash)).
				Limit(20-uint64(len(out))).Select(store.ChunkDocPath, store.ChunkBreadcrumb)
			if err != nil {
				return err
			}
			for _, r := range rows {
				out = append(out, RefusedText{Path: r.DocPath, Breadcrumb: r.Breadcrumb, Hash: hash, Error: entry.err.Error(),
					Bytes: entry.bytes, Tokens: entry.tokens, RetryAt: entry.retryAt})
			}
		}
		return nil
	})
	return out, err
}
