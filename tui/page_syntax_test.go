package tui

import (
	"testing"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// TestACodeFenceOnThePageIsDrawnInItsLanguage: a go fenced block on a note's Rendered page draws
// in the theme's syntax colours — the page's keyword in the same colour the Editor's Raw view
// paints a keyword of its own with (a markdown heading is the markdown highlighter's Keyword),
// one SyntaxStyles colouring both surfaces. The code inside Raw is a string of the markdown
// highlighter, by that highlighter's documented design, so the cell pins the styles the two views
// share: the styles themselves.
func TestACodeFenceOnThePageIsDrawnInItsLanguage(t *testing.T) {
	note := "# Title\n\n```go\nfunc main() {\n\tprintln()\n}\n```\n"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", note}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 40)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")

	// the page: the go keyword coloured, the function after it plain (the theme colours no function)
	r.s.WaitFor(t, "the page's go drawn", func(string) bool {
		_, _, ok := find(r.s.Backend.Snapshot(), "func main()")
		return ok
	})
	snap := r.s.Backend.Snapshot()
	fx, fy, _ := find(snap, "func main()")
	pageKw := snap[fy][fx].Attrs.FG
	pageFn := snap[fy][fx+5].Attrs.FG
	if pageKw == pageFn {
		t.Fatalf("the page's keyword and the function after it share one colour %v", pageKw)
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
		t.Fatalf("the page's go keyword %v and Raw's markdown keyword %v differ", pageKw, rawKw)
	}
}

// A page made before the program attaches is given no highlighting yet; the first note shown with
// the program attached highlights it. The unit test's construction panics before the fix: the page
// is made with no Program, and giveSyntax read p.h.p anyway.
func TestAPageMadeBeforeTheProgramHighlightsWhenShown(t *testing.T) {
	p := newCellNotePage(newHost(NewSession("unused", nil), Options{}))
	if p.view != nil {
		t.Fatal("a page was made before its first use")
	}
	_ = p
	note := "# Title\n\n```go\nfunc main() {\n\tprintln()\n}\n```\n"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", note}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 40)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the page's go drawn", func(string) bool {
		_, _, ok := find(r.s.Backend.Snapshot(), "func main()")
		return ok
	})
	snap := r.s.Backend.Snapshot()
	fx, fy, _ := find(snap, "func main()")
	pageKw := snap[fy][fx].Attrs.FG
	pageFn := snap[fy][fx+5].Attrs.FG
	if pageKw == pageFn {
		t.Fatalf("the page's keyword and the function after it share one colour %v", pageKw)
	}
}

// A theme switch re-gives the page the theme's syntax colours: the layout reload shows the note
// again, and the page's go keyword takes the new theme's syntax.keyword — still one colour with
// Raw's markdown keywords. Without the re-give, the page keeps the old theme's colour.
func TestASwitchedThemeRecoloursThePagesCode(t *testing.T) {
	note := "# Title\n\n```go\nfunc main() {\n\tprintln()\n}\n```\n"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", note}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 120, 40)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.s.WaitFor(t, "the page's go drawn", func(string) bool {
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
	r.s.WaitFor(t, "the page recoloured", func(string) bool {
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
		t.Fatalf("the page's go keyword %v and Raw's markdown keyword %v differ after the switch", after, rawKw)
	}
}
