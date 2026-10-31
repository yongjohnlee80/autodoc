package tui

import (
	"io/fs"
	"path"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/docs"
)

// THE TUTORIAL — Help › Tutorial…: the short tutorials of docs/tutorials/ (docs.Tutorials, the
// files the docs site publishes), read in order, in the UI's language
// (docs/decisions/editor.md#six-languages): a page a language lacks is English's. The dialog shows
// one at a time, Rendered under
// --gui, its mermaid diagrams drawn, and as text in a terminal, with Previous and Next
// (views/Tutorial.qml). Neither is ever disabled, so neither disables itself from its own click:
// Previous on the first page does nothing, and Next on the last is Close.

// tutorialBooks are the pages' texts by language (docs.Tutorials, tutorialBooksOf).
var tutorialBooks = tutorialBooksOf(docs.Tutorials)

// tutorialBooksOf is fsys's tutorial by language, in its files' order: English's (tutorials/), and
// each other language's English with its own page (tutorials/<tag>/, the same file name) in place
// of each it has.
func tutorialBooksOf(fsys fs.FS) map[string][]string {
	names, _ := fs.Glob(fsys, "tutorials/*.md")
	books := make(map[string][]string, len(languages))
	for _, tag := range languages {
		pages := make([]string, 0, len(names))
		for _, n := range names {
			b, err := fs.ReadFile(fsys, path.Join("tutorials", tag, path.Base(n)))
			if err != nil {
				b, _ = fs.ReadFile(fsys, n)
			}
			pages = append(pages, string(b))
		}
		books[tag] = pages
	}
	return books
}

// tutorialPages are English's pages.
var tutorialPages = tutorialBooks["en"]

// tutorial is the pages in the UI's language.
func (h *Host) tutorial() []string {
	if pages, ok := tutorialBooks[h.language]; ok {
		return pages
	}
	return tutorialPages
}

// openTutorial opens the tutorial at its first page.
func (h *Host) openTutorial() {
	h.tutorialAt = 0
	h.showTutorialPage()
	h.open("tutorial")
}

// tutorialPage is Previous ("back") and Next ("next"), which on the last page closes the tutorial.
func (h *Host) tutorialPage(which string) {
	switch which {
	case "back":
		h.tutorialStep(-1)
	case "next":
		if h.tutorialAt == len(h.tutorial())-1 {
			h.closeDialog("tutorial")
			return
		}
		h.tutorialStep(1)
	}
}

// tutorialStep is a page back (-1) or on (+1), stopping at the ends.
func (h *Host) tutorialStep(dir int) {
	if n := h.tutorialAt + dir; n >= 0 && n < len(h.tutorial()) {
		h.tutorialAt = n
		h.showTutorialPage()
	}
}

// showTutorialPage sets the dialog's page, its title (the page's heading, numbered) and Next's label:
// Close on the last page, with Next's key, so n through the pages ends by closing them.
func (h *Host) showTutorialPage() {
	pages := h.tutorial()
	page := pages[h.tutorialAt]
	heading, body, _ := strings.Cut(page, "\n")
	title := strings.TrimSpace(strings.TrimPrefix(heading, "# "))
	h.set("App.tutorialTitle", h.trf("autodoc.text.tutorial.of", strconv.Itoa(h.tutorialAt+1), strconv.Itoa(len(pages)), title))
	h.set("App.tutorialText", strings.TrimLeft(body, "\n"))
	next := h.tr("autodoc.text.tutorial.next")
	if h.tutorialAt == len(pages)-1 {
		next = h.tr("autodoc.text.tutorial.close") // with Next's key: n through the pages ends by closing them
	}
	h.set("App.tutorialNextText", next)
}
