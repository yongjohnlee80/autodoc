package index

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search/chunk"

	"github.com/yongjohnlee80/autodoc/core/embed"
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
type code struct {
	chunk int64
	bits  []uint64
}

// codeSnap is the code index of one model at one commit: the codes of the alive chunks of the
// semantic-ready documents. It is never changed once published; the next commit publishes another,
// sharing the documents that did not change.
type codeSnap struct {
	fp        string
	watermark int64 // the commit_seq it reflects
	docs      map[int64][]code
}

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
	snap    atomic.Pointer[codeSnap]
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
// covers every chunk (commitVectors flips then).
func (x *Indexer) setupModels(ctx context.Context) error {
	m := x.sem
	target := m.target.Model()
	s := x.store
	var active string
	err := s.db.Write(ctx, func(tx *store.Tx) error {
		row := s.sc.Models(tx).Set(store.ModelFP, target.Fingerprint()).Set(store.ModelProvider, target.Provider).
			Set(store.ModelName, target.Name).Set(store.ModelDims, int64(target.Dims)).Set(store.ModelActive, int64(0))
		if err := dao.UpsertOnly(row); err != nil { // kept as it is when the model is known
			return err
		}
		var err error
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
	if docs == nil {
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
		have, err := s.embeddedTexts(tx, fp)
		if err != nil {
			return err
		}
		missing := map[int64]bool{}
		for part := range inParts(docs) {
			rows, err := alive(s.sc.Chunks(tx)).With(store.ChunkDoc, part...).Select(store.ChunkDoc, store.ChunkTextHash)
			if err != nil {
				return err
			}
			for _, r := range rows {
				if !have[string(r.TextHash)] {
					missing[r.DocID] = true
				}
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
// (none embeds with one, but a batch is the writer's to check, not to trust) it only stores them:
// the choice of model is the target's to make, never a late batch's.
func (x *Indexer) commitVectors(ctx context.Context, vb vecBatch) error {
	m := x.sem
	s := x.store
	started := time.Now()
	changed := []int64{} // target batches change no active-model codes
	flipped := false
	err := s.db.Write(ctx, func(tx *store.Tx) error {
		if _, err := s.bumpSeq(tx); err != nil {
			return err
		}
		if len(vb.items) > 0 {
			b := s.sc.EmbeddingBatch(tx).SkipConflicts()
			for _, it := range vb.items {
				b.Add(map[store.EmbeddingField]any{store.EmbTextHash: it.textHash, store.EmbModel: vb.fp,
					store.EmbBits: codeBytes(signBits(it.vec)), store.EmbF32: floatBytes(it.vec)})
			}
			if err := b.Flush(); err != nil {
				return err
			}
		}
		active := m.active()
		switch {
		case vb.fp == active && len(vb.items) > 0:
			var err error
			if changed, err = s.docsWithText(tx, vb.items); err != nil {
				return err
			}
			return s.setReady(tx, changed)
		case vb.fp == m.target.Model().Fingerprint():
			missing, err := s.pendingUnrefused(tx, vb.fp, m.skip(vb.fp))
			if err != nil {
				return err
			}
			if missing == 0 {
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
	rows, err := dao.SelectDistinct(s.sc.Chunks(tx).With(store.ChunkTextHash, hashes...), store.ChunkDoc)
	if err != nil {
		return nil, err
	}
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.DocID
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
	s := x.store
	return s.read(ctx, func(tx *store.Tx) error {
		watermark, err := s.commitSeq(tx)
		if err != nil {
			return err
		}
		fp := m.active()
		cur := m.snap.Load()
		next := &codeSnap{fp: fp, watermark: watermark}
		switch {
		case cur == nil || cur.fp != fp || changed == nil:
			if next.docs, err = s.loadCodes(tx, fp, nil); err != nil {
				return err
			}
		case len(changed) == 0:
			next.docs = cur.docs
		default:
			fresh, err := s.loadCodes(tx, fp, changed)
			if err != nil {
				return err
			}
			next.docs = make(map[int64][]code, len(cur.docs)+len(fresh))
			for d, cs := range cur.docs {
				next.docs[d] = cs
			}
			for _, d := range changed {
				delete(next.docs, d)
			}
			for d, cs := range fresh {
				next.docs[d] = cs
			}
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
			out[r.DocID] = append(out[r.DocID], code{chunk: r.ID, bits: bitsOf(r.EmbBits)})
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

// embedSome embeds texts into vb. A provider rejecting the input (not failing as a whole) is
// narrowed down by halves to the texts it rejects, which are set aside for refusedFor; the rest
// are embedded. Any other failure fails the batch, to be tried again after a wait.
func (m *semantic) embedSome(ctx context.Context, p embed.Provider, fp string, dims int, hashes [][]byte, texts []string, vb *vecBatch) error {
	cctx, cancel := context.WithTimeout(ctx, batchTimeout)
	vecs, err := p.Embed(cctx, texts)
	cancel()
	switch {
	case errors.Is(err, embed.ErrRejected) && len(texts) == 1:
		m.mu.Lock()
		m.refused[fp+"\x00"+string(hashes[0])] = refusalEntry{err: err, retryAt: time.Now().Add(refusedFor), bytes: len(texts[0]), tokens: chunk.Tokens([]byte(texts[0]))}
		m.mu.Unlock()
		return nil
	case errors.Is(err, embed.ErrRejected):
		half := len(texts) / 2
		if err := m.embedSome(ctx, p, fp, dims, hashes[:half], texts[:half], vb); err != nil {
			return err
		}
		return m.embedSome(ctx, p, fp, dims, hashes[half:], texts[half:], vb)
	case err != nil:
		return err
	case len(vecs) != len(texts):
		return fmt.Errorf("%w: %d vectors for %d texts", embed.ErrDims, len(vecs), len(texts))
	}
	for i, v := range vecs {
		if len(v) != dims {
			return fmt.Errorf("%w: %d dimensions, the model has %d", embed.ErrDims, len(v), dims)
		}
		vb.items = append(vb.items, vecItem{textHash: hashes[i], vec: normalized(v)})
		m.mu.Lock()
		delete(m.refused, fp+"\x00"+string(hashes[i]))
		m.mu.Unlock()
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
// those set aside, in the order their first chunks were written: the text is breadcrumb, a line
// feed and body, exactly what text_hash names, so every chunk of one hash has the same text.
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
				Select(store.ChunkID, store.ChunkTextHash, store.ChunkBreadcrumb, store.ChunkBody)
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
				hashes, texts = append(hashes, r.TextHash), append(texts, r.Breadcrumb+"\n"+r.Body)
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

// PurgeModel removes a model's vectors and its row (index.purge_model). The active model and the
// target cannot be purged.
func (x *Indexer) PurgeModel(ctx context.Context, fp string) error {
	if x.sem != nil && (fp == x.sem.active() || fp == x.sem.target.Model().Fingerprint()) {
		return fmt.Errorf("%w: %s", ErrModelInUse, fp)
	}
	sc := x.store.sc
	return x.do(ctx, func(ctx context.Context, tx *store.Tx) error {
		m, err := sc.Models(tx).With(store.ModelFP, fp).Get(store.ModelActive)
		if errors.Is(err, dao.ErrNoRows) {
			return fmt.Errorf("index: no model %s: %w", fp, ErrNoDocument)
		}
		if err != nil {
			return err
		}
		if m.Active == 1 {
			return fmt.Errorf("%w: %s is active", ErrModelInUse, fp)
		}
		if err := sc.Embeddings(tx).With(store.EmbModel, fp).Delete(); err != nil {
			return err
		}
		return sc.Models(tx).With(store.ModelFP, fp).Delete()
	})
}

// semanticHits is the semantic retriever, inside the query's transaction: the 1-bit scan over the
// snapshot of model fp at the transaction's commit_seq (or, when there is none, the same scan over
// embedding rows in SQL), then windows of hammingTop candidates validated in the transaction (alive,
// ready, the filters) and rescored by dot product, until retrieverTop survive.
func (s *Store) semanticHits(tx *store.Tx, m *semantic, fp string, qvec []float32, opts QueryOpts) ([]candidate, error) {
	watermark, err := s.commitSeq(tx)
	if err != nil {
		return nil, err
	}
	qbits := signBits(qvec)
	type scored struct {
		chunk int64
		dist  int
	}
	var all []scored
	add := func(c code) {
		d := 0
		for i, w := range c.bits {
			if i < len(qbits) {
				d += bits.OnesCount64(w ^ qbits[i])
			}
		}
		all = append(all, scored{c.chunk, d})
	}
	if snap := m.snap.Load(); snap != nil && snap.fp == fp && snap.watermark == watermark {
		m.snapshotScans.Add(1)
		for _, cs := range snap.docs {
			for _, c := range cs {
				add(c)
			}
		}
	} else {
		m.fallbackScans.Add(1)
		codes, err := s.loadCodes(tx, fp, nil)
		if err != nil {
			return nil, err
		}
		for _, cs := range codes {
			for _, c := range cs {
				add(c)
			}
		}
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].dist != all[j].dist {
			return all[i].dist < all[j].dist
		}
		return all[i].chunk < all[j].chunk
	})
	type rescored struct {
		c   candidate
		dot float64
	}
	var valid []rescored
	for start := 0; start < len(all) && len(valid) < retrieverTop; start += hammingTop {
		window := all[start:min(start+hammingTop, len(all))]
		ids := make([]any, len(window))
		for i, w := range window {
			ids[i] = w.chunk
		}
		d, ok, err := s.filtered(tx, withVectors(alive(s.sc.Chunks(tx)), fp).With(store.ChunkID, ids...), opts)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
		rows, err := d.Select(store.ChunkDoc, store.ChunkOrd, store.ChunkDocPath, store.ChunkBreadcrumb, store.ChunkByteStart,
			store.ChunkByteEnd, store.ChunkDocActiveGen, store.ChunkBody, store.ChunkEmbF32)
		if err != nil {
			return nil, fmt.Errorf("index: semantic search: %w", err)
		}
		for _, r := range rows {
			valid = append(valid, rescored{dot: dot(qvec, floatsOf(r.EmbF32)), c: candidate{docID: r.DocID, ord: int(r.Ord),
				hit: Hit{Path: r.DocPath, Breadcrumb: r.Breadcrumb, ByteStart: int(r.ByteStart), ByteEnd: int(r.ByteEnd),
					Generation: r.DocActiveGen, Snippet: chunk.Snippet(r.Body), Via: []string{ModeSemantic}}}})
		}
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].dot != valid[j].dot {
			return valid[i].dot > valid[j].dot
		}
		if valid[i].c.hit.Path != valid[j].c.hit.Path {
			return valid[i].c.hit.Path < valid[j].c.hit.Path
		}
		return valid[i].c.ord < valid[j].c.ord
	})
	out := make([]candidate, 0, min(len(valid), retrieverTop))
	for _, r := range valid[:min(len(valid), retrieverTop)] {
		out = append(out, r.c)
	}
	return out, nil
}

func normalized(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	out := make([]float32, len(v))
	if sum == 0 {
		return out
	}
	n := math.Sqrt(sum)
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

func dot(a, b []float32) float64 {
	var s float64
	for i := range min(len(a), len(b)) {
		s += float64(a[i]) * float64(b[i])
	}
	return s
}

// signBits is a vector's 1-bit code: bit i is set when dimension i is positive.
func signBits(v []float32) []uint64 {
	out := make([]uint64, (len(v)+63)/64)
	for i, x := range v {
		if x > 0 {
			out[i/64] |= 1 << (i % 64)
		}
	}
	return out
}

func codeBytes(ws []uint64) []byte {
	b := make([]byte, 8*len(ws))
	for i, w := range ws {
		binary.LittleEndian.PutUint64(b[8*i:], w)
	}
	return b
}

func bitsOf(b []byte) []uint64 {
	out := make([]uint64, len(b)/8)
	for i := range out {
		out[i] = binary.LittleEndian.Uint64(b[8*i:])
	}
	return out
}

func floatBytes(v []float32) []byte {
	b := make([]byte, 4*len(v))
	for i, x := range v {
		binary.LittleEndian.PutUint32(b[4*i:], math.Float32bits(x))
	}
	return b
}

func floatsOf(b []byte) []float32 {
	out := make([]float32, len(b)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(b[4*i:]))
	}
	return out
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
	st, err := x.store.Status(ctx)
	if err != nil || x.sem == nil {
		return st, err
	}
	m := x.sem
	es := EmbeddingStatus{Provider: m.target.Name(), Model: m.active(), Semantic: SemanticReady}
	if m.switching() {
		es.Target = m.target.Model().Fingerprint()
	}
	refusedFP := es.Model
	if es.Target != "" {
		refusedFP = es.Target
	}
	s := x.store
	if err := s.read(ctx, func(tx *store.Tx) error {
		texts, missing, err := s.textCoverage(tx, refusedFP)
		if err != nil {
			return err
		}
		es.Texts = texts
		if es.Target != "" {
			es.TargetPending, es.TargetRefused = missing[0], len(m.skip(es.Target))
		} else {
			es.Pending = missing[0]
		}
		unready, err := s.sc.Documents(tx).With(store.DocSemanticReady, int64(0)).Exists()
		switch {
		case es.Target != "":
			es.Semantic = SemanticSwitching
		case unready:
			es.Semantic = SemanticPartial
		}
		return err
	}); err != nil {
		return st, err
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
