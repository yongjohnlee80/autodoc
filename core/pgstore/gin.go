package pgstore

import (
	"context"
	"fmt"
)

// MergePending merges the pending list of the chunks' text-search index (rag_chunk_tsv) into the
// index, as VACUUM would, and returns how many pages it merged. The index keeps one (fastupdate):
// a chunk's text holds many words, and writing them into the index one write at a time would slow
// a bulk load. Until merged, every lexical search reads the list whole, so the ingester merges it
// after a load: a refresh, or a pass that wrote many documents. The list is the index's, shared by
// every tenant; merging it twice, or from two servers, is safe.
//
// The document's own GIN indexes (facets, tags) keep no pending list (000009).
func (s *Store) MergePending(ctx context.Context) (int64, error) {
	rows, err := s.conn.QueryContext(ctx, "SELECT gin_clean_pending_list('rag_chunk_tsv'::regclass)")
	if err != nil {
		return 0, fmt.Errorf("pgstore: merging the text index's pending list: %w", err)
	}
	defer rows.Close()
	var pages int64
	if rows.Next() {
		if err := rows.Scan(&pages); err != nil {
			return 0, fmt.Errorf("pgstore: merging the text index's pending list: %w", err)
		}
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("pgstore: merging the text index's pending list: %w", err)
	}
	return pages, nil
}
