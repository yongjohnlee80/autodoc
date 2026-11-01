package diagram_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/diagram"
	"github.com/yongjohnlee80/autodoc/core/kind"
)

// TestADiagramTheViewerCantDrawIsDiagnosedAtItsLine: each mermaid fence the viewer's parser refuses
// is one diagnostic, rule mermaid, at the file's line of what it refused (HTML in a label, as an ADR's
// `<job id>`), or at the fence's first line for a type it doesn't draw, with the parser's reason; a
// malformed one at its malformed line; `<br/>` in a label, and a vega-lite fence, are none.
func TestADiagramTheViewerCantDrawIsDiagnosedAtItsLine(t *testing.T) {
	src := strings.Join([]string{
		"---", // 1
		"type: adr",
		"---",
		"# Jobs",
		"", // 5
		"```mermaid",
		"flowchart LR", // 7
		"  A[queue] --> B[worker]",
		"  B --> C[<job id>]", // 9: HTML in a label
		"```",                 // 10
		"",
		"```mermaid",
		"flowchart LR", // 13
		"  A[one<br/>two] --> B[three]",
		"```",
		"",
		"> ```mermaid", // 17: a fence in a block quote is a fence
		"> gantt",      // 18
		">   title x",
		"> ```",
		"",
		"```mermaid",
		"flowchart LR",       // 23
		"  A[unclosed --> B", // 24: malformed
		"```",
		"",
		"```vegalite",
		`{"mark": "bar"}`,
		"```",
		"",
		"````markdown",
		"```mermaid",
		"flowchart LR", // shown inside a longer fence: text, never a diagram
		"  A[<b>x</b>]",
		"```",
		"````",
	}, "\n") + "\n"
	doc := kind.ParseMarkdown([]byte(src))
	ds := diagram.Diagnostics(doc)
	type got struct {
		line int
		msg  string
	}
	var have []got
	for _, d := range ds {
		if d.Rule != diagram.Rule || d.Field != "" {
			t.Errorf("a diagnostic of rule %q, field %q: %+v", d.Rule, d.Field, d)
		}
		have = append(have, got{d.Line, d.Message})
	}
	want := []got{
		{9, "HTML in a label, diagram not drawn natively"},
		{18, `"gantt" diagrams, diagram not drawn natively`},
	}
	if len(have) != 3 || !slices.Equal(have[:2], want) || have[2].line != 24 || !strings.HasSuffix(have[2].msg, ", diagram malformed") {
		t.Errorf("diagnostics %+v, want %+v and a malformed one at line 24", have, want)
	}
	var langs []string
	for _, f := range diagram.Fences(doc) {
		langs = append(langs, f.Lang)
	}
	if !slices.Equal(langs, []string{diagram.Mermaid, diagram.Mermaid, diagram.Mermaid, diagram.Mermaid, diagram.VegaLite}) {
		t.Errorf("fences %v: want the four mermaid ones and the vegalite one, as vega-lite, none from inside the longer fence", langs)
	}
}
