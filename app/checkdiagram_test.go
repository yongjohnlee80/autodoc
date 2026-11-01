package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
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

// checkDiagrams reports each file's diagrams, a line each, at the file line of what was refused:
// a diagram's line plus golib's own line into its source, for a Mermaid syntax error and for a
// construct the parser won't draw alike. An unreadable file, a file of no diagram kind, and any
// diagram not drawn make the run exit 1; a .md without diagrams is said and isn't a failure, and a
// run whose every diagram is drawn exits 0. layDiagram is golib's Mermaid parser here, the check
// the GUI build's layout begins with, so the cell runs in every build.
func TestCheckDiagramsReportsEachDiagramAtItsFileLine(t *testing.T) {
	was := layDiagram
	t.Cleanup(func() { layDiagram = was })
	var laid []string
	layDiagram = func(lang, src string) error {
		laid = append(laid, lang)
		_, err := pm.Parse(src)
		return err
	}
	// report.md: the fence's source starts on line 4; its third line is refused, so file line 6
	report := "# Notes\n\n```mermaid\nflowchart TD\n  A --> B\n  B --> C[\"x <job id> y\"]\n```\n"
	dir := writeFiles(t, map[string]string{
		"ok.mmd":     "flowchart TD\n  A --> B\n",
		"broken.mmd": "flowchart TD\n  A --> B\n  C -->\n",
		"report.md":  report,
		"plain.md":   "# No diagrams\n",
		"notes.txt":  "x",
	})
	at := func(name string) string { return filepath.Join(dir, name) }
	var out strings.Builder
	code := checkDiagrams([]string{at("ok.mmd"), at("broken.mmd"), at("report.md"), at("plain.md"), at("notes.txt"), at("gone.mmd")}, &out)
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{
		at("ok.mmd") + ":1: ok (mermaid)",
		at("broken.mmd") + ":3: not drawn: mermaid: line 3, column 8: expected a node id",
		at("report.md") + ":6: not drawn: mermaid: not drawn natively: HTML in a label (line 3)",
		at("plain.md") + ": no mermaid or vega-lite diagrams",
		at("notes.txt") + ": not a diagram: a .mmd, .mermaid, .chart.json, .vl.json or .md file is",
	}
	if code != diagramsNotDrawn || len(lines) != len(want)+1 {
		t.Fatalf("exit %d, %d lines; want %d and %d lines:\n%s", code, len(lines), diagramsNotDrawn, len(want)+1, out.String())
	}
	for i, w := range want {
		if lines[i] != w {
			t.Errorf("line %d: %q\nwant      %q", i+1, lines[i], w)
		}
	}
	if gone := lines[len(want)]; !strings.HasPrefix(gone, at("gone.mmd")+": ") || !strings.Contains(gone, "no such file") {
		t.Errorf("an unreadable file: %q; want its read error", gone)
	}
	if len(laid) != 3 {
		t.Errorf("diagrams laid out: %v; want the three files' three diagrams, no more", laid)
	}

	out.Reset()
	if code := checkDiagrams([]string{at("ok.mmd"), at("plain.md")}, &out); code != diagramsDrawn {
		t.Errorf("every diagram drawn: exit %d; want %d:\n%s", code, diagramsDrawn, out.String())
	}
}

// errorLine is how far into its source golib's error is: a syntax error's or a refusal's line less
// one, and 0 for an error with no line (a refusal of the whole diagram) or that isn't golib's.
func TestErrorLineIsGolibsLineIntoTheSource(t *testing.T) {
	for _, c := range []struct {
		err  error
		want int
	}{
		{&pm.SyntaxError{Line: 3, Col: 8, Msg: "expected a node id"}, 2},
		{&pm.NotDrawnError{Line: 1, What: "HTML in a label"}, 0},
		{&pm.NotDrawnError{Line: 5, What: "HTML in a label"}, 4},
		{&pm.NotDrawnError{What: `"gantt" diagrams`}, 0},
		{&pm.SyntaxError{Msg: "no line"}, 0},
		{os.ErrNotExist, 0},
	} {
		if got := errorLine(c.err); got != c.want {
			t.Errorf("errorLine(%v) = %d; want %d", c.err, got, c.want)
		}
	}
}

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
