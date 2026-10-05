package index

import (
	"context"
	"errors"
	"fmt"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// Rank is the indexer's re-ranking stage (golib search/rank): Source holds the ranker in use, the
// daemon's one for every workspace. Texts are what the ranker reads, the chunks' own by default;
// Required makes a search ranked or refused. A zero Rank has no stage.
type Rank struct {
	Source   rank.Source
	Texts    rank.TextSource
	Required bool
}

// ranked wraps s in the stage, when the indexer has one.
func (x *Indexer) ranked(s search.Searcher) search.Searcher {
	r := x.opts.Rank
	if r.Source == nil {
		return s
	}
	texts := r.Texts
	if texts == nil {
		texts = x.store.chunkTexts
	}
	var opts []rank.Option
	if r.Required {
		opts = append(opts, rank.Required())
	}
	return rank.NewSearcher(s, texts, r.Source, opts...)
}

// chunkTexts is the ranker's default text for each hit: its chunk's breadcrumb and body, the text
// the chunk was found by, read in one transaction. A hit is its document's path, its generation and
// the chunk's byte range: a document gone or at another generation, or a range that is not exactly
// one alive chunk, is rank.ErrStale, and the search answers in recall order.
func (s *Store) chunkTexts(ctx context.Context, hits []search.Hit) ([]string, error) {
	out := make([]string, len(hits))
	err := s.read(ctx, func(tx *store.Tx) error {
		type doc struct{ id, gen int64 }
		docs := map[string]doc{}
		for i, h := range hits {
			d, ok := docs[h.Path]
			if !ok {
				row, err := s.sc.Documents(tx).With(store.DocPath, h.Path).Get(store.DocID, store.DocActiveGen)
				if errors.Is(err, dao.ErrNoRows) {
					return fmt.Errorf("%w: %s", rank.ErrStale, h.Path)
				}
				if err != nil {
					return err
				}
				d = doc{row.ID, row.ActiveGen}
				docs[h.Path] = d
			}
			if d.gen != h.Generation {
				return fmt.Errorf("%w: %s is at another generation", rank.ErrStale, h.Path)
			}
			rows, err := s.sc.Chunks(tx).With(store.ChunkDoc, d.id).
				With(store.ChunkByteStart, int64(h.ByteStart)).With(store.ChunkByteEnd, int64(h.ByteEnd)).
				WithPredicate(dao.Lte(`"chunk"."gen_from"`, d.gen)).
				WithPredicate(dao.Or(dao.IsNull(`"chunk"."gen_to"`), dao.Gt(`"chunk"."gen_to"`, d.gen))).
				Limit(2).Select(store.ChunkBreadcrumb, store.ChunkBody)
			if err != nil {
				return err
			}
			if len(rows) != 1 {
				return fmt.Errorf("%w: %s has %d chunks at bytes %d-%d", rank.ErrStale, h.Path, len(rows), h.ByteStart, h.ByteEnd)
			}
			out[i] = rows[0].Breadcrumb + "\n" + rows[0].Body
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
