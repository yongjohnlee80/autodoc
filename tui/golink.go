package tui

import (
	"context"
	"path/filepath"

	"github.com/yongjohnlee80/golib/parse/markdown"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// cursorLink is the link the cursor is on: its source text, as the daemon records a link's raw,
// and where it points (a Markdown link's destination, a wikilink's page).
type cursorLink struct {
	raw, dest string
	wiki      bool
}

// linkAt is the link under the cursor (a byte offset into source): a Markdown link, an autolink,
// or a wikilink naming a page. A wikilink to a heading of this note names none, so is none.
func linkAt(source []byte, cursor int) (cursorLink, bool) {
	doc := kind.ParseMarkdown(source)
	var found cursorLink
	var ok bool
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil && !ok; c = c.Next {
			if cursor < c.Span.Start || cursor >= c.Span.End {
				continue
			}
			raw := string(source[c.Span.Start:c.Span.End])
			switch c.Kind {
			case markdown.KindLink, markdown.KindAutolink:
				found, ok = cursorLink{raw: raw, dest: string(c.Dest())}, len(c.Dest()) > 0
			case markdown.KindWikilink:
				if t := c.Target(); t != nil && len(t.Page) > 0 {
					found, ok = cursorLink{raw: raw, dest: string(t.Page), wiki: true}, true
				}
			default:
				walk(c)
			}
		}
	}
	walk(doc.Root)
	return found, ok
}

// followLink is SPC j: the link under the editor's cursor, followed as the right-click menu's Go to
// link follows it, in Raw and in a note's Rendered view alike. Off a link it says so, and where the
// page's links are listed. On an HTML file's page, which has no cursor, it is the first link in the
// file's source (docs/decisions/editor.md#html-files-open-raw-or-simplified). In a draft (no file
// open) it does what Go to link does there: a link goes as written, a web address to the browser,
// since there is no workspace to resolve it in.
func (h *Host) followLink() {
	source := []byte(h.core.Value())
	name := untitled
	if h.file.open {
		name = h.file.path
	}
	if p := h.notePage(); p != nil && p.shownHere() {
		l, ok := firstLink(source)
		if !ok {
			h.notify(h.trf("autodoc.notice.golink.no_link_on_the_page_here_spc_l_shows_what_links", name))
			return
		}
		h.goToLink(l)
		return
	}
	l, ok := linkAt(source, h.cursorBytes())
	if !ok {
		h.notify(h.trf("autodoc.notice.golink.no_link_under_the_cursor_spc_l_shows_what_links", name))
		return
	}
	h.goToLink(l)
}

// firstLink is the first link in source, as linkAt reads one there.
func firstLink(source []byte) (cursorLink, bool) {
	doc := kind.ParseMarkdown(source)
	at := -1
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil && at < 0; c = c.Next {
			switch c.Kind {
			case markdown.KindLink, markdown.KindAutolink, markdown.KindWikilink:
				if _, ok := linkAt(source, c.Span.Start); ok {
					at = c.Span.Start
				}
			default:
				walk(c)
			}
		}
	}
	walk(doc.Root)
	if at < 0 {
		return cursorLink{}, false
	}
	return linkAt(source, at)
}

// goToLink opens what the link under the cursor names: the document the daemon resolves it to now
// (graph.resolve: as the index will, so a link just typed, saved or not, goes where it will lead);
// else a web or mail address in the browser, or a Markdown link's file, as the HTML pane opens
// them. A link to no document, or to several, says so.
func (h *Host) goToLink(l cursorLink) {
	who := h.authorityNow()               // the answer comes later: whoever followed the link, it opens as they act
	if !h.file.open || h.file.outside() { // in no workspace's graph: the link as written
		h.htmlLinkAs(who, l.dest)
		return
	}
	ws, from, tk := h.file.ws, h.file.path, h.ticket(nil, TicketFile)
	do(h, func(ctx context.Context) answerOf[map[string]any] {
		res, err := h.call(ctx, "graph.resolve", ws, from, l.raw)
		return answerOf[map[string]any]{v: asMap(res), err: err}
	}, func(a answerOf[map[string]any]) {
		if !tk.Current() {
			return // another file opened, or another daemon, since: the link was the old file's
		}
		switch path, reason := str(a.v, "path"), str(a.v, "reason"); {
		case a.err != nil:
			h.failed("go to link", a.err)
		case path != "" && who.gated(): // a document, opened within the script's bounds
			h.openAbsoluteAs(who, filepath.Join(h.rootOf(ws), filepath.FromSlash(path)))
		case path != "":
			h.openIn(ws, path)
		case reason == "ambiguous":
			h.notify(h.trf("autodoc.notice.golink.names_several_documents_rename_one_or_write_its", l.raw))
		case reason != "" || l.wiki:
			h.notify(h.trf("autodoc.notice.golink.names_no_document_in_this_workspace", l.raw))
		default:
			h.htmlLinkAs(who, l.dest)
		}
	})
}
