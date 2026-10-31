package index

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// Seeding a scale benchmark's corpus. benchEnv indexes its files the way an editor's saves arrive:
// a few hundred at a time, each waited for, their texts embedded 64 per vector commit. That is
// minutes at 10k documents, and an hour or more at 100k, before a single query is timed. seedEnv
// makes the same rows and the same snapshot with the pipeline's own parts, in larger steps: each
// file is cut by the indexer's prepare and kept by its writer port (storeWriter.Put) in one writer
// op per seedDocs documents, and the vectors of their texts, from the same fake provider, are
// handed to the writer as the embedding worker hands them (handVectors), seedTexts at a time, so
// commitVectors makes the documents ready and publishes their codes.

const (
	seedDocs  = 1000 // documents per writer op
	seedTexts = 2000 // texts per vector commit
)

// seedEnv is benchEnv's corpus of n files, seeded: every document semantic-ready, the snapshot at
// the head.
func seedEnv(b *testing.B, n int) *env {
	b.Helper()
	f := newFake("m", "a")
	// the bench embeds itself: no embedding loop races it for the writer
	e := newEnv(b, options{Provider: f, BatchDelay: time.Millisecond, BatchSize: 200, ExternalEmbedding: true})
	ctx := context.Background()
	x := e.ix
	fp := x.sem.target.Model().Fingerprint()
	seen := map[string]bool{}
	var hashes [][]byte
	var texts []string
	embed := func() {
		vecs, err := f.Embed(ctx, texts)
		if err != nil {
			b.Fatal(err)
		}
		vb := vecBatch{fp: fp}
		for i, v := range vecs {
			vb.items = append(vb.items, vecItem{textHash: hashes[i], vec: v})
		}
		if err := x.handVectors(ctx, vb); err != nil {
			b.Fatal(err)
		}
		hashes, texts = hashes[:0], texts[:0]
	}
	for from := 0; from < n; from += seedDocs {
		var docs []Doc
		for i := from; i < min(from+seedDocs, n); i++ {
			p := fmt.Sprintf("notes/%04d.md", i)
			e.write(p, benchNote(i))
			pr := x.prepare(ctx, workItem{path: p})
			if pr.err != nil || pr.delete || pr.hold {
				b.Fatalf("%s: prepared as err %v, delete %v, hold %v", p, pr.err, pr.delete, pr.hold)
			}
			docs = append(docs, pr.doc())
		}
		if err := x.do(ctx, func(ctx context.Context, tx *store.Tx) error {
			w := storeWriter{s: e.store, tx: tx, now: time.Now()}.batch()
			for _, d := range docs {
				if err := w.Put(ctx, d); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		for _, d := range docs {
			for _, c := range d.Chunks {
				if seen[string(c.TextHash)] {
					continue
				}
				seen[string(c.TextHash)] = true
				hashes, texts = append(hashes, c.TextHash), append(texts, c.EmbedText())
				if len(texts) == seedTexts {
					embed()
				}
			}
		}
	}
	if len(texts) > 0 {
		embed()
	}
	if x.sem.switching() {
		// the model is still the target: the writer checks its coverage, and flips
		if err := x.handVectors(ctx, vecBatch{fp: fp}); err != nil {
			b.Fatal(err)
		}
	}
	e.ready()
	e.atHead()
	return e
}
