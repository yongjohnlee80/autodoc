package tui

import (
	"context"

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

// goToLink opens what the link under the cursor names: the document the daemon resolves it to now
// (graph.resolve: as the index will, so a link just typed, saved or not, goes where it will lead);
// else a web or mail address in the browser, or a Markdown link's file, as the HTML pane opens
// them. A link to no document, or to several, says so.
func (h *Host) goToLink(l cursorLink) {
	if !h.file.open || h.file.outside() { // in no workspace's graph: the link as written
		h.htmlLink(l.dest)
		return
	}
	ws, from, gen, ep := h.file.ws, h.file.path, h.file.gen, h.epoch
	do(h, func(ctx context.Context) answerOf[map[string]any] {
		res, err := h.call(ctx, "graph.resolve", ws, from, l.raw)
		return answerOf[map[string]any]{v: asMap(res), err: err}
	}, func(a answerOf[map[string]any]) {
		if gen != h.file.gen || ep != h.epoch {
			return // another file opened, or another daemon, since: the link was the old file's
		}
		switch path, reason := str(a.v, "path"), str(a.v, "reason"); {
		case a.err != nil:
			h.notify("go to link: " + a.err.Error())
		case path != "":
			h.openIn(ws, path)
		case reason == "ambiguous":
			h.notify(l.raw + " names several documents: rename one, or write its path")
		case reason != "" || l.wiki:
			h.notify(l.raw + " names no document in this workspace")
		default:
			h.htmlLink(l.dest)
		}
	})
}
