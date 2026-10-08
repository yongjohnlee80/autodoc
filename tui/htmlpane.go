package tui

import (
	"context"
	"net/url"
	"path/filepath"
	"strings"
	"time"

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

// htmlPaneDelay is the quiet after an edit before the pane shows it.
const htmlPaneDelay = 150 * time.Millisecond

// htmlPaneState is the pane's: whether it shows the open note, and the latest render.
type htmlPaneState struct {
	gen uint64 // numbers the renders; a later one supersedes an earlier
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

// openHTMLPane shows the open note in the pane beside the editor.
func (h *Host) openHTMLPane() {
	if h.file.open && filepath.Ext(h.file.path) != ".md" {
		h.notify("HTML preview currently accepts Markdown files")
		return
	}
	h.keep(h.p.Call("htmlPane", "open"))
	h.renderHTMLPane()
}

// htmlPaneShown reports whether the pane is open.
func (h *Host) htmlPaneShown() bool { return h.panelOpen["htmlPane"] }

// renderHTMLPane exports the editor's text off the loop and shows it in the pane, its images read
// from the note's folder and its blocks carrying their source bytes.
func (h *Host) renderHTMLPane() {
	h.htmlPane.gen++
	gen := h.htmlPane.gen
	source, theme := []byte(h.core.Value()), h.exportTheme()
	options := export.Options{SourceSpans: true}
	dir := ""
	if full := h.diskPath(); full != "" {
		dir = filepath.Dir(full)
		options.Base = dir
	}
	do(h, func(context.Context) answerOf[[]byte] {
		page, err := export.RenderWith(source, export.HTML, theme, options)
		return answerOf[[]byte]{v: page, err: err}
	}, func(a answerOf[[]byte]) {
		if gen != h.htmlPane.gen {
			return // an edit since: its render follows
		}
		if a.err != nil {
			h.notify("HTML preview: " + a.err.Error())
			return
		}
		v, ok := h.htmlView()
		if !ok {
			return
		}
		if dir != "" {
			v.SetImageResolver(widget.DirImages(dir))
		} else {
			v.SetImageResolver(nil)
		}
		v.SetHTML(a.v)
		h.followCursorInPane()
	})
}

// htmlPaneSoon renders the pane again once the editor has been quiet, when it is open.
func (h *Host) htmlPaneSoon() {
	if !h.htmlPaneShown() {
		return
	}
	h.htmlPane.gen++
	gen := h.htmlPane.gen
	h.after(htmlPaneDelay, func() {
		if gen == h.htmlPane.gen && h.htmlPaneShown() {
			h.renderHTMLPane()
		}
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
				h.notify("open " + href + ": " + err.Error())
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
