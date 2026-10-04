package index

import (
	"crypto/sha256"

	"github.com/yongjohnlee80/golib/search"
)

// chunkT is a chunk as the store keeps it: golib's cut (search/chunk), with its two hashes. hash is
// its identity under ChunkerVersion: a chunker change changes every hash, so every chunk is
// rewritten once. textHash is exactly the text an embedding is made of, so a chunker change that
// keeps the text keeps its vector.
type chunkT struct {
	search.Chunk
	hash, textHash []byte
}

// hashed is chunks with their hashes.
func hashed(chunks []search.Chunk) []chunkT {
	out := make([]chunkT, len(chunks))
	for i, c := range chunks {
		h := sha256.New()
		h.Write([]byte(ChunkerVersion))
		h.Write([]byte{0})
		h.Write([]byte(c.Breadcrumb))
		h.Write([]byte{0})
		h.Write([]byte(c.Body))
		t := c.TextHash()
		out[i] = chunkT{Chunk: c, hash: h.Sum(nil), textHash: t[:]}
	}
	return out
}
