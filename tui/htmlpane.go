package tui

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// THE NATIVE HTML PREVIEW — File › Preview HTML under --gui (ADR 1791429896): the note exported
// in-process and shown in golib's HTMLView, in a pane beside the editor, drawn natively, selectable
// and live. In a terminal Preview HTML is what it was (the page as an image, or the browser): one
// menu item, the widget chosen by the backend that runs.
//
// The pane follows the editor one way: an edit exports the note again after a moment of quiet,
// and the cursor's block comes to the pane's top. A link clicked in it opens a note, a file or
// the browser.

// htmlPaneState is the pane's: whether it shows the open note, and the latest render.
type htmlPaneState struct {
	gen    uint64 // numbers the renders; a later one supersedes an earlier
	dir    string // the folder the page's images load from
	dirSet bool   // dir has been given to the view
}

// native reports whether the backend draws native views: a GUI window (--gui).
func (h *Host) native() bool {
	if h.nativeViews != nil {
		return h.nativeViews()
	}
	return h.p != nil && h.p.App() != nil && h.p.App().Capabilities().NativeViews
}

// htmlView is the pane's HTMLView.
func (h *Host) htmlView() (*widget.HTMLView, bool) {
	return tuidecl.FindAs[*widget.HTMLView](h.p, "htmlView")
}

// openPaneInBrowser opens the note's page, as the pane shows it, in the browser: the pane's Open in
// browser, as the terminal preview's button is. The page is written to the preview's file, its
// relative links and embeds read from the note's folder.
func (h *Host) openPaneInBrowser() {
	if !h.previewable() {
		h.notify("HTML preview currently accepts Markdown files")
		return
	}
	source, theme := []byte(h.pageNow().Text), h.exportTheme()
	var options export.Options
	if full := h.diskPath(); full != "" {
		options.Base, options.Document = filepath.Dir(full), full
	}
	h.say("opening the page in the browser…")
	do(h, func(ctx context.Context) answerOf[string] {
		content, err := export.RenderWith(source, export.HTML, theme, options)
		if err != nil {
			return answerOf[string]{err: err}
		}
		path, err := htmlPreviewFile(content)
		if err != nil {
			return answerOf[string]{err: err}
		}
		return answerOf[string]{v: path, err: h.browser(ctx, path)}
	}, func(a answerOf[string]) {
		if a.err != nil {
			h.failed("the browser did not open", a.err)
			return
		}
		h.htmlPreviewPath = a.v
		h.say("HTML preview opened in the browser: " + filepath.Base(a.v))
	})
}

// openHTMLPane shows the open note in the pane beside the editor.
func (h *Host) openHTMLPane() {
	if !h.previewable() {
		h.notify("HTML preview currently accepts Markdown files")
		return
	}
	h.keep(h.p.Call("htmlPane", "open"))
	h.renderHTMLPane()
}

// previewable reports whether the editor holds Markdown: a .md file, or the draft.
func (h *Host) previewable() bool { return !h.file.open || filepath.Ext(h.file.path) == ".md" }

// htmlPaneNoteChanged is another note in the editor, opened or closed for the draft, which no
// typing reports: an open pane shows the new note at once (its render supersedes the old note's
// still on its way), or closes when it is not Markdown.
func (h *Host) htmlPaneNoteChanged() {
	if !h.htmlPaneShown() {
		return
	}
	if !h.previewable() {
		h.keep(h.p.Call("htmlPane", "close"))
		return
	}
	h.renderHTMLPane()
}

// htmlPaneShown reports whether the pane is open.
func (h *Host) htmlPaneShown() bool { return h.panelOpen["htmlPane"] }

// renderHTMLPane exports the editor's text off the loop and shows it in the pane, its images read
// from the note's folder and its blocks carrying their source bytes.
func (h *Host) renderHTMLPane() {
	tk := h.ticket(&h.htmlPane.gen, TicketLocal)
	source, theme := h.pageNow().Text, h.exportTheme()
	options := export.Options{SourceSpans: true}
	dir, root, base := "", "", ""
	if full := h.diskPath(); full != "" {
		dir = filepath.Dir(full)
		options.Base, options.Document = dir, full
		root, base = h.resourcesFor(h.file.ws, h.file.path)
	}
	do(h, func(context.Context) answerOf[[]byte] {
		page, err := export.RenderWith(source, export.HTML, theme, options)
		return answerOf[[]byte]{v: page, err: err}
	}, func(a answerOf[[]byte]) {
		if !tk.Current() {
			return // an edit since: its render follows
		}
		if a.err != nil {
			h.failed("HTML preview", a.err)
			return
		}
		v, ok := h.htmlView()
		if !ok {
			return
		}
		// a new resolver drops the page's images, so it is set before the page, and only for another
		// folder: at each render it would decode every image again
		if key := root + "\x00" + base; !h.htmlPane.dirSet || key != h.htmlPane.dir {
			h.htmlPane.dir, h.htmlPane.dirSet = key, true
			v.SetOnRefused(h.resourceRefused)
			if dir != "" {
				v.SetImageResolver(widget.DirResources(root, base))
			} else {
				v.SetImageResolver(nil)
			}
		}
		v.SetHTML(a.v)
		h.followCursorInPane()
	})
}

// followCursorInPane brings the block holding the cursor to the pane's top.
func (h *Host) followCursorInPane() {
	if !h.htmlPaneShown() {
		return
	}
	if v, ok := h.htmlView(); ok {
		v.ScrollToSource(h.cursorBytes())
	}
}

// htmlLink is a link clicked in the pane: a web or mail address goes to the system browser; a
// file: URL, or a path relative to the note (a wikilink's page, a Markdown link), opens in the
// editor, a workspace's file as that workspace's.
func (h *Host) htmlLink(href string) {
	u, err := url.Parse(strings.TrimSpace(href))
	if err != nil || href == "" {
		return
	}
	switch strings.ToLower(u.Scheme) {
	case "file":
		h.openAbsolute(filepath.FromSlash(u.Path))
		return
	case "":
	default:
		do(h, func(ctx context.Context) error { return h.browser(ctx, href) }, func(err error) {
			if err != nil {
				h.failed("open "+href, err)
			}
		})
		return
	}
	if u.Path == "" {
		return // a fragment of this note
	}
	full := h.diskPath()
	if full == "" {
		h.notify("the note is not on disk yet: save it, then its links open")
		return
	}
	p := filepath.Join(filepath.Dir(full), filepath.FromSlash(u.Path))
	if filepath.IsAbs(filepath.FromSlash(u.Path)) {
		p = filepath.FromSlash(u.Path)
	}
	if filepath.Ext(p) == "" {
		p += ".md" // a wikilink names its page without the extension
	}
	h.openAbsolute(p)
}
