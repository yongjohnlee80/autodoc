package app

import (
	"fmt"
	"io"
	"slices"
	"strings"
)

// --diagram-reference prints the diagrams and charts AutoDoc's GUI draws, read from golib's own
// tables (gui/mermaid's Kinds, gui/vegalite's Subset), so it can't fall behind what the Rendered
// view draws. An agent reads it before writing a diagram, then checks the diagram with
// --check-diagram.

// diagramSubset is what golib draws: the Mermaid kinds, each with its header keywords, and the
// Vega-Lite subset. The GUI build's (diagramref_on.go); nil without it.
var diagramSubset func() drawnSubset

// drawnSubset is golib's answer, in AutoDoc's words.
type drawnSubset struct {
	kinds                                         []drawnKind
	marks, channels, fieldTypes, scaleTypes, aggs []string
	layers                                        bool
	maxRows, maxBands, maxSeries                  int
	passed                                        map[string][]string
}

// drawnKind is a Mermaid kind drawn, and the header keywords that start one.
type drawnKind struct {
	name     string
	keywords []string
}

// diagramReference is --diagram-reference: the reference, and the exit code. A build without the
// GUI has no golib/gui to read, and exits diagramsNoGUI, as --check-diagram does.
func diagramReference(out io.Writer) int {
	if diagramSubset == nil {
		fmt.Fprintln(out, "autodoc --diagram-reference: this build draws no diagrams: it needs the GUI build (golib/gui), as --gui does")
		return diagramsNoGUI
	}
	writeDiagramReference(out, diagramSubset())
	return 0
}

// writeDiagramReference writes s as Markdown.
func writeDiagramReference(out io.Writer, s drawnSubset) {
	code := func(names []string) string {
		quoted := make([]string, len(names))
		for i, n := range names {
			quoted[i] = "`" + n + "`"
		}
		return strings.Join(quoted, ", ")
	}
	w := func(format string, a ...any) { fmt.Fprintf(out, format, a...) }
	w("# Diagrams and charts AutoDoc's GUI draws\n\n")
	w("Read from golib (`gui/mermaid` and `gui/vegalite`), the renderer of `autodoc --gui`'s Rendered view, so it is what this build draws.\n")
	w("An export (`--export html`, a site) draws Mermaid with mermaid.js instead, which draws more than this.\n")
	w("Check a diagram before you show it: `autodoc --check-diagram <file>...` prints ok, or golib's reason.\n\n")

	w("## Mermaid\n\n")
	w("The diagram kinds drawn, and the first word that starts each:\n\n")
	for _, k := range s.kinds {
		w("- %s: %s\n", k.name, code(k.keywords))
	}
	w("\nA diagram of any other kind (`pie`, `gantt`, …) is not drawn: the Rendered view shows golib's reason in its place. ")
	w("Within a kind, golib draws a subset of Mermaid's syntax, and `--check-diagram` names what it refuses.\n\n")

	w("## Vega-Lite\n\n")
	w("- Marks: %s (`circle` is a filled point; a `rule` is drawn over another mark, not alone)\n", code(s.marks))
	w("- Channels: %s (`color` and `fill` are one channel by two names)\n", code(s.channels))
	w("- Field types: %s\n", code(s.fieldTypes))
	w("- Scale types: %s\n", code(s.scaleTypes))
	w("- Aggregates: %s (`average` is `mean`)\n", code(s.aggs))
	if s.layers {
		w("- Layers: drawn, each a mark of its own over shared scales\n")
	}
	w("- Limits: %d rows of data, %d categories on a discrete axis, %d colour series\n\n", s.maxRows, s.maxBands, s.maxSeries)
	w("The other keys taken, by where they sit. Most change only how a chart looks or behaves in a browser, and are ignored; ")
	w("a title's text and subtitle, and a selection's type, are read.\n\n")
	places := make([]string, 0, len(s.passed))
	for p := range s.passed {
		places = append(places, p)
	}
	slices.Sort(places)
	for _, p := range places {
		w("- %s: %s\n", p, code(s.passed[p]))
	}
	w("\nAnything else that changes what a chart says (a transform, a log scale, a binned or time-unit field, another channel) is not drawn: ")
	w("the Rendered view shows golib's reason in its place, and an export writes it above the chart's spec.\n")
}
