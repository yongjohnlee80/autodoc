//go:build gui

package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	guimermaid "github.com/yongjohnlee80/golib/gui/mermaid"
	"github.com/yongjohnlee80/golib/gui/vegalite"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// --check-diagram, as the GUI build draws: a diagram golib draws is ok; one it does not is "not
// drawn" with golib's own error, the parser's words for a Mermaid kind outside its subset, vegalite's
// for a chart, a syntax error at its line in the file, and a .md's fences each at its line. Every
// file drawn exits 0; any not drawn exits 1.
func TestCheckDiagramSaysWhatGolibDraws(t *testing.T) {
	git := "kanban\n  Todo\n    [Write the docs]\n  Done\n    [Ship it]\n"
	_, gitWhy := pm.Parse(git)
	if gitWhy == nil {
		t.Fatal("golib's parser takes a kanban board: the cell needs a kind outside its subset")
	}
	bar := `{"data":{"values":[{"k":"a","v":1}]},"mark":"bar","encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}`
	md := "# Report\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\n```vega-lite\n" + ruleByField + "\n```\n"
	dir := writeFiles(t, map[string]string{
		"flow.mmd": "flowchart LR\n  A --> B\n", "git.mmd": git, "broken.mmd": "flowchart TB\n  A[unclosed --> B\n",
		"bar.chart.json": bar, "levels.chart.json": ruleByField, "report.md": md,
	})
	at := func(name string) string { return filepath.Join(dir, name) }

	code, out := runMain(t, nil, "--check-diagram", at("flow.mmd"), at("bar.chart.json"))
	if code != diagramsDrawn || !strings.Contains(out, at("flow.mmd")+":1: ok (mermaid)") || !strings.Contains(out, at("bar.chart.json")+":1: ok (vega-lite)") {
		t.Fatalf("diagrams golib draws: %d\n%s", code, out)
	}

	code, out = runMain(t, nil, "--check-diagram", at("git.mmd"), at("levels.chart.json"), at("broken.mmd"), at("report.md"))
	if code != diagramsNotDrawn {
		t.Errorf("diagrams golib does not draw: exit %d, want %d\n%s", code, diagramsNotDrawn, out)
	}
	for _, want := range []string{
		at("git.mmd") + ":1: not drawn: ", gitWhy.Error(),
		at("levels.chart.json") + ":1: not drawn: ", "a rule coloured by a field",
		at("broken.mmd") + ":2: not drawn: mermaid: line 2",
		at("report.md") + ":4: ok (mermaid)",
		at("report.md") + ":9: not drawn: ",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the output lacks %q:\n%s", want, out)
		}
	}

	// the fences are the Markdown parser's: a fence shown inside a longer one is no diagram, a
	// kanban board in a block quote is one, and a closing fence with trailing spaces ends the flowchart it closes
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
	if code != diagramsNotDrawn || !strings.Contains(out, at("shapes.md")+":11: not drawn: "+gitWhy.Error()) ||
		!strings.Contains(out, at("shapes.md")+":16: ok (mermaid)") || strings.Contains(out, at("shapes.md")+":4:") {
		t.Errorf("a quoted kanban board, a text example and a flowchart: %d\n%s", code, out)
	}
}

// --diagram-reference is golib's own tables: every kind gui/mermaid draws with each of its keywords,
// and each list of gui/vegalite's subset, as golib answers them; exit 0. A few names are pinned too,
// so the reference is seen to carry them.
func TestTheDiagramReferenceIsGolibs(t *testing.T) {
	code, out := runMain(t, nil, "--diagram-reference")
	if code != 0 {
		t.Fatalf("exit %d: %s", code, out)
	}
	code1 := func(names []string) string {
		q := make([]string, len(names))
		for i, n := range names {
			q[i] = "`" + n + "`"
		}
		return strings.Join(q, ", ")
	}
	var want []string
	for _, k := range guimermaid.Kinds() {
		want = append(want, "- "+k.Kind.String()+": "+code1(k.Keywords)+"\n")
	}
	s := vegalite.Subset()
	want = append(want, "- Marks: "+code1(s.Marks), "- Channels: "+code1(s.Channels), "- Field types: "+code1(s.FieldTypes),
		"- Scale types: "+code1(s.ScaleTypes), "- Aggregates: "+code1(s.Aggregates))
	for place, keys := range s.Passed {
		want = append(want, "- "+place+": "+code1(keys)+"\n")
	}
	want = append(want, "- flowchart: `flowchart`, `graph`\n", "- timeline: `timeline`\n", "`rule`", "`average`", "`utc`")
	// the kinds it says aren't drawn are none it draws: any golib reads and doesn't draw, then ones its
	// parser doesn't read (a pie and a gantt are drawn now, so they're no example)
	drawnNames := map[string]bool{}
	for _, k := range guimermaid.Kinds() {
		drawnNames[k.Kind.String()] = true
	}
	var undrawn []string
	for _, k := range pm.Kinds() {
		if !drawnNames[k.Kind.String()] {
			undrawn = append(undrawn, k.Kind.String())
		}
	}
	want = append(want, "A diagram of any other kind ("+code1(append(undrawn, "kanban"))+", …) is not drawn")
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("the reference lacks %q:\n%s", w, out)
		}
	}
}

