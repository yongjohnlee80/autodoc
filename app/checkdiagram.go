package app

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// --check-diagram checks diagrams against the GUI's own renderer, with no UI: each file's Mermaid
// diagram or Vega-Lite chart, or a Markdown file's mermaid and vega-lite fences, laid out by golib's
// gui/mermaid and gui/vegalite as the Rendered view lays them out. An agent checks a diagram before
// it shows one; a chart or diagram golib does not draw says why in golib's words.
//
//	autodoc --check-diagram notes/flow.mmd notes/sales.chart.json notes/report.md
//
// The exit codes, which say what was checked and how it went:
const (
	diagramsDrawn    = 0 // every diagram drawn
	diagramsNotDrawn = 1 // one or more not drawn, or a file unread or of no diagram kind
	diagramsUsage    = 2 // no file given
	diagramsNoGUI    = 3 // this build has no golib/gui to draw with (the non-GUI build)
)

// layDiagram lays out src in lang ("mermaid" or "vega-lite") as the Rendered view does, answering
// golib's error when it cannot. The GUI build's (checkdiagram_on.go); nil without it.
var layDiagram func(lang, src string) error

// checkDiagram is one diagram to check: its file and the line its source starts on there, its
// language, and its source.
type checkDiagram struct {
	file      string
	line      int
	lang, src string
}

// diagramFence is a mermaid or vega-lite fence's opening line: its marker (``` or ~~~, three or
// more) and its language. Indented up to three spaces, as CommonMark's.
var diagramFence = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})\\s*(mermaid|vega-lite|vegalite)\\s*$")

// diagramsIn are the diagrams file holds: the whole of a .mmd or .mermaid (Mermaid), the whole of a
// .chart.json or .vl.json (Vega-Lite), each mermaid and vega-lite fence of a .md, none for a .md
// with none. Any other file is refused.
func diagramsIn(file string) ([]checkDiagram, error) {
	name := strings.ToLower(filepath.Base(file))
	var lang string
	switch {
	case strings.HasSuffix(name, ".mmd"), strings.HasSuffix(name, ".mermaid"):
		lang = "mermaid"
	case strings.HasSuffix(name, ".chart.json"), strings.HasSuffix(name, ".vl.json"):
		lang = "vega-lite"
	case strings.HasSuffix(name, ".md"):
	default:
		return nil, errors.New("not a diagram: a .mmd, .mermaid, .chart.json, .vl.json or .md file is")
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	if lang != "" {
		return []checkDiagram{{file: file, line: 1, lang: lang, src: string(data)}}, nil
	}
	var out []checkDiagram
	lines := strings.Split(string(data), "\n")
	for i := 0; i < len(lines); i++ {
		m := diagramFence.FindStringSubmatch(strings.TrimRight(lines[i], "\r"))
		if m == nil {
			continue
		}
		marker, start := m[1], i+1
		var src []string
		for i++; i < len(lines); i++ {
			l := strings.TrimRight(lines[i], "\r")
			if t := strings.TrimLeft(l, " "); len(l)-len(t) <= 3 && strings.HasPrefix(t, marker[:3]) && strings.Trim(t, marker[:1]) == "" && len(t) >= len(marker) {
				break // the closing fence: the same character, at least as many
			}
			src = append(src, l)
		}
		lang := m[2]
		if lang == "vegalite" {
			lang = "vega-lite"
		}
		out = append(out, checkDiagram{file: file, line: start + 1, lang: lang, src: strings.Join(src, "\n")})
	}
	return out, nil
}

// checkDiagrams is --check-diagram: each file's diagrams laid out, a line for each, "file:line:
// ok (mermaid)" or "file:line: not drawn: <golib's error>", and the exit code (diagramsDrawn …).
func checkDiagrams(files []string, out io.Writer) int {
	if len(files) == 0 {
		fmt.Fprintln(out, "autodoc --check-diagram: give the files to check: .mmd, .mermaid, .chart.json, .vl.json or .md")
		return diagramsUsage
	}
	if layDiagram == nil {
		fmt.Fprintln(out, "autodoc --check-diagram: this build draws no diagrams: it needs the GUI build (golib/gui), as --gui does")
		return diagramsNoGUI
	}
	code := diagramsDrawn
	for _, file := range files {
		diagrams, err := diagramsIn(file)
		if err != nil {
			fmt.Fprintf(out, "%s: %v\n", file, err)
			code = diagramsNotDrawn
			continue
		}
		if len(diagrams) == 0 {
			fmt.Fprintf(out, "%s: no mermaid or vega-lite diagrams\n", file)
			continue
		}
		for _, d := range diagrams {
			if err := layDiagram(d.lang, d.src); err != nil {
				fmt.Fprintf(out, "%s:%d: not drawn: %v\n", d.file, d.line+errorLine(err), err)
				code = diagramsNotDrawn
				continue
			}
			fmt.Fprintf(out, "%s:%d: ok (%s)\n", d.file, d.line, d.lang)
		}
	}
	return code
}

// errorLine is how far into its source golib's error is: a Mermaid syntax error's line, less one (the
// diagram's first line is its file's line already); 0 for an error with no line.
func errorLine(err error) int {
	var se *pm.SyntaxError
	if errors.As(err, &se) && se.Line > 0 {
		return se.Line - 1
	}
	return 0
}
