package index

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/markdown"
)

func TestOversizedTableKeepsHeadersAndBoundedSourceSpans(t *testing.T) {
	var src strings.Builder
	src.WriteString("# Records\n\n| Link | Identifier |\n|---|---|\n")
	for i := range 98 {
		fmt.Fprintf(&src, "| https://example.org/%04d/%s | %s |\n", i, strings.Repeat("abcdef1234", 10), strings.Repeat("id", 50))
	}
	text := src.String()
	doc := markdown.Parse([]byte(text), markdown.GFM())
	chunks := chunkDoc(doc, "Records")
	if len(chunks) < 3 {
		t.Fatalf("oversized table produced %d chunks", len(chunks))
	}
	for _, c := range chunks {
		if n := tokensOf([]byte(c.breadcrumb+"\n"+c.body), 0, len(c.breadcrumb)+1+len(c.body)); n > maxTokens {
			t.Errorf("chunk %d has %d estimated tokens (> %d)", c.ord, n, maxTokens)
		}
		if c.byteStart < 0 || c.byteEnd > len(text) || c.byteStart >= c.byteEnd {
			t.Errorf("chunk %d has invalid source span %d:%d", c.ord, c.byteStart, c.byteEnd)
		}
		if strings.Contains(c.body, "example.org") && (!strings.Contains(c.body, "| Link | Identifier |") || !strings.Contains(c.body, "|---|---|")) {
			t.Errorf("chunk %d lost table column names", c.ord)
		}
	}
}

func TestTokenEstimateCountsUnbrokenURLs(t *testing.T) {
	s := "https://example.org/" + strings.Repeat("identifier0123456789", 250)
	if n := tokensOf([]byte(s), 0, len(s)); n < 1000 {
		t.Fatalf("long URL estimated at %d tokens", n)
	}
}

func TestLongCodeAndListRespectSectionBudget(t *testing.T) {
	for _, tc := range []struct{ name, body string }{
		{"code", "```go\n" + strings.Repeat("fmt.Println(\"one two three four five six seven eight\")\n", 100) + "```\n"},
		{"list", "- root\n" + strings.Repeat("  - nested long item with the same many words and a URL https://example.org/abc123456789\n", 95)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "# Heading\n\n" + tc.body
			cs := chunkDocWithLimit(markdown.Parse([]byte(src), markdown.GFM()), "Heading", 128)
			if len(cs) < 2 {
				t.Fatalf("oversize %s did not split", tc.name)
			}
			for _, c := range cs {
				text := []byte(c.breadcrumb + "\n" + c.body)
				if got := tokensOf(text, 0, len(text)); got > 128 {
					t.Errorf("chunk %d estimated %d (>128)", c.ord, got)
				}
				if tc.name == "code" && strings.Contains(c.body, "fmt.Println") &&
					(!strings.HasPrefix(c.body, "```go") || !strings.HasSuffix(strings.TrimSpace(c.body), "```")) {
					t.Errorf("chunk %d lost fenced Go syntax: %q", c.ord, c.body[:min(60, len(c.body))])
				}
			}
		})
	}
}

func TestLongTableHeaderCannotMakeAnOversizedChunk(t *testing.T) {
	text := "# Heading\n\n| " + strings.Repeat("headerid", 180) + " | Value |\n|---|---|\n" + strings.Repeat("| long row | value |\n", 40)
	for _, c := range chunkDocWithLimit(markdown.Parse([]byte(text), markdown.GFM()), "Heading", 128) {
		s := []byte(c.breadcrumb + "\n" + c.body)
		if n := tokensOf(s, 0, len(s)); n > 128 {
			t.Errorf("chunk %d has %d tokens", c.ord, n)
		}
	}
}

func TestTitleOnlyAndHeaderOnlyTableStayBounded(t *testing.T) {
	title := strings.Repeat("long title with many words ", 80)
	cs := chunkDocWithLimit(markdown.Parse(nil, markdown.GFM()), title, 128)
	if len(cs) != 1 || cs[0].body != "" || tokensOf([]byte(cs[0].breadcrumb), 0, len(cs[0].breadcrumb)) > 128 {
		t.Fatalf("empty note with long title: %+v", cs)
	}
	table := "# Title\n\n| First | Second |\n|---|---|\n"
	cs = chunkDocWithLimit(markdown.Parse([]byte(table), markdown.GFM()), "Title", 128)
	if len(cs) != 1 || !strings.Contains(cs[0].body, "| First | Second |") {
		t.Fatalf("header-only table: %+v", cs)
	}
}
