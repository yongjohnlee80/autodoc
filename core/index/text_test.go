package index

import (
	"context"
	"strings"
	"testing"
)

func TestPlainTextIndexesLiterally(t *testing.T) {
	env := newEnv(t, Options{Match: func(path string) bool { return strings.HasSuffix(path, ".txt") }})
	env.put("notes.txt", "# Puffin\n\n[[missing]] and #bird are plain text.\n")
	hits := env.search("puffin", QueryOpts{}).Hits
	if len(hits) != 1 || hits[0].Breadcrumb != "notes" {
		t.Fatalf("plain-text hit = %+v, want one hit under filename title", hits)
	}
	if links := env.targets("notes.txt"); len(links) != 0 {
		t.Fatalf("plain text created links: %v", links)
	}
	if tagged := env.search("puffin", QueryOpts{Tags: []string{"bird"}}).Hits; len(tagged) != 0 {
		t.Fatalf("plain text created Markdown tags: %+v", tagged)
	}
}

func TestPlainTextChunkSpansAndBound(t *testing.T) {
	src := []byte(strings.Repeat("long sentence with words\n", 100) + "\nsecond paragraph")
	chunks := chunkPlainText(src, "notes", 64)
	if len(chunks) < 2 {
		t.Fatalf("got %d chunks, want split text", len(chunks))
	}
	for _, chunk := range chunks {
		if chunk.byteStart < 0 || chunk.byteEnd > len(src) || chunk.byteStart >= chunk.byteEnd {
			t.Fatalf("invalid source span: %+v", chunk)
		}
		if tokensOf([]byte(chunk.breadcrumb+"\n"+chunk.body), 0, len(chunk.breadcrumb)+1+len(chunk.body)) > 64 {
			t.Fatalf("oversized plain-text chunk: %+v", chunk)
		}
	}
}

func TestCommunityIndexerDoesNotReadProDocumentsThroughBroadGlobs(t *testing.T) {
	env := newEnv(t, Options{Match: func(string) bool { return true }})
	for _, extension := range []string{".doc", ".docx", ".odt", ".pdf"} {
		path := "private" + extension
		env.write(path, "binary-looking contents")
		prepared := env.ix.prepare(context.Background(), workItem{path: path})
		if !prepared.delete || prepared.err != nil {
			t.Errorf("Pro-only format %q was not rejected: %+v", path, prepared)
		}
		if got := env.fault.openCount(path); got != 0 {
			t.Errorf("Pro-only format %q was read %d times", path, got)
		}
	}
}
