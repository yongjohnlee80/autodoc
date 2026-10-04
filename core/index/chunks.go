package index

import (
	"crypto/sha256"
	"fmt"

	"github.com/yongjohnlee80/golib/search"
)

// chunkT is a chunk as the store keeps it: golib's cut (search/chunk), with its two hashes. hash is
// its identity under ChunkerVersion: a chunker change changes every hash, so every chunk is
// rewritten once. A chunk whose embed text is its own (search.Chunk.Embed) adds it, so a change to
// it alone is a new chunk too; the built-ins' hashes are unchanged. textHash is exactly the text an
// embedding is made of, so a chunker change that keeps the text keeps its vector.
type chunkT struct {
	search.Chunk
	hash, textHash []byte
}

// hashed is chunks the built-in chunkers cut, with their hashes.
func hashed(chunks []search.Chunk) []chunkT { return hashedUnder(ChunkerVersion, chunks) }

// hashedUnder is chunks cut by a chunker of version, with their hashes: a chunker's change of
// version rewrites only its own documents' chunks (ADR 0216 §1.5).
func hashedUnder(version string, chunks []search.Chunk) []chunkT {
	out := make([]chunkT, len(chunks))
	for i, c := range chunks {
		h := sha256.New()
		h.Write([]byte(version))
		h.Write([]byte{0})
		h.Write([]byte(c.Breadcrumb))
		h.Write([]byte{0})
		h.Write([]byte(c.Body))
		if c.Embed != "" {
			h.Write([]byte{0})
			h.Write([]byte(c.Embed))
		}
		t := c.TextHash()
		out[i] = chunkT{Chunk: c, hash: h.Sum(nil), textHash: t[:]}
	}
	return out
}

// cutRegistered cuts d with a registered chunker. The chunker is another module's code: a panic is
// the document's error, as a refusal is, and never the indexer's.
func cutRegistered(c search.Chunker, version string, d search.Doc) (cs []search.Chunk, err error) {
	defer func() {
		if r := recover(); r != nil {
			cs, err = nil, fmt.Errorf("the %s chunker failed: %v", version, r)
		}
	}()
	return c.Chunk(d)
}
