package index

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// VECTORS FOLLOW THEIR CHUNKS (ADR 1791329335) — a workspace keeps a vector only while some chunk of
// it, alive or dead, has its text: the vector of any model, the active one's and a switch's target's.
// A dead chunk keeps its text's vector until gc deletes the chunk (it is in no published snapshot by
// then), and a text two documents share keeps it while either has it.
//
// The writer keeps this in the transaction that would break it: gc and a document's removal delete
// the vectors of the texts whose last chunk they deleted (dropOrphans), and a batch of vectors stores
// none for a text no chunk has. Every check is a lookup in chunk_text (sqlite 000016). What a store
// held before, or what a missed path left, the start pass sweeps (sweepOrphans), in batches of keys
// it examines.

// orphanBatch is how many of a model's vectors one batch of the start pass examines (a variable, so
// a test can take smaller steps).
var orphanBatch uint64 = 2000

// hashParts yields hashes in parts small enough for one IN list.
func hashParts(hashes [][]byte) func(yield func([]any) bool) {
	return func(yield func([]any) bool) {
		for i := 0; i < len(hashes); i += inPart {
			part := make([]any, 0, min(inPart, len(hashes)-i))
			for _, h := range hashes[i:min(i+inPart, len(hashes))] {
				part = append(part, h)
			}
			if !yield(part) {
				return
			}
		}
	}
}

// referencedTexts is the subset of hashes that some chunk of the workspace has, alive or dead.
func (s *Store) referencedTexts(tx *store.Tx, hashes [][]byte) (map[string]bool, error) {
	out := make(map[string]bool, len(hashes))
	for part := range hashParts(hashes) {
		rows, err := dao.SelectDistinct(s.sc.Chunks(tx).With(store.ChunkTextHash, part...), store.ChunkTextHash)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			out[string(r.TextHash)] = true
		}
	}
	return out, nil
}

// unreferenced is those of hashes, once each, that no chunk of the workspace has.
func (s *Store) unreferenced(tx *store.Tx, hashes [][]byte) ([][]byte, error) {
	kept, err := s.referencedTexts(tx, hashes)
	if err != nil {
		return nil, err
	}
	var out [][]byte
	seen := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		if k := string(h); !kept[k] && !seen[k] {
			seen[k] = true
			out = append(out, h)
		}
	}
	return out, nil
}

// dropOrphans deletes, under every model, the vectors of those of hashes that no chunk has: called in
// the transaction that deleted their chunks, after the delete. It reports the texts whose vectors went.
func (s *Store) dropOrphans(tx *store.Tx, hashes [][]byte) (int, error) {
	if len(hashes) == 0 {
		return 0, nil
	}
	gone, err := s.unreferenced(tx, hashes)
	if err != nil {
		return 0, err
	}
	for part := range hashParts(gone) {
		if err := s.sc.Embeddings(tx).With(store.EmbTextHash, part...).Delete(); err != nil {
			return 0, err
		}
	}
	return len(gone), nil
}

// sweepOrphans deletes every vector of the workspace whose text no chunk has, model by model, in
// batches through write: each examines the next orphanBatch vectors in key order after a cursor, so
// its work is bounded by the keys it reads, not by the orphans it finds. A document committed between
// two batches waits behind one at most. It reports the vectors deleted, and returns the freed pages to
// the file system when it deleted any.
func (s *Store) sweepOrphans(ctx context.Context, write writeFn) (int, error) {
	var fps []string
	if err := s.read(ctx, func(tx *store.Tx) error {
		rows, err := s.sc.Models(tx).Select(store.ModelFP)
		for _, r := range rows {
			fps = append(fps, r.FP)
		}
		return err
	}); err != nil {
		return 0, err
	}
	deleted := 0
	for _, fp := range fps {
		var cursor []byte
		for {
			var examined int
			err := write(ctx, func(_ context.Context, tx *store.Tx) error {
				q := s.sc.Embeddings(tx).With(store.EmbModel, fp)
				if cursor != nil {
					q = q.WithPredicate(dao.Gt(`"embedding"."text_hash"`, cursor))
				}
				keys, err := q.OrderBy(dao.Asc(store.ByKey)).Limit(orphanBatch).Select(store.EmbTextHash)
				if err != nil || len(keys) == 0 {
					return err
				}
				examined, cursor = len(keys), keys[len(keys)-1].TextHash
				hashes := make([][]byte, len(keys))
				for i, k := range keys {
					hashes[i] = k.TextHash
				}
				gone, err := s.unreferenced(tx, hashes)
				if err != nil {
					return err
				}
				for part := range hashParts(gone) {
					if err := s.sc.Embeddings(tx).With(store.EmbModel, fp).With(store.EmbTextHash, part...).Delete(); err != nil {
						return err
					}
				}
				deleted += len(gone)
				return nil
			})
			if err != nil {
				return deleted, fmt.Errorf("index: sweeping %s's orphaned vectors: %w", fp, err)
			}
			if uint64(examined) < orphanBatch {
				break
			}
		}
	}
	if deleted > 0 {
		if err := s.db.IncrementalVacuum(ctx); err != nil {
			return deleted, err
		}
	}
	return deleted, nil
}

// SweepOrphans deletes the workspace's vectors whose text no chunk has, through the indexer's writer,
// once its models are recorded (setupModels).
func (x *Indexer) SweepOrphans(ctx context.Context) (int, error) {
	select {
	case <-x.ready:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return x.store.sweepOrphans(ctx, x.do)
}

// SweepOrphansStore does the same for a workspace no indexer writes: no provider, or its root
// unavailable.
func SweepOrphansStore(ctx context.Context, db *store.Store, workspace int64) (int, error) {
	s := Open(db, workspace)
	return s.sweepOrphans(ctx, s.storeWrite)
}

// withChunks is those of items whose text some chunk of the workspace has.
func (s *Store) withChunks(tx *store.Tx, items []vecItem) ([]vecItem, error) {
	if len(items) == 0 {
		return nil, nil
	}
	hashes := make([][]byte, len(items))
	for i, it := range items {
		hashes[i] = it.textHash
	}
	kept, err := s.referencedTexts(tx, hashes)
	if err != nil {
		return nil, err
	}
	out := items[:0:0]
	for _, it := range items {
		if kept[string(it.textHash)] {
			out = append(out, it)
		}
	}
	return out, nil
}
