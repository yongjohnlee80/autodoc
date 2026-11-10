package explorer

import (
	"fmt"
	"testing"
)

// The list keeps the newest maxRecent, each once; a preference that is not a list reads as none.
func TestRecentFilesKeepTheNewestOnce(t *testing.T) {
	var docs []RecentDoc
	for i := range maxRecent + 5 {
		docs = withRecent(docs, RecentDoc{"kb", fmt.Sprintf("%d.md", i)})
	}
	if len(docs) != maxRecent || docs[0].Path != fmt.Sprintf("%d.md", maxRecent+4) || docs[maxRecent-1].Path != "5.md" {
		t.Fatalf("kept %d, first %v, last %v", len(docs), docs[0], docs[len(docs)-1])
	}
	docs = withRecent(docs, RecentDoc{"kb", "10.md"})
	if len(docs) != maxRecent || docs[0].Path != "10.md" || docs[1].Path != fmt.Sprintf("%d.md", maxRecent+4) {
		t.Fatalf("again: kept %d, first %v, %v", len(docs), docs[0], docs[1])
	}
	for _, d := range docs[1:] {
		if d.Path == "10.md" {
			t.Fatal("10.md listed twice")
		}
	}
	for _, bad := range []string{"nope", `{"workspace":"kb"}`, `[{"workspace":"kb","path":"a.md"},5]`} {
		if got := recentOf(bad); got != nil {
			t.Fatalf("read %v from %s", got, bad)
		}
	}
	if RecentJSON(nil) != "[]" {
		t.Fatal("no recent files written as other than []")
	}
}
