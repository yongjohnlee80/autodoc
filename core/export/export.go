package export

import (
	"bytes"
	"errors"
	"fmt"
	stdhtml "html"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/diagram"
	"github.com/yongjohnlee80/golib/parse/markdown"
	markdownhtml "github.com/yongjohnlee80/golib/parse/markdown/html"
)

type Format string

const (
	HTML Format = "html"
	Text Format = "text"
)

type palette struct {
	background, foreground, accent, muted, surface, border string
}

var palettes = map[string]palette{
	"dark":  {"#1c1c1c", "#d0d0d0", "#87afd7", "#8a8a8a", "#303030", "#585858"},
	"light": {"#f6f2e7", "#2e2a24", "#2f5f8f", "#7a7163", "#ebe6d9", "#b3aa96"},
}

var mermaidBlock = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)

func Render(source []byte, format Format, theme string) ([]byte, error) {
	document := markdown.Parse(source, markdown.GFM(), markdown.Obsidian())
	switch format {
	case HTML:
		colors, ok := palettes[theme]
		if !ok {
			return nil, fmt.Errorf("export: theme %q must be light or dark", theme)
		}
		forEach(document.Root, func(node *markdown.Node) {
			if node.Kind == markdown.KindImage {
				node.Kind = markdown.KindEmph
			}
		})
		var body bytes.Buffer
		if err := markdownhtml.Render(&body, document); err != nil {
			return nil, err
		}
		rendered := mermaidBlock.ReplaceAllStringFunc(body.String(), func(block string) string {
			match := mermaidBlock.FindStringSubmatch(block)
			source := stdhtml.UnescapeString(match[1])
			model, err := diagram.Parse(source)
			if err != nil {
				return `<div class="diagram-error">Unsupported Mermaid construct: ` + stdhtml.EscapeString(err.Error()) + block + `</div>`
			}
			return diagramSVG(model, colors)
		})
		var output bytes.Buffer
		output.WriteString("<!doctype html>\n<html lang=\"en\"><head><meta charset=\"utf-8\">\n")
		output.WriteString("<meta http-equiv=\"Content-Security-Policy\" content=\"default-src 'none'; style-src 'unsafe-inline'; img-src data:\">\n")
		output.WriteString("<meta name=\"color-scheme\" content=\"" + theme + "\">\n")
		fmt.Fprintf(&output, "<style>:root{color-scheme:%s;--background:%s;--foreground:%s;--accent:%s;--muted:%s;--surface:%s;--border:%s}",
			theme, colors.background, colors.foreground, colors.accent, colors.muted, colors.surface, colors.border)
		output.WriteString("body{max-width:76ch;margin:3rem auto;padding:0 1.5rem;background:var(--background);color:var(--foreground);font:1rem/1.6 system-ui,sans-serif}")
		output.WriteString("a{color:var(--accent)}pre,code{background:var(--surface)}pre{padding:1rem;overflow:auto;border:1px solid var(--border)}")
		output.WriteString("blockquote{border-left:.2rem solid var(--accent);padding-left:1rem;color:var(--muted)}table{border-collapse:collapse}th,td{border:1px solid var(--border);padding:.3rem .6rem}")
		output.WriteString("svg.diagram{max-width:100%;height:auto;background:var(--background)}.diagram-error{color:var(--accent)}")
		output.WriteString("</style></head><body>\n")
		output.WriteString(rendered)
		output.WriteString("</body></html>\n")
		return output.Bytes(), nil
	case Text:
		var output bytes.Buffer
		writeText(&output, document.Root, document.Source)
		return []byte(strings.TrimSpace(output.String()) + "\n"), nil
	default:
		return nil, fmt.Errorf("export: format %q must be html or text", format)
	}
}

