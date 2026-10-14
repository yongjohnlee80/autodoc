package tui

import (
	"testing"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// notedoc_test.go: the GUI editor's document view for a note (notedoc.go), without a window.

// TestANoteDocumentViewShowsWhatTheHostRenders: a text the host renders is not the page; one it
// does not (a raw HTML file's), or any text before the host is linked, is the page itself.
func TestANoteDocumentViewShowsWhatTheHostRenders(t *testing.T) {
	d := NewNoteDocumentView(widget.NewHTMLView())
	d.ShowDocument([]byte("<p>unlinked</p>"))
	if got := string(d.Source()); got != "<p>unlinked</p>" {
		t.Errorf("before the host is linked the page is %q; want the text itself", got)
	}
	var asked []string
	note := true
	if !linkNoteDocument(d, func(text []byte) bool { asked = append(asked, string(text)); return note }) {
		t.Fatal("a NoteDocumentView was not linked")
	}
	d.ShowDocument([]byte("# a note"))
	if got := string(d.Source()); got != "<p>unlinked</p>" {
		t.Errorf("a note's text became the page %q; the host renders it", got)
	}
	note = false
	d.ShowDocument([]byte("<p>raw</p>"))
	if got := string(d.Source()); got != "<p>raw</p>" {
		t.Errorf("a text the host does not render: the page is %q", got)
	}
	if len(asked) != 2 || asked[0] != "# a note" || asked[1] != "<p>raw</p>" {
		t.Errorf("the host was asked %q", asked)
	}
	if linkNoteDocument(widget.NewHTMLView(), func([]byte) bool { return true }) {
		t.Error("golib's own HTMLView was linked as a NoteDocumentView")
	}
}

// TestThePageOfADocumentView: golib's HTMLView is its own page; a NoteDocumentView's is the one it
// wraps; anything else has none.
func TestThePageOfADocumentView(t *testing.T) {
	v := widget.NewHTMLView()
	if got, ok := pageOf(v); !ok || got != v {
		t.Error("golib's HTMLView is not its own page")
	}
	if got, ok := pageOf(NewNoteDocumentView(v)); !ok || got != v {
		t.Error("a NoteDocumentView's page is not the HTMLView it wraps")
	}
	if _, ok := pageOf(nil); ok {
		t.Error("no view has a page")
	}
}
