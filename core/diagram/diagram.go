// Package diagram finds a Markdown document's diagram fences as every reader of them does (the
// viewer, the export, --check-diagram and doc.validate), and checks a Mermaid one as the viewer's
// own parser reads it: golib's parse/mermaid, never a copy of its rules
// (docs/decisions/search.md#diagrams-the-viewer-cant-draw).
package diagram

import (
	"errors"

	"github.com/yongjohnlee80/golib/parse/markdown"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"

	"github.com/yongjohnlee80/autodoc/core/lab"
	"github.com/yongjohnlee80/autodoc/core/schema"
)

// The languages of the diagram fences Fences finds.
const (
	Mermaid  = "mermaid"
	VegaLite = "vega-lite"
)

// Fence is one diagram fence: the line its source starts on (the line after its opening fence, as
// the parser counts lines: \n, \r\n or a lone \r), its language and its source.
type Fence struct {
	Line      int
	Lang, Src string
}

// Fences are doc's mermaid and vega-lite fences (vegalite is vega-lite), in order. They are golib's
// Markdown parser's: a fence shown inside a longer one is text, and one in a block quote or a list
// is a fence.
func Fences(doc *markdown.Document) []Fence {
	var out []Fence
	var walk func(*markdown.Node)
	walk = func(n *markdown.Node) {
		for c := n.FirstChild; c != nil; c = c.Next {
			if c.Kind != markdown.KindCodeBlock || c.Fence() == 0 {
				walk(c)
				continue
			}
			lang := lab.FenceLang(string(c.Info()))
			if lang == "vegalite" {
				lang = VegaLite
			}
			if lang != Mermaid && lang != VegaLite {
				continue
			}
			out = append(out, Fence{Line: doc.Position(c.Span.Start).Line + 1, Lang: lang, Src: string(c.Literal)})
		}
	}
	walk(doc.Root)
	return out
}

// Rule is the rule of a diagnostic about a diagram.
const Rule = "mermaid"

// Diagnostics are doc's Mermaid diagrams the viewer can't draw, each at the line in the file of
// what its parser refused, or of its malformed construct: one a diagram, none for a diagram drawn.
// They never block a write, as no diagnostic does.
func Diagnostics(doc *markdown.Document) []schema.Diagnostic {
	var out []schema.Diagnostic
	for _, f := range Fences(doc) {
		if f.Lang != Mermaid {
			continue
		}
		if d, ok := Check(f); ok {
			out = append(out, d)
		}
	}
	return out
}

// Check is a Mermaid fence's diagnostic, and false when its parser reads it. A refusal is at the
// line of what it refused, or the fence's first line when that is no one line's (its type, its
// size); a malformed diagram at its malformed construct's.
func Check(f Fence) (schema.Diagnostic, bool) {
	_, err := pm.Parse(f.Src)
	if err == nil {
		return schema.Diagnostic{}, false
	}
	d := schema.Diagnostic{Line: f.Line, Rule: Rule}
	var nd *pm.NotDrawnError
	var se *pm.SyntaxError
	switch {
	case errors.As(err, &nd):
		d.Line += max(nd.Line-1, 0)
		d.Message = nd.What + ", diagram not drawn natively"
	case errors.As(err, &se):
		d.Line += max(se.Line-1, 0)
		d.Message = se.Msg + ", diagram malformed"
	default:
		d.Message = err.Error() + ", diagram not drawn natively"
	}
	return d, true
}
