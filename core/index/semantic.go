package index

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"math/bits"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/embed"
)

// Semantic states (Result.Semantic) and the hybrid mode (Result.ModeUsed), beside SemanticOff and
// the query modes.
const (
	SemanticReady   = "ready"
	SemanticPartial = "partial"
	SemanticError   = "error"
	ModeHybrid      = "hybrid"
)

// ErrModelInUse is a model PurgeModel cannot remove: the active one, or the target.
var ErrModelInUse = errs.Sentinel(errs.ErrPrecondition, "index: the model is in use")

// ErrEmbedFailed is a semantic query whose text the provider could not embed (EmbedFailed,
// -32067): retry later, or search lexically.
var ErrEmbedFailed = errors.New("index: the query could not be embedded")

// The semantic tier's constants (ADR 0204 §4.4, §4.5).
const (
	embedBatch   = 64               // texts per provider call
	refusedFor   = time.Hour        // how long a rejected text is set aside before it is tried again
	batchTimeout = 2 * time.Minute  // one provider call for a batch
	queryTimeout = 30 * time.Second // one provider call for a query
	hammingTop   = 200              // candidates the 1-bit scan passes to float32 rescoring, per window
	snippetBytes = 200              // a semantic hit's snippet: the start of its text
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

// semantic is the Indexer's embedding tier: nil without a provider.
type semantic struct {
	target      embed.Provider
	providerFor func(embed.Model) (embed.Provider, error)
	snap        atomic.Pointer[codeSnap]
	vectors     chan vecBatch
	wake        [2]chan struct{} // one per worker (roleActive, roleTarget)

	mu        sync.Mutex
	activeFP  string                    // the writer's view: set at start and at the flip
	providers map[string]embed.Provider // by fingerprint
	lastErr   error                     // the worker's last provider failure, nil after a success
	// refused holds the texts a provider rejected (fingerprint, then text hash), and until when they
	// are set aside: their documents answer lexically, and the other texts go on. The worker's own.
	refused map[string]time.Time

	snapshotScans, fallbackScans atomic.Int64 // for tests: which path queries took
}

func newSemantic(target embed.Provider, providerFor func(embed.Model) (embed.Provider, error)) *semantic {
	return &semantic{target: target, providerFor: providerFor, vectors: make(chan vecBatch),
		wake:      [2]chan struct{}{make(chan struct{}, 1), make(chan struct{}, 1)},
		providers: map[string]embed.Provider{target.Model().Fingerprint(): target},
		refused:   map[string]time.Time{}}
}

func (m *semantic) active() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.activeFP
}

// provider is the one that embeds with model fp: the target, or one made for a model still active
// while the target fills.
func (m *semantic) provider(fp string, model embed.Model) (embed.Provider, error) {
	m.mu.Lock()
	p, ok := m.providers[fp]
	m.mu.Unlock()
	if ok {
		return p, nil
	}
	if m.providerFor == nil {
		return nil, fmt.Errorf("index: no provider for model %s", fp)
	}
	p, err := m.providerFor(model)
	if err != nil {
		return nil, err
	}
	if p.Model().Fingerprint() != fp {
		return nil, fmt.Errorf("index: the provider made for %s embeds with %s", fp, p.Model().Fingerprint())
	}
	m.mu.Lock()
	m.providers[fp] = p
	m.mu.Unlock()
	return p, nil
}

func (m *semantic) signal() {
	for _, w := range m.wake {
		select {
		case w <- struct{}{}:
		default:
		}
	}
}

// The embedding workers: one keeps the active model's vectors current, one fills the target while
// another model is active, so a long fill never holds back the vectors of new edits.
const (
	roleActive = iota
	roleTarget
)

// modelOf reads a model row back into an embed.Model; the digest lives in the fingerprint.
func modelOf(fp, provider, name string, dims int) embed.Model {
	m := embed.Model{Provider: provider, Name: name, Dims: dims}
	if parts := strings.Split(fp, "|"); len(parts) >= 4 {
		m.Digest = strings.Join(parts[2:len(parts)-1], "|")
	}
	return m
}

