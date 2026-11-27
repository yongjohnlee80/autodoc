package explorer

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// indexList answers index.list with paths.
func indexList(answers map[string]func([]any) (any, error), paths ...string) {
	docs := make([]any, len(paths))
	for i, p := range paths {
		docs[i] = map[string]any{"path": p}
	}
	answers["index.list"] = func([]any) (any, error) { return map[string]any{"docs": docs}, nil }
}

// A listing the host makes of the workspace in use while its row's own listing is in flight is
// kept: the row's listing, read before c.md was made and answering after the host's, does not
// lose it. The mutant, named here: fetch putting its own answer whatever the host listed meanwhile.
func TestAHostListingDuringARowsListingIsKept(t *testing.T) {
	c := newScripted("kb")
	indexList(c.Answers, "a.md")
	e := NewExplorer(c)
	c.Spaces = []feature.Workspace{{Name: "kb", State: "ready"}}
	e.Listed()
	c.Deferred = true
	kb := tuidecl.Index{Row: 0}
	e.fetch(kb) // read with a.md alone
	c.Listing = feature.FileList{Paths: []string{"a.md", "c.md"}, Loaded: true, Gen: 1}
	e.Listed() // the host's listing after c.md was made: the row not listed yet
	c.Deliver(0)
	if n := e.Model.RowCount(&kb); n != 2 {
		t.Fatalf("kb has %d rows once its own listing answered after the host's; want 2, c.md kept", n)
	}
	// and the host's next listing is still taken
	c.Listing = feature.FileList{Paths: []string{"a.md", "c.md", "d.md"}, Loaded: true, Gen: 2}
	e.Listed()
	if n := e.Model.RowCount(&kb); n != 3 {
		t.Fatalf("kb has %d rows after the host listed d.md; want 3", n)
	}
}

// A row's listing asked before the top level was rebuilt, answering after the one asked after it,
// is dropped. The mutant, named here: fetch with no sequence (the earlier list lands last).
func TestAnEarlierListingOfARowIsDropped(t *testing.T) {
	c := newScripted("kb")
	indexList(c.Answers, "a.md")
	e := NewExplorer(c)
	c.Spaces = []feature.Workspace{{Name: "kb", State: "ready"}, {Name: "notes", State: "ready"}}
	e.Listed()
	c.Deferred = true
	kb := tuidecl.Index{Row: 0}
	e.fetch(kb) // read with a.md alone
	c.Spaces = []feature.Workspace{{Name: "kb", State: "ready"}, {Name: "notes", State: "indexing"}}
	e.Listed() // the top level rebuilt: kb's row is listed afresh when opened
	indexList(c.Answers, "a.md", "c.md")
	e.fetch(kb)
	c.Deliver(1, 0) // the later listing first
	if n := e.Model.RowCount(&kb); n != 2 {
		t.Fatalf("kb has %d rows once the earlier listing answered last; want 2", n)
	}
}
