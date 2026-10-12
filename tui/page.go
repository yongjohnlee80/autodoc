package tui

import (
	"time"

	"github.com/yongjohnlee80/autodoc/core/kind"
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

// FileList is the current workspace's files, as last listed: replaced whole on each listing, so a
// reader may keep it, and never changes it.
type FileList struct {
	Paths  []string // nil until the first listing of the workspace in use answers
	Loaded bool     // a listing has answered since the workspace was entered
	Gen    uint64   // moves on each listing, and on entering a workspace
}

// Page is what the editor shows: a file, or the draft (Ref zero). Text is one snapshot, handed to
// every watcher as it is: a reader never changes it.
type Page struct {
	Ref     fileRef
	Kind    kind.Kind // how its text is read: derived, raw and the daemon's registrations applied
	Text    []byte
	Outside bool // a file in no workspace
}

// More optional parts of a Feature (feature.go).
type (
	// Lister hears that the workspace's files or the daemon's workspaces were listed again; it
	// reads Core.Files and Core.Workspaces for them.
	Lister interface{ Listed() }
	// DocWatcher hears the page's lifecycle, on the loop, in this order: Closed (when a page was
	// shown), then Shown; Edited, Moved and Reread only between a Shown and the next Closed.
	DocWatcher interface {
		Shown(Page)         // a file read and shown, or the draft started
		Edited(Page)        // the text after a typing pause
		Moved(line, at int) // the cursor moved, edit or not: its line (0-based) and byte offset
		Reread(Page)        // the same text read as another kind (the build's registrations came)
		Closed()            // the page shown is going: another is about to be shown
	}
)

// listed tells the features that hear listings.
func (r *registry) listed() {
	if r == nil {
		return
	}
	for _, f := range r.features {
		if l, ok := f.(Lister); ok {
			l.Listed()
		}
	}
}

// watchers are the features that hear the page.
func (r *registry) watchers() []DocWatcher {
	if r == nil {
		return nil
	}
	var out []DocWatcher
	for _, f := range r.features {
		if w, ok := f.(DocWatcher); ok {
			out = append(out, w)
		}
	}
	return out
}

// shown tells the watchers a page is shown, the one before it closed first.
func (r *registry) shown(p Page) {
	if r == nil {
		return
	}
	if r.pageShown {
		for _, w := range r.watchers() {
			w.Closed()
		}
	}
	r.pageShown = true
	for _, w := range r.watchers() {
		w.Shown(p)
	}
}

// edited, moved and reread tell the watchers, while a page is shown.
func (r *registry) edited(p Page) {
	if r == nil || !r.pageShown {
		return
	}
	for _, w := range r.watchers() {
		w.Edited(p)
	}
}

func (r *registry) moved(line, at int) {
	if r == nil || !r.pageShown {
		return
	}
	for _, w := range r.watchers() {
		w.Moved(line, at)
	}
}

func (r *registry) reread(p Page) {
	if r == nil || !r.pageShown {
		return
	}
	for _, w := range r.watchers() {
		w.Reread(p)
	}
}

// setFiles is the workspace's files listed: the snapshot replaced whole, and the listers told.
func (h *Host) setFiles(paths []string) {
	h.files = FileList{Paths: paths, Loaded: true, Gen: h.files.Gen + 1}
	h.reg.listed()
}

// resetFiles is a workspace entered, or none in use: no files until a listing answers.
func (h *Host) resetFiles() {
	h.files = FileList{Gen: h.files.Gen + 1}
	h.reg.listed()
}

// showPage is a page shown, a file read or the draft started: its text snapshotted, and the
// watchers told (the one before closed first). The host's consumers read h.page from here on.
func (h *Host) showPage() {
	h.page, h.typing = h.takePage(), false
	h.reg.shown(h.page)
}

// pageNow is the page as it is now, for a reader that cannot wait for the pause (a picker opened
// while typing): snapshotted again only when typed since the last snapshot. The pause still comes.
func (h *Host) pageNow() Page {
	if h.typing {
		h.page = h.takePage()
	}
	return h.page
}

// takePage is the page as the editor shows it now, its text one snapshot.
func (h *Host) takePage() Page {
	p := Page{Kind: h.outlineKind(), Text: []byte(h.core.Value())}
	if h.file.open {
		p.Ref, p.Outside = fileRef{h.file.ws, h.file.path}, h.file.outside()
	}
	return p
}

// pageSoon is typing: once it has rested editPause, the page's text is snapshotted once and handed
// on (pagePaused). A later keystroke supersedes it.
func (h *Host) pageSoon() {
	h.typing = true
	tk := h.ticket(&h.pageGen, TicketLocal)
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
	h.refreshOutline()
	if h.htmlPaneShown() {
		h.renderHTMLPane()
	}
	h.validateSoon(validateDelay - editPause)
	h.feedSoonAfter(max(pluginFeedDelay-editPause, 0))
	h.reg.edited(h.page)
}
