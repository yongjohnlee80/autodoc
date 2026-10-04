package index

import (
	"bytes"
	"context"
	"crypto/sha256"
	"slices"
	"testing"

	"github.com/yongjohnlee80/golib/search"
)

// TestAChunksOwnEmbedTextIsPartOfItsIdentity: a chunk with no embed text of its own hashes exactly
// as before (the built-ins' goldens hold every such hash), one with its own adds it, so a change to
// the embed text alone is a new chunk, and the text hash is the embed text's.
func TestAChunksOwnEmbedTextIsPartOfItsIdentity(t *testing.T) {
	plain := search.Chunk{Breadcrumb: "a.go > F", Body: "func F() {}"}
	h := sha256.New()
	h.Write([]byte(ChunkerVersion + "\x00a.go > F\x00func F() {}"))
	if got := hashed([]search.Chunk{plain})[0]; !bytes.Equal(got.hash, h.Sum(nil)) {
		t.Fatal("a chunk with no embed text of its own hashes otherwise than before")
	}
	sig, doc := plain, plain
	sig.Embed, doc.Embed = "func F()", "func F() // F does nothing"
	cs := hashed([]search.Chunk{plain, sig, doc})
	if bytes.Equal(cs[0].hash, cs[1].hash) || bytes.Equal(cs[1].hash, cs[2].hash) {
		t.Fatal("chunks differing only in their embed text share an identity")
	}
	if want := sha256.Sum256([]byte("func F()")); !bytes.Equal(cs[1].textHash, want[:]) {
		t.Fatal("the text hash is not the embed text's")
	}
}

// TestTheEmbeddingWorkerSendsTheStoredEmbedText: unembedded reads a row's embed text when it has
// one, else its breadcrumb, a line feed and body, so the text sent is the one its text hash names.
func TestTheEmbeddingWorkerSendsTheStoredEmbedText(t *testing.T) {
	e := newEnv(t, Options{})
	e.put("a.md", "# A\n\nalpha body\n", "b.md", "# B\n\nbeta body\n")
	embedText := "func Alpha() // the signature and its documentation"
	th := sha256.Sum256([]byte(embedText))
	e.exec(`UPDATE chunk SET embed = ?, text_hash = ? WHERE doc_id = (SELECT id FROM document WHERE path = 'a.md')`, embedText, th[:])
	hashes, texts, err := e.store.unembedded(context.Background(), "fp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(texts, embedText) || !slices.Contains(texts, "B\nbeta body") {
		t.Fatalf("texts %q: want the stored embed text, and the breadcrumb and body of a row with none", texts)
	}
	for i, s := range texts {
		if sum := sha256.Sum256([]byte(s)); !bytes.Equal(sum[:], hashes[i]) {
			t.Errorf("%q is sent under a text hash that does not name it", s)
		}
	}
}
