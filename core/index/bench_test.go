package index

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/parse/markdown"
	"github.com/yongjohnlee80/golib/search/chunk"
)

// The benchmarks measure chunking and search before and after the search code moves, on the same
// machine: benchstat over -count=10 of each must stay within 5%.

// BenchmarkChunk is the indexer's per-file preparation for each kind: parse, metadata and chunks
// for Markdown, chunks for plain text and YAML.
func BenchmarkChunk(b *testing.B) {
	c := goldenCorpus()
	md := []byte(c["guides/architecture.md"] + c["big/table.md"] + c["big/code.md"] + c["big/list.md"] + c["big/paragraphs.md"])
	txt := []byte(strings.Repeat(c["notes/plain.txt"], 4))
	yml := []byte(strings.Repeat(c["config/settings.yaml"], 1) + benchYAML(200))
	b.Run("Markdown", func(b *testing.B) {
		b.SetBytes(int64(len(md)))
		for b.Loop() {
			doc := markdown.Parse(md, markdown.GFM(), markdown.Obsidian())
			meta := chunk.ReadMeta(doc, "bench.md")
			hashed(chunk.Markdown(doc, meta.Title, 512))
		}
	})
	b.Run("Text", func(b *testing.B) {
		b.SetBytes(int64(len(txt)))
		for b.Loop() {
			hashed(chunk.Text(txt, "bench", 512))
		}
	})
	b.Run("YAML", func(b *testing.B) {
		b.SetBytes(int64(len(yml)))
		for b.Loop() {
			_, cs := chunk.YAML(yml, "bench.yaml", 512)
			hashed(cs)
		}
	})
}

func benchYAML(entries int) string {
	var s strings.Builder
	for i := range entries {
		fmt.Fprintf(&s, "service%d:\n  host: host%d.internal\n  port: %d\n  tags: [a, b, c]\n", i, i, 8000+i)
	}
	return s.String()
}

// benchVocabulary gives the generated notes overlapping words, so queries match many notes in
// both retrievers.
var benchVocabulary = strings.Fields(`storage search index chunk vector model query writer reader
	snapshot commit table column schema facet tag path link graph daemon client socket session
	editor outline export diagram theme plugin provider embedding fusion rank score window`)

// benchEnv indexes n generated notes with the fake provider, every one semantic-ready.
func benchEnv(b *testing.B, n int) *env {
	b.Helper()
	e := newEnv(b, Options{Provider: newFake("m", "a"), BatchDelay: time.Millisecond, BatchSize: 200})
	var pc []string
	for i := range n {
		var body strings.Builder
		fmt.Fprintf(&body, "# Note %d\n\n", i)
		for s := range 4 {
			fmt.Fprintf(&body, "## Section %d\n\n", s)
			for w := range 40 {
				body.WriteString(benchVocabulary[(i*7+s*13+w*w)%len(benchVocabulary)])
				body.WriteByte(' ')
			}
			body.WriteString("\n\n")
		}
		pc = append(pc, fmt.Sprintf("notes/%04d.md", i), body.String())
	}
	for i := 0; i < len(pc); i += 200 { // 100 notes at a time: put waits for the whole queue
		e.put(pc[i:min(i+200, len(pc))]...)
	}
	e.ready()
	e.atHead()
	return e
}

// BenchmarkSearch is search.query in each mode over 2000 notes (8000 sections), from the snapshot.
func BenchmarkSearch(b *testing.B) {
	e := benchEnv(b, 2000)
	ctx := context.Background()
	for _, m := range []struct{ name, mode string }{{"Lexical", ModeLexical}, {"Semantic", ModeSemantic}, {"Hybrid", ModeAuto}} {
		b.Run(m.name, func(b *testing.B) {
			for b.Loop() {
				if _, err := e.ix.Search(ctx, "storage vector snapshot", QueryOpts{Mode: m.mode}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
