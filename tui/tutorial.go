package tui

import (
	"embed"
	"io/fs"
	"strconv"
	"strings"
)

// THE TUTORIAL — Help › Tutorial… (SPC T): a few pages, each Markdown, embedded in the binary and
// read in order (tutorial/NN-name.md). The dialog shows one at a time, Rendered under --gui and as
// text in a terminal, with Back and Next (views/Tutorial.qml).

//go:embed tutorial/*.md
var tutorialFS embed.FS

// tutorialPages are the pages' texts, in their files' order.
var tutorialPages = func() []string {
	names, _ := fs.Glob(tutorialFS, "tutorial/*.md")
	pages := make([]string, 0, len(names))
	for _, n := range names {
		b, _ := tutorialFS.ReadFile(n)
		pages = append(pages, string(b))
	}
	return pages
}()

// openTutorial opens the tutorial at its first page.
func (h *Host) openTutorial() {
	h.tutorialAt = 0
	h.showTutorialPage()
	h.open("tutorial")
}

// tutorialPage is Back ("back") and Next ("next").
func (h *Host) tutorialPage(which string) {
	switch which {
	case "back":
		h.tutorialStep(-1)
	case "next":
		h.tutorialStep(1)
	}
}

// tutorialStep is a page back (-1) or on (+1), stopping at the ends.
func (h *Host) tutorialStep(dir int) {
	if n := h.tutorialAt + dir; n >= 0 && n < len(tutorialPages) {
		h.tutorialAt = n
		h.showTutorialPage()
	}
}

// showTutorialPage sets the dialog's page, its title (the page's heading, numbered) and its buttons.
func (h *Host) showTutorialPage() {
	page := tutorialPages[h.tutorialAt]
	heading, body, _ := strings.Cut(page, "\n")
	title := strings.TrimSpace(strings.TrimPrefix(heading, "# "))
	h.set("App.tutorialTitle", strconv.Itoa(h.tutorialAt+1)+" of "+strconv.Itoa(len(tutorialPages))+" · "+title)
	h.set("App.tutorialText", strings.TrimLeft(body, "\n"))
	h.set("App.tutorialBack", h.tutorialAt > 0)
	h.set("App.tutorialNext", h.tutorialAt < len(tutorialPages)-1)
}
