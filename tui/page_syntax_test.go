package tui

import (
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// TestACodeFenceInTheRenderedViewIsDrawnInItsLanguage: a go fenced block in a note's Rendered view
// draws in the theme's syntax colours, by the program's languages (the note's renderer is given them
// as the note is shown): its keyword in the same colour the Editor's Raw view paints a keyword of
// its own with (a markdown heading is the markdown highlighter's Keyword), one SyntaxStyles colouring
// both views. The code inside Raw is a string of the markdown highlighter, by that highlighter's
// documented design, so the cell pins the styles the two views share: the styles themselves.
func TestACodeFenceInTheRenderedViewIsDrawnInItsLanguage(t *testing.T) {
	note := "# Title\n\n```go\nfunc main() {\n\tprintln()\n}\n```\n"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", note}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 40)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the note Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	if given := onLoop(r, func() bool {
		p := r.h.notePage()
		return p != nil && p.md != nil && p.md.Languages != nil && p.md.Languages == r.h.p.SourceLanguages()
	}); !given {
		t.Fatal("the note's renderer was not given the program's languages")
	}

	// Rendered: the go keyword coloured, the function after it plain (the theme colours no function)
	r.s.WaitFor(t, "the Rendered go drawn", func(string) bool {
		_, _, ok := find(r.s.Backend.Snapshot(), "func main()")
		return ok
	})
	snap := r.s.Backend.Snapshot()
	fx, fy, _ := find(snap, "func main()")
	pageKw := snap[fy][fx].Attrs.FG
	pageFn := snap[fy][fx+5].Attrs.FG
	if pageKw == pageFn {
		t.Fatalf("the Rendered keyword and the function after it share one colour %v", pageKw)
	}

	// Raw: the markdown heading is the same keyword style — the two views wear one SyntaxStyles
	r.keys(t, ctrlKey('t'))
	r.s.WaitFor(t, "Raw drawn", func(string) bool {
		_, _, ok := find(r.s.Backend.Snapshot(), "```go")
		return ok
	})
	snap = r.s.Backend.Snapshot()
	hx, hy, _ := find(snap, "# Title")
	if rawKw := snap[hy][hx].Attrs.FG; rawKw != pageKw {
		t.Fatalf("the Rendered go keyword %v and Raw's markdown keyword %v differ", pageKw, rawKw)
	}
}

// TestAnHTMLPageMadeBeforeTheProgramTakesNoHighlightingYet: an HTML file's page made before the
// program attaches is given no highlighting, and a theme or language change then gives it none
// either, without failing: giveSyntax reads the Program only once there is one.
func TestAnHTMLPageMadeBeforeTheProgramTakesNoHighlightingYet(t *testing.T) {
	p := newCellNotePage(newHost(NewSession("unused", nil), Options{}))
	if p.view != nil {
		t.Fatal("a page was made before its first use")
	}
	if p.viewOf() == nil {
		t.Fatal("no page made on first use")
	}
	p.ThemeChanged()
	p.LanguageChanged()
}

// A theme switch recolours a note's Rendered code: the layout reload draws the note again, and its
// go keyword takes the new theme's syntax.keyword — still one colour with Raw's markdown keywords.
func TestASwitchedThemeRecoloursTheRenderedCode(t *testing.T) {
	note := "# Title\n\n```go\nfunc main() {\n\tprintln()\n}\n```\n"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", note}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 40)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the note Rendered by the editor", func(string) bool { return renderedByTheEditor(r) })
	r.s.WaitFor(t, "the Rendered go drawn", func(string) bool {
		_, _, ok := find(r.s.Backend.Snapshot(), "func main()")
		return ok
	})
	kw := func() tuicore.CellColor {
		snap := r.s.Backend.Snapshot()
		x, y, _ := find(snap, "func main()")
		return snap[y][x].Attrs.FG
	}
	before := kw()
	to := "dark"
	if r.h.theme == to {
		to = "sepia"
	}
	onLoop(r, func() bool { r.h.switchTheme(to); return true })
	r.s.WaitFor(t, "the Rendered code recoloured", func(string) bool {
		return kw() != before
	})
	after := kw()
	r.keys(t, ctrlKey('t'))
	r.s.WaitFor(t, "Raw drawn", func(string) bool {
		_, _, ok := find(r.s.Backend.Snapshot(), "```go")
		return ok
	})
	snap := r.s.Backend.Snapshot()
	hx, hy, _ := find(snap, "# Title")
	if rawKw := snap[hy][hx].Attrs.FG; rawKw != after {
		t.Fatalf("the Rendered go keyword %v and Raw's markdown keyword %v differ after the switch", after, rawKw)
	}
}
