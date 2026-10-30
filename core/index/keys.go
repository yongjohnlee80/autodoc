package index

import (
	"context"

	"github.com/yongjohnlee80/golib/dao"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE SORT KEYS' BACKFILL (ADR 1793312720 D2) — documents written before 000027 have no stored sort
// keys, and index.documents lists by the Go fallback while any workspace document lacks them. At
// start, the writer gives them their keys, keyBatch documents a commit, through docKeys (the
// function upsertDoc writes them with). It is resumable by its own gate: a restart finds the
// documents still without keys and goes on from there.

// keyBatch is the most documents one backfill commit keys (as AP-8 bounds a job write).
var keyBatch uint64 = 2000

// backfillKeysOnce keys at most keyBatch documents that have no keys yet, in one write, and answers
// how many it keyed.
func (s *Store) backfillKeysOnce(ctx context.Context, write writeFn) (int, error) {
	n := 0
	err := write(ctx, func(_ context.Context, tx *store.Tx) error {
		rows, err := s.documents(tx).WithPredicate(dao.IsNull(`"document"."k_upd_s"`)).
			Limit(keyBatch).Select(DocID, DocFrontmatterJSON, DocIndexedAt)
		if err != nil {
			return err
		}
		for _, r := range rows {
			updS, updNs, indexed := docKeysOf(r.FrontmatterJSON, r.IndexedAt)
			if err := s.documents(tx).With(DocID, r.ID).Set(DocKUpdS, updS).Set(DocKUpdNs, updNs).
				Set(DocKIndexed, indexed).Update(); err != nil {
				return err
			}
		}
		n = len(rows)
		return nil
	})
	return n, err
}

// backfillKeys keys every document of the workspace that has none, a batch a commit, and answers
// how many it keyed. Cut short (ctx, a failure), it leaves the rest to the next start.
func (s *Store) backfillKeys(ctx context.Context, write writeFn) (int, error) {
	total := 0
	for {
		n, err := s.backfillKeysOnce(ctx, write)
		total += n
		if err != nil || uint64(n) < keyBatch {
			return total, err
		}
	}
}

// BackfillKeys keys the workspace's documents that have no sort keys, through the indexer's writer,
// once it is ready.
func (x *Indexer) BackfillKeys(ctx context.Context) (int, error) {
	select {
	case <-x.ready:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	return x.store.backfillKeys(ctx, x.do)
}

// BackfillKeysStore does the same for a workspace no indexer writes: no provider, or its root
// unavailable.
func BackfillKeysStore(ctx context.Context, db *store.Store, workspace int64) (int, error) {
	s := NewStore(db, workspace)
	return s.backfillKeys(ctx, s.storeWrite)
}
