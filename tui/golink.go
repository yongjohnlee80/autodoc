package tui

import (
	"github.com/yongjohnlee80/golib/parse/markdown"
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
	doc := markdown.Parse(source, markdown.GFM(), markdown.Obsidian())
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
				found, ok = cursorLink{raw: raw, dest: string(c.Dest)}, len(c.Dest) > 0
			case markdown.KindWikilink:
				if c.Target != nil && len(c.Target.Page) > 0 {
					found, ok = cursorLink{raw: raw, dest: string(c.Target.Page), wiki: true}, true
				}
			default:
				walk(c)
			}
		}
	}
	walk(doc.Root)
	return found, ok
}

// goToLink opens what the link under the cursor names: the document the daemon resolved it to, in
// this workspace; else a web or mail address in the browser, or a Markdown link's file beside the
// note, as the HTML pane opens them. A wikilink the daemon did not resolve says so.
func (h *Host) goToLink(l cursorLink) {
	for _, e := range h.relOut {
		if e.raw == l.raw && e.resolved {
			h.openIn(h.relFor.ws, e.path)
			return
		}
	}
	if l.wiki {
		h.notify(l.raw + " names no document in this workspace")
		return
	}
	h.htmlLink(l.dest)
}
