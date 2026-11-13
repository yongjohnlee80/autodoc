package tui

import (
	"time"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// THE PAGE AND THE LISTS A FEATURE READS — what the host shares with every feature, once.
//
// The page is the file shown in the editor, or the draft. A feature hears its lifecycle as a
// DocWatcher: one immutable snapshot of the text per typing pause, shared by every watcher and by
// the host's own consumers (the frontmatter check, the outline, the HTML pane, the plugin feed),
// where each of them used to copy the editor's text on a timer of its own.
//
// The workspace's files and the daemon's workspaces are each one snapshot, replaced whole on each
// listing. A feature reads them through Core and hears them change as a Lister.

// editPause is how long typing rests before the page's text is snapshotted and handed on. A
// consumer that wants a longer quiet period waits the rest on top, timed from the same last edit.
const editPause = 150 * time.Millisecond

// setFiles is the workspace's files listed: the snapshot replaced whole, and the listers told.
func (h *Host) setFiles(paths []string) {
	h.files = feature.FileList{Paths: paths, Loaded: true, Gen: h.files.Gen + 1}
	h.reg.Listed()
}

// resetFiles is a workspace entered, or none in use: no files until a listing answers.
func (h *Host) resetFiles() {
	h.files = feature.FileList{Gen: h.files.Gen + 1}
	h.reg.Listed()
}

// showPage is a page shown, a file read or the draft started, and the one way a page is shown: its
// version rises, its text is snapshotted with it, and the watchers are told (the one before closed
// first). Every change of the page shown goes through here, so none can show a page with the last
// one's version. The host's consumers read h.page from here on.
func (h *Host) showPage() {
	h.pageGen++ // a pause still to come was the page before's: it must not reach this one
	h.pageVersion++
	h.page, h.typing = h.takePage(), false
	h.reg.Shown(h.page)
	h.showTitle() // the file's name, whether or not a feature puts a breadcrumb after it
}

// pageNow is the page as it is now, for a reader that cannot wait for the pause (a picker opened
// while typing): snapshotted again only when typed since the last snapshot. The pause still comes.
func (h *Host) pageNow() feature.Page {
	if h.typing {
		h.page = h.takePage()
	}
	return h.page
}

// takePage is the page as the editor shows it now, its text one snapshot.
func (h *Host) takePage() feature.Page {
	line, _ := h.core.Line()
	p := feature.Page{Kind: h.pageKind(), Text: []byte(h.core.Value()), Line: line, At: h.cursorBytes(), Version: h.pageVersion}
	if h.file.open {
		p.Ref, p.Outside = feature.FileRef{WS: h.file.ws, Path: h.file.path}, h.file.outside()
	}
	return p
}

// pageKind is how the page's text is read: a derived document's text as Markdown, a file opened
// raw (an HTML file's own bytes) as plain text, the rest by the daemon's registrations, a draft by
// its hint (Markdown with none).
func (h *Host) pageKind() kind.Kind {
	switch {
	case !h.file.open:
		return h.resolver().Of(h.file.hint) // "" is Markdown
	case h.file.derived != "":
		return kind.Markdown
	case h.file.raw:
		return kind.Text
	}
	return h.resolver().Of(h.file.path)
}

// pageSoon is typing: once it has rested editPause, the page's text is snapshotted once and handed
// on (pagePaused). A later keystroke supersedes it.
func (h *Host) pageSoon() {
	h.typing = true
	tk := h.ticket(&h.pageGen, feature.TicketLocal)
	h.after(editPause, func() {
		if tk.Current() {
			h.pagePaused()
		}
	})
}

// pagePaused hands one snapshot of the text to every consumer: the outline and the HTML pane at
// once, the frontmatter check and the plugin feed after the rest of their own quiet period (timed
// from the same last keystroke, as before), and the watchers.
func (h *Host) pagePaused() {
	h.page, h.typing = h.takePage(), false
	if h.htmlPaneShown() {
		h.renderHTMLPane()
	}
	h.validateSoon(validateDelay - editPause)
	h.reg.Edited(h.page)
}
