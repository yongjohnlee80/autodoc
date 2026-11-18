package search

import (
	"context"
	"testing"

	"github.com/yongjohnlee80/golib/search/embed"
)

// querier is a provider whose queries embed apart from its documents (embed.Querier), as
// Vertex's gemini-embedding-2 takes a query in its own prompt format.
type querier struct{ asked []string }

func (q *querier) Name() string { return "test" }
func (q *querier) Model() embed.Model {
	return embed.Model{Provider: "test", Name: "q", Dims: 2}
}
func (q *querier) Embed(_ context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		q.asked = append(q.asked, "document "+t)
		out[i] = []float32{1, 0}
	}
	return out, nil
}
func (q *querier) EmbedQuery(_ context.Context, text string) ([]float32, error) {
	q.asked = append(q.asked, "query "+text)
	return []float32{0, 1}, nil
}

// The library's QueryEmbedder embeds a query as a query when its provider has a query format
// (embed.Query), never as a document.
func TestAQueryIsEmbeddedAsAQuery(t *testing.T) {
	p := &querier{}
	_, vec, err := QueryEmbedder(p)(context.Background(), "owl song")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.asked) != 1 || p.asked[0] != "query owl song" || vec[1] != 1 {
		t.Fatalf("asked %q, vector %v: want the query embedded as a query", p.asked, vec)
	}
}
