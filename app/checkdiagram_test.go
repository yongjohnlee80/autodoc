package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFiles writes each name's text under a new directory, and answers the directory.
func writeFiles(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// A file's diagrams: the whole of a .mmd, a .mermaid, a .chart.json or a .vl.json; each mermaid and
// vega-lite fence of a .md (``` or ~~~, vegalite spelled either way), with the line its source
// starts on, others' fences passed over; none for a .md without one. Another file is refused.
// The fences are the Markdown parser's: one shown inside a longer fence is text, one in a block
// quote is a diagram, and a closing fence with trailing spaces closes it. A line ends as the parser
// ends one: \n, \r\n or a lone \r.
func TestDiagramsAreFoundInFiles(t *testing.T) {
	md := "# Report\n\n```mermaid\nflowchart LR\n  A --> B\n```\n\n```go\nx := 1\n```\n\n~~~~ vegalite\n{\"mark\": \"bar\"}\n~~~~\n"
	dir := writeFiles(t, map[string]string{"report.md": md, "shapes.md": diagramShapes, "plain.md": "# none\n", "flow.mmd": "flowchart LR\n  A --> B\n",
		"seq.mermaid": "sequenceDiagram\n", "sales.chart.json": "{}", "old.vl.json": "{}", "notes.txt": "x"})
	at := func(name string) string { return filepath.Join(dir, name) }
	got, err := diagramsIn(at("report.md"))
	if err != nil || len(got) != 2 {
		t.Fatalf("report.md: %+v, %v; want its two fences", got, err)
	}
	if got[0].lang != "mermaid" || got[0].line != 4 || got[0].src != "flowchart LR\n  A --> B\n" {
		t.Errorf("the mermaid fence: %+v", got[0])
	}
	if got[1].lang != "vega-lite" || got[1].line != 13 || got[1].src != "{\"mark\": \"bar\"}\n" {
		t.Errorf("the vegalite fence: %+v", got[1])
	}
	for name, lang := range map[string]string{"flow.mmd": "mermaid", "seq.mermaid": "mermaid", "sales.chart.json": "vega-lite", "old.vl.json": "vega-lite"} {
		if got, err := diagramsIn(at(name)); err != nil || len(got) != 1 || got[0].lang != lang || got[0].line != 1 {
			t.Errorf("%s: %+v, %v; want the whole file, %s", name, got, err, lang)
		}
	}
	got, err = diagramsIn(at("shapes.md"))
	if err != nil || len(got) != 2 {
		t.Fatalf("shapes.md: %+v, %v; want the quoted pie and the flowchart, not the text example", got, err)
	}
	if got[0].line != 11 || got[0].src != "pie\n  \"a\": 1\n" {
		t.Errorf("the quoted pie: %+v", got[0])
	}
	if got[1].line != 16 || got[1].src != "flowchart LR\n  A --> B\n" {
		t.Errorf("the flowchart closed by a fence with trailing spaces: %+v", got[1])
	}
	for name, eol := range map[string]string{"cr.md": "\r", "crlf.md": "\r\n"} {
		src := strings.ReplaceAll("# Note\n\n```mermaid\nflowchart LR\n  A --> B\n```\n", "\n", eol)
		if err := os.WriteFile(at(name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		if got, err := diagramsIn(at(name)); err != nil || len(got) != 1 || got[0].line != 4 {
			t.Errorf("%s: %+v, %v; want its fence's source at line 4", name, got, err)
		}
	}
	if got, err := diagramsIn(at("plain.md")); err != nil || len(got) != 0 {
		t.Errorf("a .md without diagrams: %+v, %v", got, err)
	}
	if _, err := diagramsIn(at("notes.txt")); err == nil || !strings.Contains(err.Error(), "not a diagram") {
		t.Errorf("a .txt: %v; want it refused", err)
	}
}

// diagramShapes are the fences a line scan misreads: a mermaid fence shown inside a longer one, as
// text (lines 4 to 7); a pie in a block quote, its source from line 11; and a flowchart from line
// 16, whose closing fence has trailing spaces, then prose.
const diagramShapes = "Text example:\n\n````text\n```mermaid\npie\n  \"a\": 1\n```\n````\n\n" +
	"> ```mermaid\n> pie\n>   \"a\": 1\n> ```\n\n" +
	"```mermaid\nflowchart LR\n  A --> B\n```   \nAfter the fence, prose.\n"

// --check-diagram with no file says what it takes, and exits 2.
func TestCheckDiagramWithoutFiles(t *testing.T) {
	if code, out := runMain(t, nil, "--check-diagram"); code != diagramsUsage || !strings.Contains(out, "give the files to check") {
		t.Fatalf("no file: %d %s", code, out)
	}
}

// The reference writes what it is given: each Mermaid kind with its keywords, each Vega-Lite list,
// the layers and limits, and the taken keys by place, in name order.
func TestTheDiagramReferenceWritesTheSubset(t *testing.T) {
	var out strings.Builder
	writeDiagramReference(&out, drawnSubset{
		kinds: []drawnKind{{"flowchart", []string{"flowchart", "graph"}}, {"timeline", []string{"timeline"}}},
		marks: []string{"bar", "line"}, channels: []string{"x", "y"}, fieldTypes: []string{"nominal"},
		scaleTypes: []string{"linear"}, aggs: []string{"sum"}, layers: true, maxRows: 10, maxBands: 2, maxSeries: 3,
		passed: map[string][]string{"top": {"$schema"}, "axis": {"labelAngle", "orient"}},
	})
	text := out.String()
	for _, want := range []string{
		"## Mermaid", "- flowchart: `flowchart`, `graph`\n", "- timeline: `timeline`\n",
		"## Vega-Lite", "- Marks: `bar`, `line` ", "- Channels: `x`, `y` ", "- Field types: `nominal`\n",
		"- Scale types: `linear`\n", "- Aggregates: `sum` ", "- Layers: drawn", "- Limits: 10 rows of data, 2 categories on a discrete axis, 3 colour series",
		"- axis: `labelAngle`, `orient`\n- top: `$schema`\n", "autodoc --check-diagram",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the reference lacks %q:\n%s", want, text)
		}
	}
}
