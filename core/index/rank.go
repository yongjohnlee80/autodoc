package index

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// Rank is the indexer's re-ranking stage (golib search/rank): Source holds the ranker in use, the
// daemon's one for every workspace. Texts are what the ranker reads, the chunks' own by default;
// Required makes a search that asks for the stage ranked or refused. A zero Rank has no stage.
type Rank struct {
	Source   rank.Source
	Texts    rank.TextSource
	Required bool
}

// searchers are s as it is, and wrapped in the stage when the indexer has one: a query that does
// not ask for the ranker is answered by s itself.
func (x *Indexer) searchers(s search.Searcher) searchers {
	r := x.opts.Rank
	if r.Source == nil {
		return searchers{plain: s}
	}
	texts := r.Texts
	if texts == nil {
		texts = x.store.chunkTexts
	}
	var opts []rank.Option
	if r.Required {
		opts = append(opts, rank.Required())
	}
	return searchers{plain: s, ranked: rank.NewSearcher(s, texts, r.Source, opts...)}
}

// chunkTexts is the ranker's default text for each hit: its chunk's breadcrumb and body, the text
// the chunk was found by, read in one transaction. A hit is its document's path, its generation and
// the chunk's byte range: a document gone or at another generation, or a range that is not exactly
// one alive chunk, is rank.ErrStale, and the search answers in recall order.
func (s *Store) chunkTexts(ctx context.Context, hits []search.Hit) ([]string, error) {
	out := make([]string, len(hits))
	if len(hits) == 0 {
		return out, nil
	}
	// two reads whatever the number of hits: their documents by path, then the chunks at their
	// byte starts, matched here to each hit's exact range and its document's generation. The second
	// read is a superset: every hit's document crossed with every hit's start, under each
	// generation still kept (until gc). A (document, start) has at most one chunk a generation, so
	// it reads at most documents × distinct starts × kept generations rows, hits² × generations at
	// worst. Starts seldom coincide across documents but at 0, so it is about (hits + documents) ×
	// kept generations: some hundreds of rows for a window of 50.
	err := s.read(ctx, func(tx *store.Tx) error {
		paths, starts := make([]any, 0, len(hits)), make([]any, 0, len(hits))
		for _, h := range hits {
			paths, starts = append(paths, h.Path), append(starts, int64(h.ByteStart))
		}
		docs, err := s.documents(tx).WithPredicate(dao.In(`"document"."path"`, paths)).Select(DocID, DocPath, DocActiveGen)
		if err != nil {
			return err
		}
		byPath := make(map[string]*Document, len(docs))
		ids := make([]any, 0, len(docs))
		for _, d := range docs {
			byPath[d.Path] = d
			ids = append(ids, d.ID)
		}
		var chunks []*Chunk
		if len(ids) > 0 {
			chunks, err = s.chunks(tx).WithPredicate(dao.In(`"chunk"."doc_id"`, ids)).
				WithPredicate(dao.In(`"chunk"."byte_start"`, starts)).
				Select(ChunkDoc, ChunkByteStart, ChunkByteEnd, ChunkGenFrom, ChunkGenTo, ChunkBreadcrumb, ChunkBody)
			if err != nil {
				return err
			}
		}
		type key struct{ doc, start, end int64 }
		at := map[key][]*Chunk{}
		for _, c := range chunks {
			k := key{c.DocID, c.ByteStart, c.ByteEnd}
			at[k] = append(at[k], c)
		}
		for i, h := range hits {
			d, ok := byPath[h.Path]
			if !ok {
				return fmt.Errorf("%w: %s", rank.ErrStale, h.Path)
			}
			if d.ActiveGen != h.Generation {
				return fmt.Errorf("%w: %s is at another generation", rank.ErrStale, h.Path)
			}
			var alive []*Chunk
			for _, c := range at[key{d.ID, int64(h.ByteStart), int64(h.ByteEnd)}] {
				if c.GenFrom <= d.ActiveGen && (c.GenTo == nil || *c.GenTo > d.ActiveGen) {
					alive = append(alive, c)
				}
			}
			if len(alive) != 1 {
				return fmt.Errorf("%w: %s has %d chunks at bytes %d-%d", rank.ErrStale, h.Path, len(alive), h.ByteStart, h.ByteEnd)
			}
			out[i] = alive[0].Breadcrumb + "\n" + alive[0].Body
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
