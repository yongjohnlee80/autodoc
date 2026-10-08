package tui

import (
	"strings"

	"github.com/yongjohnlee80/golib/parse/markdown"
)

func mermaidBlock(source []byte, cursor int) (string, bool) {
	document := markdown.Parse(source, markdown.GFM(), markdown.Obsidian())
	var first, selected string
	var walk func(*markdown.Node)
	walk = func(node *markdown.Node) {
		if node.Kind == markdown.KindCodeBlock && strings.EqualFold(strings.TrimSpace(string(node.Info)), "mermaid") {
			if first == "" {
				first = string(node.Literal)
			}
			if cursor >= node.Span.Start && cursor <= node.Span.End {
				selected = string(node.Literal)
			}
		}
		for child := node.FirstChild; child != nil; child = child.Next {
			walk(child)
		}
	}
	walk(document.Root)
	if selected != "" {
		return selected, true
	}
	return first, first != ""
}

func (h *Host) previewDiagram() {
	source, ok := mermaidBlock([]byte(h.core.Value()), h.cursorBytes())
	if !ok {
		h.notify("no Mermaid fenced block in this file")
		return
	}
	h.showDiagram(source) // an image where it can be, else its source (preview.go)
}
