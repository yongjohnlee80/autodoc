//go:build gui

package app

import (
	"path/filepath"
	"strings"
	"testing"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// --check-diagram, as the GUI build draws: a diagram golib draws is ok; one it does not is "not
// drawn" with golib's own error, the parser's words for a Mermaid kind outside its subset, vegalite's
// for a chart, a syntax error at its line in the file, and a .md's fences each at its line. Every
// file drawn exits 0; any not drawn exits 1.
func TestCheckDiagramSaysWhatGolibDraws(t *testing.T) {
	pie := "pie\n  \"a\": 1\n"
	_, pieWhy := pm.Parse(pie)
	if pieWhy == nil {
		t.Fatal("golib's parser takes a pie: the cell needs a kind outside its subset")
	}
	bar := `{"data":{"values":[{"k":"a","v":1}]},"mark":"bar","encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}`
	md := "# Report\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\n```vega-lite\n" + ruleByField + "\n```\n"
	dir := writeFiles(t, map[string]string{
		"flow.mmd": "flowchart LR\n  A --> B\n", "pie.mmd": pie, "broken.mmd": "flowchart TB\n  A[unclosed --> B\n",
		"bar.chart.json": bar, "levels.chart.json": ruleByField, "report.md": md,
	})
	at := func(name string) string { return filepath.Join(dir, name) }

	code, out := runMain(t, nil, "--check-diagram", at("flow.mmd"), at("bar.chart.json"))
	if code != diagramsDrawn || !strings.Contains(out, at("flow.mmd")+":1: ok (mermaid)") || !strings.Contains(out, at("bar.chart.json")+":1: ok (vega-lite)") {
		t.Fatalf("diagrams golib draws: %d\n%s", code, out)
	}

	code, out = runMain(t, nil, "--check-diagram", at("pie.mmd"), at("levels.chart.json"), at("broken.mmd"), at("report.md"))
	if code != diagramsNotDrawn {
		t.Errorf("diagrams golib does not draw: exit %d, want %d\n%s", code, diagramsNotDrawn, out)
	}
	for _, want := range []string{
		at("pie.mmd") + ":1: not drawn: ", pieWhy.Error(),
		at("levels.chart.json") + ":1: not drawn: ", "a rule coloured by a field",
		at("broken.mmd") + ":2: not drawn: mermaid: line 2",
		at("report.md") + ":4: ok (mermaid)",
		at("report.md") + ":9: not drawn: ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}

	// the fences are the Markdown parser's: a fence shown inside a longer one is no diagram, a pie
	// in a block quote is one, and a closing fence with trailing spaces ends the flowchart it closes
	example := strings.SplitAfter(diagramShapes, "````\n")[0]
	spaced := "```mermaid\nflowchart LR\n  A --> B\n```   \nAfter the fence, prose.\n"
	dir = writeFiles(t, map[string]string{"example.md": example, "spaced.md": spaced, "shapes.md": diagramShapes,
		"cr.md": "# Note\r\r```mermaid\rflowchart LR\r  A --> B\r```\r"})
	if code, out := runMain(t, nil, "--check-diagram", at("cr.md")); code != diagramsDrawn || !strings.Contains(out, at("cr.md")+":4: ok (mermaid)") {
		t.Errorf("a note whose lines end in a lone CR: %d\n%s", code, out)
	}
	if code, out := runMain(t, nil, "--check-diagram", at("example.md")); code != diagramsDrawn || !strings.Contains(out, "no mermaid or vega-lite diagrams") {
		t.Errorf("a mermaid fence shown as text: %d\n%s", code, out)
	}
	if code, out := runMain(t, nil, "--check-diagram", at("spaced.md")); code != diagramsDrawn || !strings.Contains(out, at("spaced.md")+":2: ok (mermaid)") {
		t.Errorf("a flowchart whose closing fence has trailing spaces: %d\n%s", code, out)
	}
	code, out = runMain(t, nil, "--check-diagram", at("shapes.md"))
	if code != diagramsNotDrawn || !strings.Contains(out, at("shapes.md")+":11: not drawn: "+pieWhy.Error()) ||
		!strings.Contains(out, at("shapes.md")+":16: ok (mermaid)") || strings.Contains(out, at("shapes.md")+":4:") {
		t.Errorf("a quoted pie, a text example and a flowchart: %d\n%s", code, out)
	}
}