// TestTheViewerDrawsEveryKindTheParserReads: doc.validate checks a diagram with golib's parser alone
// (core/diagram, every build), so the viewer's layout must lay out every kind that parser reads, or
// a diagram doc.validate passes could still go undrawn. And what doc.validate refuses, the viewer
// refuses: an ADR's <job id> label is not drawn.
func TestTheViewerDrawsEveryKindTheParserReads(t *testing.T) {
	var parsed, laid []pm.Kind
	for _, k := range pm.Kinds() {
		parsed = append(parsed, k.Kind)
	}
	for _, k := range guimermaid.Kinds() {
		laid = append(laid, k.Kind)
	}
	if !slices.Equal(parsed, laid) {
		t.Errorf("the parser reads %v, the viewer lays out %v: doc.validate would pass what the viewer can't draw", parsed, laid)
	}
	if err := layDiagram("mermaid", "flowchart LR\n  A --> B[<job id>]\n"); err == nil {
		t.Error("the viewer drew a <job id> label, which doc.validate diagnoses")
	}
}

// --check-diagram checks a note's embedded diagrams as it checks its fences: each line holding
// only an image of a .mmd or .chart.json file (export.EmbedLines, the page's rule), its file read
// where the page reads it (beside the note, in a subfolder, or in the note's resources folder),
// reported at the note's line with the embed named: ok, not drawn with golib's reason (at the
// line of the embedded file), a missing file, or a file outside the note's folder. An image (.png)
// and a table (.csv) are no diagram: nothing is said of them. Any embed not drawn exits 1; a note
// whose embeds are all drawn exits 0, and one with only images says it has no diagrams (the
// mutants: the embeds not looked for; a missing file not said so; the error's line not the embedded
// file's).
func TestCheckDiagramChecksTheNotesEmbeds(t *testing.T) {
	bar := `{"data":{"values":[{"k":"a","v":1}]},"mark":"bar","encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}`
	note := "# Note\n\n![flow](flow.mmd)\n\n![sales](assets/sales.chart.json)\n\n![levels](levels.chart.json)\n\n" +
		"![gone](gone.mmd)\n\n![photo](photo.png)\n\n![table](t.csv)\n\n![up](../up.mmd)\n\n![kept](kept.mmd)\n\n![broken](broken.mmd)\n"
	dir := writeFiles(t, map[string]string{
		"note.md": note, "flow.mmd": "flowchart LR\n  A --> B\n", "levels.chart.json": ruleByField,
		"photo.png": "not a picture", "t.csv": "a,b\n1,2\n", "broken.mmd": "flowchart TB\n  A[unclosed --> B\n",
		"good.md": "![flow](flow.mmd)\n\nText.\n\n![sales](assets/sales.chart.json)\n", "pictures.md": "![photo](photo.png)\n",
	})
	for name, text := range map[string]string{"assets/sales.chart.json": bar, "note/kept.mmd": "flowchart LR\n  C --> D\n"} {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	at := filepath.Join(dir, "note.md")

	code, out := runMain(t, nil, "--check-diagram", at)
	if code != diagramsNotDrawn {
		t.Errorf("a note with embeds not drawn: exit %d, want %d\n%s", code, diagramsNotDrawn, out)
	}
	want := []string{
		at + ":3: flow.mmd: ok (mermaid)",
		at + ":5: assets/sales.chart.json: ok (vega-lite)",
		at + ":7: levels.chart.json: not drawn: ",
		at + ":9: gone.mmd: missing file",
		at + ":15: ../up.mmd: not read: not in the note's folder",
		at + ":17: kept.mmd: ok (mermaid)",
		at + ":19: broken.mmd:2: not drawn: mermaid: line 2",
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != len(want) {
		t.Fatalf("%d lines, want %d, one per diagram embed in the note's order:\n%s", len(lines), len(want), out)
	}
	for i, w := range want {
		if !strings.HasPrefix(lines[i], w) {
			t.Errorf("line %d: %q, want it to begin %q", i+1, lines[i], w)
		}
	}
	if !strings.Contains(lines[2], "a rule coloured by a field") || strings.Contains(out, "photo.png") || strings.Contains(out, "t.csv") {
		t.Errorf("golib's reason for the chart, and nothing of the image or the table:\n%s", out)
	}

	if code, out := runMain(t, nil, "--check-diagram", filepath.Join(dir, "good.md")); code != diagramsDrawn ||
		!strings.Contains(out, "good.md:1: flow.mmd: ok (mermaid)") || !strings.Contains(out, "good.md:5: assets/sales.chart.json: ok (vega-lite)") {
		t.Errorf("a note whose embeds all draw: exit %d, want %d\n%s", code, diagramsDrawn, out)
	}
	if code, out := runMain(t, nil, "--check-diagram", filepath.Join(dir, "pictures.md")); code != diagramsDrawn ||
		!strings.Contains(out, "pictures.md: no mermaid or vega-lite diagrams") {
		t.Errorf("a note with only an image: exit %d, want %d, saying it has no diagrams\n%s", code, diagramsDrawn, out)
	}
}
