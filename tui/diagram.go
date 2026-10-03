package tui

import (
	"fmt"
	"strings"

	"github.com/yongjohnlee80/golib/parse/markdown"

	"github.com/yongjohnlee80/autodoc/core/diagram"
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
	source, ok := mermaidBlock([]byte(h.editor.Value()), h.cursorBytes())
	if !ok {
		h.notify("no Mermaid fenced block in this note")
		return
	}
	model, err := diagram.Parse(source)
	if err != nil {
		h.showDiagramText(fmt.Sprintf("Unsupported Mermaid construct: %v\n\nSource:\n%s", err, source),
			"The source remains editable; the renderer supports basic flowcharts and sequences.")
		return
	}
	h.showDiagram(model) // an image where it can be, else the terminal graph (preview.go)
}
