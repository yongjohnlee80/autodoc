package app

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/diagram"
	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/kind"
	pm "github.com/yongjohnlee80/golib/parse/mermaid"
)

// --check-diagram checks diagrams against the GUI's own renderer, with no UI: each file's Mermaid
// diagram or Vega-Lite chart, or a Markdown file's mermaid and vega-lite fences and the .mmd and
// .chart.json files it embeds (![title](flow.mmd), alone on its line), laid out by golib's
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
// language, and its source. An embedded one names its embed too, as written, and why its file was
// not read, if it was not.
type checkDiagram struct {
	file      string
	line      int
	lang, src string
	embed     string
	unread    error
}

// diagramsIn are the diagrams file holds: the whole of a .mmd or .mermaid (Mermaid), the whole of a
// .chart.json or .vl.json (Vega-Lite), and in a .md each mermaid and vega-lite fence (vegalite too)
// and each .mmd and .chart.json file it embeds, in the order of their lines; none for a .md with
// none. Any other file is refused. An embed of anything else (a table, an image) is no diagram.
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
	// the fences are every reader's (diagram.Fences): golib's Markdown parser's, as the export, the
	// TUI and doc.validate find them
	var out []checkDiagram
	for _, f := range diagram.Fences(kind.ParseMarkdown(data)) {
		out = append(out, checkDiagram{file: file, line: f.Line, lang: f.Lang, src: f.Src})
	}
	// the embeds are every reader's too (export.EmbedLines: the page's, the window's, the HTML
	// preview's), each read where the page reads it (export.EmbedDiagramSource)
	for _, e := range export.EmbedLines(data) {
		lang, src, ok, err := export.EmbedDiagramSource(file, e.Dest)
		if ok {
			out = append(out, checkDiagram{file: file, line: e.Line + 1, lang: lang, src: src, embed: e.Dest, unread: err})
		}
	}
	slices.SortStableFunc(out, func(a, b checkDiagram) int { return a.line - b.line })
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
			if !d.report(out) {
				code = diagramsNotDrawn
			}
		}
	}
	return code
}

// report checks d and writes its line: "file:line: ok (mermaid)", "file:line: not drawn: <golib's
// error>" (at the line of the file the error is in), and for an embed the embed after the note's
// line: "note.md:7: flow.mmd: ok (mermaid)", "note.md:7: flow.mmd:3: not drawn: …", "note.md:7:
// flow.mmd: missing file" or "…: not read: <why>". It reports whether d was drawn.
func (d checkDiagram) report(out io.Writer) bool {
	at := fmt.Sprintf("%s:%d: ", d.file, d.line)
	if d.embed != "" {
		at += d.embed + ": "
		switch {
		case errors.Is(d.unread, fs.ErrNotExist):
			fmt.Fprintf(out, "%smissing file\n", at)
			return false
		case d.unread != nil:
			fmt.Fprintf(out, "%snot read: %v\n", at, d.unread)
			return false
		}
	}
	if err := layDiagram(d.lang, d.src); err != nil {
		switch {
		case d.embed == "":
			at = fmt.Sprintf("%s:%d: ", d.file, d.line+errorLine(err))
		case errorLine(err) > 0:
			at = fmt.Sprintf("%s:%d: %s:%d: ", d.file, d.line, d.embed, 1+errorLine(err))
		}
		fmt.Fprintf(out, "%snot drawn: %v\n", at, err)
		return false
	}
	fmt.Fprintf(out, "%sok (%s)\n", at, d.lang)
	return true
}

// errorLine is how far into its source golib's error is: the line of a Mermaid syntax error, or of
// what its parser refused, less one (the diagram's first line is its file's line already); 0 for an
// error with no line.
func errorLine(err error) int {
	var se *pm.SyntaxError
	var nd *pm.NotDrawnError
	switch {
	case errors.As(err, &se) && se.Line > 0:
		return se.Line - 1
	case errors.As(err, &nd) && nd.Line > 0:
		return nd.Line - 1
	}
	return 0
}