// setupModels records the target model and, when none is active yet, makes it active: the first
// model has nothing to keep answering. Another active model keeps answering until the target
// covers every chunk (commitVectors flips then).
func (x *Indexer) setupModels(ctx context.Context) error {
	m := x.sem
	target := m.target.Model()
	tx, err := x.store.w.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO model(fp, provider, name, dims, active) VALUES (?, ?, ?, ?, 0)",
		target.Fingerprint(), target.Provider, target.Name, target.Dims); err != nil {
		return err
	}
	var active string
	if err := scanOne(ctx, tx, &active, "SELECT fp FROM model WHERE active = 1"); errors.Is(err, errNoRow) {
		active = target.Fingerprint()
		if _, err := tx.ExecContext(ctx, "UPDATE model SET active = 1 WHERE fp = ?", active); err != nil {
			return err
		}
		if err := setReady(ctx, tx, nil); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if _, err := bumpSeq(ctx, tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	m.mu.Lock()
	m.activeFP = active
	m.mu.Unlock()
	return x.publish(ctx, nil)
}

// bumpSeq advances commit_seq, as every writer transaction does, and returns the new value.
func bumpSeq(ctx context.Context, tx dao.TxConn) (int64, error) {
	if _, err := tx.ExecContext(ctx, "UPDATE meta SET v = CAST(v AS INTEGER) + 1 WHERE k = 'commit_seq'"); err != nil {
		return 0, err
	}
	var v string
	if err := scanOne(ctx, tx, &v, "SELECT v FROM meta WHERE k = 'commit_seq'"); err != nil {
		return 0, err
	}
	return strconv.ParseInt(v, 10, 64)
}

// setReady recomputes semantic_ready for docs (nil: every document): a document is ready when
// every chunk alive at its active generation has a vector under the active model.
func setReady(ctx context.Context, tx dao.TxConn, docs []int64) error {
	q := `UPDATE document SET semantic_ready = (
		EXISTS (SELECT 1 FROM model WHERE active = 1) AND NOT EXISTS (
			SELECT 1 FROM chunk c WHERE c.doc_id = document.id
			AND c.gen_from <= document.active_gen AND (c.gen_to IS NULL OR c.gen_to > document.active_gen)
			AND NOT EXISTS (SELECT 1 FROM embedding e WHERE e.text_hash = c.text_hash
				AND e.model_fp = (SELECT fp FROM model WHERE active = 1))))`
	var args []any
	if docs != nil {
		if len(docs) == 0 {
			return nil
		}
		q += " WHERE id IN (?" + strings.Repeat(", ?", len(docs)-1) + ")"
		for _, d := range docs {
			args = append(args, d)
		}
	}
	_, err := tx.ExecContext(ctx, q, args...)
	return err
}

// commitVectors stores a batch of vectors in one transaction. Under the active model it makes the
// documents they complete ready; under the target it flips the active model once the target covers
// every alive chunk, and every document's readiness follows the new model. Under any other model
// (the one active before a flip, whose worker was still embedding) it only stores them: the choice
// of model is the target's to make, never a late batch's.
func (x *Indexer) commitVectors(ctx context.Context, vb vecBatch) error {
	m := x.sem
	tx, err := x.store.w.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := bumpSeq(ctx, tx); err != nil {
		return err
	}
	for _, it := range vb.items {
		if _, err := tx.ExecContext(ctx, "INSERT OR IGNORE INTO embedding(text_hash, model_fp, bits, f32) VALUES (?, ?, ?, ?)",
			it.textHash, vb.fp, codeBytes(signBits(it.vec)), floatBytes(it.vec)); err != nil {
			return err
		}
	}
	active := m.active()
	var changed []int64
	flipped := false
	switch {
	case vb.fp == active && len(vb.items) > 0:
		if changed, err = docsWithText(ctx, tx, vb.items); err != nil {
			return err
		}
		if err := setReady(ctx, tx, changed); err != nil {
			return err
		}
	case vb.fp == m.target.Model().Fingerprint():
		var missing int
		if err := scanOne(ctx, tx, &missing, `SELECT EXISTS (SELECT 1 FROM chunk c JOIN document d ON d.id = c.doc_id
			WHERE c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen)
			AND NOT EXISTS (SELECT 1 FROM embedding e WHERE e.text_hash = c.text_hash AND e.model_fp = ?))`, vb.fp); err != nil {
			return err
		}
		if missing == 0 {
			if _, err := tx.ExecContext(ctx, "UPDATE model SET active = (fp = ?)", vb.fp); err != nil {
				return err
			}
			if err := setReady(ctx, tx, nil); err != nil {
				return err
			}
			flipped = true
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if flipped {
		m.mu.Lock()
		m.activeFP = vb.fp
		m.mu.Unlock()
		return x.publish(ctx, nil)
	}
	return x.publish(ctx, changed)
}

func docsWithText(ctx context.Context, tx dao.TxConn, items []vecItem) ([]int64, error) {
	q := "SELECT DISTINCT doc_id FROM chunk WHERE text_hash IN (?" + strings.Repeat(", ?", len(items)-1) + ")"
	args := make([]any, len(items))
	for i, it := range items {
		args[i] = it.textHash
	}
	rows, err := tx.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var d int64
		if err := rows.Scan(&d); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// publish swaps in the code snapshot of the commit just made: changed lists the documents whose
// codes may differ (nil: rebuild them all). A commit that changed none still publishes, a new
// header over the same codes, so every commit_seq has its snapshot. Only the writer publishes,
// after each commit and before its next batch.
func (x *Indexer) publish(ctx context.Context, changed []int64) error {
	m := x.sem
	if m == nil {
		return nil
	}
	var seq string
	if err := scanOne(ctx, x.store.w, &seq, "SELECT v FROM meta WHERE k = 'commit_seq'"); err != nil {
		return err
	}
	watermark, err := strconv.ParseInt(seq, 10, 64)
	if err != nil {
		return err
	}
	fp := m.active()
	cur := m.snap.Load()
	next := &codeSnap{fp: fp, watermark: watermark}
	switch {
	case cur == nil || cur.fp != fp || changed == nil:
		if next.docs, err = loadCodes(ctx, x.store.w, fp, nil); err != nil {
			return err
		}
	case len(changed) == 0:
		next.docs = cur.docs
	default:
		fresh, err := loadCodes(ctx, x.store.w, fp, changed)
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
}

// loadCodes reads the codes of the alive chunks of ready documents under model fp: of docs, or of
// every document when docs is nil.
func loadCodes(ctx context.Context, q dao.Querier, fp string, docs []int64) (map[int64][]code, error) {
	query := `SELECT c.doc_id, c.id, e.bits FROM chunk c JOIN document d ON d.id = c.doc_id
		JOIN embedding e ON e.text_hash = c.text_hash AND e.model_fp = ?
		WHERE d.semantic_ready = 1 AND c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen)`
	args := []any{fp}
	if docs != nil {
		query += " AND c.doc_id IN (?" + strings.Repeat(", ?", len(docs)-1) + ")"
		for _, d := range docs {
			args = append(args, d)
		}
	}
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]code{}
	for rows.Next() {
		var doc int64
		var c code
		var b []byte
		if err := rows.Scan(&doc, &c.chunk, &b); err != nil {
			return nil, err
		}
		c.bits = bitsOf(b)
		out[doc] = append(out[doc], c)
	}
	return out, rows.Err()
}

// embedLoop is an embedding worker: it embeds the alive chunks that lack a vector under its model
// (the active one, or the target), 64 distinct texts at a time, and hands the vectors to the writer.
// A provider failure waits and tries again, doubling the wait up to a minute.
func (x *Indexer) embedLoop(ctx context.Context, role int) {
	m := x.sem
	wait := x.opts.RetryDelay
	poll := time.NewTicker(5 * time.Second)
	defer poll.Stop()
	for ctx.Err() == nil {
		worked, err := x.embedOnce(ctx, role)
		m.mu.Lock()
		m.lastErr = err
		m.mu.Unlock()
		switch {
		case err != nil:
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
		case <-m.wake[role]:
		case <-poll.C:
		}
	}
}

// embedOnce embeds one batch for the worker's model, reporting whether there was one.
func (x *Indexer) embedOnce(ctx context.Context, role int) (bool, error) {
	m := x.sem
	target, active := m.target.Model().Fingerprint(), m.active()
	fp := active
	if role == roleTarget {
		if target == active {
			return false, nil // nothing to fill: the target is the active model
		}
		fp = target
	}
	var provider, name string
	var dims int
	if err := scanRow(ctx, x.store.r, []any{&provider, &name, &dims}, "SELECT provider, name, dims FROM model WHERE fp = ?", fp); err != nil {
		return false, err
	}
	p, err := m.provider(fp, modelOf(fp, provider, name, dims))
	if err != nil {
		if fp != target {
			return false, nil // an old model answers only while the target fills: it gets no new vectors
		}
		return false, err
	}
	hashes, texts, err := unembedded(ctx, x.store.r, fp, m.skip(fp))
	if err != nil {
		return false, err
	}
	if len(texts) == 0 {
		if role == roleTarget {
			// nothing left for the target: the writer checks coverage, and flips
			return false, x.handVectors(ctx, vecBatch{fp: fp})
		}
		return false, nil
	}
	vb := vecBatch{fp: fp}
	if err := m.embedSome(ctx, p, fp, dims, hashes, texts, &vb); err != nil {
		return false, err
	}
	if len(vb.items) > 0 {
		if err := x.handVectors(ctx, vb); err != nil {
			return false, err
		}
	}
	return true, nil
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
		m.refused[fp+"\x00"+string(hashes[0])] = time.Now().Add(refusedFor)
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
	}
	return nil
}

// skip is the texts of model fp set aside now, as text hashes.
func (m *semantic) skip(fp string) map[string]bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	out := map[string]bool{}
	for k, until := range m.refused {
		if now.After(until) {
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
// those set aside: the text is breadcrumb, a line feed and body, exactly what text_hash names.
func unembedded(ctx context.Context, q dao.Querier, fp string, skip map[string]bool) ([][]byte, []string, error) {
	rows, err := q.QueryContext(ctx, `SELECT c.text_hash, MIN(c.breadcrumb || char(10) || c.body)
		FROM chunk c JOIN document d ON d.id = c.doc_id
		WHERE c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen)
		AND NOT EXISTS (SELECT 1 FROM embedding e WHERE e.text_hash = c.text_hash AND e.model_fp = ?)
		GROUP BY c.text_hash ORDER BY MIN(c.id) LIMIT ?`, fp, embedBatch+len(skip))
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var hashes [][]byte
	var texts []string
	for rows.Next() {
		var h []byte
		var t string
		if err := rows.Scan(&h, &t); err != nil {
			return nil, nil, err
		}
		if skip[string(h)] || len(texts) == embedBatch {
			continue
		}
		hashes, texts = append(hashes, h), append(texts, t)
	}
	return hashes, texts, rows.Err()
}

// PurgeModel removes a model's vectors and its row (index.purge_model). The active model and the
// target cannot be purged.
func (x *Indexer) PurgeModel(ctx context.Context, fp string) error {
	if x.sem != nil && (fp == x.sem.active() || fp == x.sem.target.Model().Fingerprint()) {
		return fmt.Errorf("%w: %s", ErrModelInUse, fp)
	}
	return x.do(ctx, func(ctx context.Context, tx dao.TxConn) error {
		var active int
		if err := scanOne(ctx, tx, &active, "SELECT active FROM model WHERE fp = ?", fp); errors.Is(err, errNoRow) {
			return fmt.Errorf("index: no model %s: %w", fp, ErrNoDocument)
		} else if err != nil {
			return err
		}
		if active == 1 {
			return fmt.Errorf("%w: %s is active", ErrModelInUse, fp)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM embedding WHERE model_fp = ?", fp); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "DELETE FROM model WHERE fp = ?", fp)
		return err
	})
}

// semanticHits is the semantic retriever, inside the query's transaction: the 1-bit scan over the
// snapshot of model fp at the transaction's commit_seq (or, when there is none, the same scan over
// embedding rows in SQL), then windows of hammingTop candidates validated in the transaction (alive,
// ready, the filters) and rescored by dot product, until retrieverTop survive.
func semanticHits(ctx context.Context, tx dao.Querier, m *semantic, fp string, qvec []float32, opts QueryOpts) ([]candidate, error) {
	var seq string
	if err := scanOne(ctx, tx, &seq, "SELECT v FROM meta WHERE k = 'commit_seq'"); err != nil {
		return nil, err
	}
	watermark, err := strconv.ParseInt(seq, 10, 64)
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
		codes, err := loadCodes(ctx, tx, fp, nil)
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
		where, args := filterSQL(opts)
		q := `SELECT c.doc_id, c.ord, d.path, c.breadcrumb, c.byte_start, c.byte_end, d.active_gen, c.body, e.f32
			FROM chunk c JOIN document d ON d.id = c.doc_id JOIN embedding e ON e.text_hash = c.text_hash AND e.model_fp = ?
			WHERE d.semantic_ready = 1 AND c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen)
			AND c.id IN (?` + strings.Repeat(", ?", len(window)-1) + ")" + where
		qargs := []any{fp}
		for _, s := range window {
			qargs = append(qargs, s.chunk)
		}
		rows, err := tx.QueryContext(ctx, q, append(qargs, args...)...)
		if err != nil {
			return nil, fmt.Errorf("index: semantic search: %w", err)
		}
		for rows.Next() {
			var r rescored
			var body string
			var f []byte
			if err := rows.Scan(&r.c.docID, &r.c.ord, &r.c.hit.Path, &r.c.hit.Breadcrumb, &r.c.hit.ByteStart, &r.c.hit.ByteEnd,
				&r.c.hit.Generation, &body, &f); err != nil {
				rows.Close()
				return nil, err
			}
			r.dot = dot(qvec, floatsOf(f))
			r.c.hit.Snippet = snippetOf(body)
			r.c.hit.Via = []string{ModeSemantic}
			valid = append(valid, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
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

// snippetOf is the start of a chunk's text, cut at a rune boundary.
func snippetOf(body string) string {
	if len(body) <= snippetBytes {
		return body
	}
	cut := snippetBytes
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + "…"
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
	Pending  int64  // distinct texts of alive chunks with no vector under the active model
	Semantic string // SemanticReady, or SemanticPartial while any document is not ready
	LastErr  string // the embedding workers' last provider failure; "" after a success
	Refused  int    // texts the provider rejected under the active model, set aside for now
	// TargetPending and TargetRefused are the same for the target while it fills: a refused text
	// holds the switch back, since the target flips only when it covers every chunk.
	TargetPending int64
	TargetRefused int
}

// Status is the store's status, with the embedding tier's when the indexer has a provider.
func (x *Indexer) Status(ctx context.Context) (Status, error) {
	st, err := x.store.Status(ctx)
	if err != nil || x.sem == nil {
		return st, err
	}
	m := x.sem
	es := EmbeddingStatus{Provider: m.target.Name(), Model: m.active(), Semantic: SemanticReady}
	if t := m.target.Model().Fingerprint(); t != es.Model {
		es.Target = t
	}
	var unready int
	pending := func(fp string, n *int64) error {
		return scanOne(ctx, x.store.r, n, `SELECT COUNT(DISTINCT c.text_hash) FROM chunk c JOIN document d ON d.id = c.doc_id
			WHERE c.gen_from <= d.active_gen AND (c.gen_to IS NULL OR c.gen_to > d.active_gen)
			AND NOT EXISTS (SELECT 1 FROM embedding e WHERE e.text_hash = c.text_hash AND e.model_fp = ?)`, fp)
	}
	if err := pending(es.Model, &es.Pending); err != nil {
		return st, err
	}
	if es.Target != "" {
		if err := pending(es.Target, &es.TargetPending); err != nil {
			return st, err
		}
		es.TargetRefused = len(m.skip(es.Target))
	}
	if err := scanOne(ctx, x.store.r, &unready, "SELECT EXISTS (SELECT 1 FROM document WHERE semantic_ready = 0)"); err != nil {
		return st, err
	}
	if unready == 1 {
		es.Semantic = SemanticPartial
	}
	m.mu.Lock()
	if m.lastErr != nil {
		es.LastErr = m.lastErr.Error()
	}
	m.mu.Unlock()
	es.Refused = len(m.skip(es.Model))
	st.Embeddings = &es
	return st, nil
}