func diagramSVG(model diagram.Model, colors palette) string {
	rows := max(len(model.Edges), len(model.Nodes))
	if rows == 0 {
		rows = 1
	}
	height := rows*62 + 20
	var output strings.Builder
	fmt.Fprintf(&output, `<svg class="diagram" xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Mermaid diagram" viewBox="0 0 760 %d">`, height)
	for index, edge := range model.Edges {
		y := 20 + index*62
		from, to := diagramLabel(model, edge.From), diagramLabel(model, edge.To)
		fmt.Fprintf(&output, `<rect x="8" y="%d" width="240" height="42" rx="6" fill="%s" stroke="%s"/>`, y, colors.surface, colors.border)
		fmt.Fprintf(&output, `<rect x="512" y="%d" width="240" height="42" rx="6" fill="%s" stroke="%s"/>`, y, colors.surface, colors.border)
		fmt.Fprintf(&output, `<text x="128" y="%d" text-anchor="middle" fill="%s">%s</text>`, y+27, colors.foreground, stdhtml.EscapeString(from))
		fmt.Fprintf(&output, `<text x="632" y="%d" text-anchor="middle" fill="%s">%s</text>`, y+27, colors.foreground, stdhtml.EscapeString(to))
		fmt.Fprintf(&output, `<line x1="252" y1="%d" x2="506" y2="%d" stroke="%s" stroke-width="2"/>`, y+21, y+21, colors.accent)
		fmt.Fprintf(&output, `<path d="M 506 %d l -9 -5 v 10 z" fill="%s"/>`, y+21, colors.accent)
		if edge.Label != "" {
			fmt.Fprintf(&output, `<text x="380" y="%d" text-anchor="middle" fill="%s">%s</text>`, y+14, colors.accent, stdhtml.EscapeString(shortLabel(edge.Label)))
		}
	}
	if len(model.Edges) == 0 {
		for index, node := range model.Nodes {
			y := 20 + index*62
			fmt.Fprintf(&output, `<rect x="260" y="%d" width="240" height="42" rx="6" fill="%s" stroke="%s"/>`, y, colors.surface, colors.border)
			fmt.Fprintf(&output, `<text x="380" y="%d" text-anchor="middle" fill="%s">%s</text>`, y+27, colors.foreground, stdhtml.EscapeString(shortLabel(node.Label)))
		}
	}
	output.WriteString("</svg>")
	return output.String()
}

func diagramLabel(model diagram.Model, id string) string {
	for _, node := range model.Nodes {
		if node.ID == id {
			return shortLabel(node.Label)
		}
	}
	return shortLabel(id)
}

func shortLabel(label string) string {
	runes := []rune(label)
	if len(runes) > 28 {
		return string(runes[:27]) + "…"
	}
	return label
}

func forEach(node *markdown.Node, visit func(*markdown.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.Next {
		forEach(child, visit)
	}
}

func writeText(output *bytes.Buffer, node *markdown.Node, source []byte) {
	switch node.Kind {
	case markdown.KindFrontmatter, markdown.KindLinkRefDef, markdown.KindHTMLBlock, markdown.KindRawHTML:
		return
	case markdown.KindText, markdown.KindCodeSpan, markdown.KindAutolink:
		output.Write(node.Text(source))
		return
	case markdown.KindCodeBlock:
		output.Write(node.Literal)
		output.WriteByte('\n')
		return
	case markdown.KindSoftBreak, markdown.KindHardBreak:
		output.WriteByte('\n')
		return
	}
	for child := node.FirstChild; child != nil; child = child.Next {
		writeText(output, child, source)
	}
	switch node.Kind {
	case markdown.KindHeading, markdown.KindParagraph, markdown.KindItem, markdown.KindTableRow:
		output.WriteByte('\n')
	case markdown.KindList, markdown.KindBlockQuote, markdown.KindTable:
		output.WriteByte('\n')
	}
}

func WriteFile(destination string, content []byte) error {
	if destination == "" {
		return errors.New("export: choose a destination")
	}
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".autodoc-export-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err = temporary.Write(content); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}
