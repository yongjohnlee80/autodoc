package index

import "testing"

// TestALexicalQueryNoChunkHoldsWholeFallsBackToAnyWord (AM-8): every word must match first, so a
// precise query still narrows; when no chunk holds them all, any one word is enough, and the chunk
// holding more of them ranks first. A question typed whole finds its answer instead of nothing.
func TestALexicalQueryNoChunkHoldsWholeFallsBackToAnyWord(t *testing.T) {
	e := newEnv(t, options{})
	e.put("embeddings.md", "# Embeddings\n\nConfigure the embedding provider and its model.\n",
		"provider.md", "# Provider\n\nA provider serves the model.\n",
		"other.md", "# Other\n\nNothing about it here.\n")

	// every word held by one chunk: the narrow answer, the fallback never runs
	if r := e.query("configure embedding provider", QueryOpts{Mode: ModeLexical}); len(r.Hits) != 1 || r.Hits[0].Path != "embeddings.md" {
		t.Fatalf("all words held: %+v, want embeddings.md alone", r.Hits)
	}
	// a question whose words no chunk holds together: any word matches, most words first
	r := e.query("how do I configure the embedding provider", QueryOpts{Mode: ModeLexical})
	if len(r.Hits) == 0 || r.Hits[0].Path != "embeddings.md" {
		t.Fatalf("a question typed whole: %+v, want embeddings.md first", r.Hits)
	}
	for _, h := range r.Hits {
		if h.Path == "other.md" {
			t.Fatalf("other.md, holding none of the words, matched: %+v", r.Hits)
		}
	}
	// one word that nothing holds: still nothing, no fallback to widen
	if r := e.query("zebra", QueryOpts{Mode: ModeLexical}); len(r.Hits) != 0 {
		t.Fatalf("a single unheld word: %+v, want none", r.Hits)
	}
}
