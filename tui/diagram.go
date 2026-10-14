package tui

import (
	"bytes"
	"path"
	"strings"

	"github.com/yongjohnlee80/golib/parse/markdown"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/kind"
)

// mermaidBlock is the Mermaid source the diagram view shows: the fenced block at cursor, else the
// file's first. read gives an embed's source: with it, a line embedding a diagram file
// (![title](flow.mmd)) is a diagram too, as a fence is, at the cursor when the cursor is on its
// line; nil reads none. An embed whose file read cannot give is passed over.
func mermaidBlock(source []byte, cursor int, read func(dest string) (string, bool)) (string, bool) {
	document := kind.ParseMarkdown(source)
	var first, selected string
	var found, at bool
	take := func(src string, here bool) {
		if !found {
			first, found = src, true
		}
		if here && !at {
			selected, at = src, true
		}
	}
	var walk func(*markdown.Node)
	walk = func(node *markdown.Node) {
		switch {
		case node.Kind == markdown.KindCodeBlock && strings.EqualFold(strings.TrimSpace(string(node.Info)), "mermaid"):
			take(string(node.Literal), cursor >= node.Span.Start && cursor <= node.Span.End)
		case node.Kind == markdown.KindImage && read != nil && isDiagramFile(string(node.Dest)):
			if src, ok := read(string(node.Dest)); ok {
				start, end := lineAround(source, node.Span.Start, node.Span.End)
				take(src, cursor >= start && cursor <= end)
			}
		}
		for child := node.FirstChild; child != nil; child = child.Next {
			walk(child)
		}
	}
	walk(document.Root)
	if at {
		return selected, true
	}
	return first, found
}

// isDiagramFile reports whether dest names a Mermaid file (.mmd), its query or fragment aside.
func isDiagramFile(dest string) bool {
	if i := strings.IndexAny(dest, "?#"); i >= 0 {
		dest = dest[:i]
	}
	return strings.EqualFold(path.Ext(dest), ".mmd")
}

// lineAround is the bytes [start, end) widened to the whole lines they are on, its newline aside.
func lineAround(source []byte, start, end int) (int, int) {
	start = bytes.LastIndexByte(source[:start], '\n') + 1
	if i := bytes.IndexByte(source[end:], '\n'); i >= 0 {
		return start, end + i
	}
	return start, len(source)
}

// diagramReader reads the open file's diagram embeds as the HTML preview does: beside it, then in
// its resources folder (export.EmbedDiagram). nil when no file on disk is open: an untitled draft
// has no folder.
func (h *Host) diagramReader() func(dest string) (string, bool) {
	note := h.diskPath()
	if note == "" {
		return nil
	}
	return func(dest string) (string, bool) { return export.EmbedDiagram(note, dest) }
}

func (h *Host) previewDiagram() {
	source, ok := mermaidBlock([]byte(h.core.Value()), h.cursorBytes(), h.diagramReader())
	if !ok {
		h.notify("no Mermaid diagram in this file: a mermaid fence, or an embed of a .mmd file")
		return
	}
	h.showDiagram(source) // an image where it can be, else its source (preview.go)
}
