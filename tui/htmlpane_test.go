package tui

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/decl"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/decltest"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/htmlview"
)

// htmlpane_test.go: File › Preview HTML (htmlpane.go, htmlpane_cells.go) — the note's page in the
// pane beside the editor, live and following the cursor, its links opening notes, files and the
// browser: in a terminal, golib's cell HTMLView, the note page's, its diagrams drawn in cells.

// longNote is a note of n paragraphs after a heading, linking to b.
func longNote(n int) string {
	var sb strings.Builder
	sb.WriteString("# Heading\n\nsee [[b]] and [the web](https://example.com)\n\n")
	for i := range n {
		sb.WriteString("paragraph " + string(rune('a'+i%26)) + " of the note\n\n")
	}
	return sb.String()
}

// runPane is the TUI on a managed daemon whose kb holds a.md (a long note) and b.md, in a terminal,
// with a.md open; it answers the TUI and the kb's root.
func runPane(t *testing.T) (*running, string) {
	t.Helper()
	r, kb, _, _ := runOutside(t)
	if err := os.WriteFile(filepath.Join(kb, "a.md"), []byte(longNote(40)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kb, "b.md"), []byte("# B\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "a.md")) })
	r.waitOpen(t, "kb", "a.md")
	return r, kb
}

func (r *running) paneSource() string {
	return onLoop(r, func() string {
		v, ok := r.h.htmlView()
		if !ok {
			return ""
		}
		return string(v.Source())
	})
}

// TestPreviewHTMLOpensThePane: in a terminal, Preview HTML opens the pane on the note's page, its
// blocks carrying their source bytes; an edit reaches it after the quiet; the cursor's block comes
// to its top.
func TestPreviewHTMLOpensThePane(t *testing.T) {
	t.Parallel()
	r, _ := runPane(t)
	if onLoop(r, r.h.native) {
		t.Fatal("the test backend draws native views")
	}
	r.h.p.Post(r.h.previewHTML)
	r.s.WaitFor(t, "the pane open on the page", func(string) bool {
		return onLoop(r, r.h.htmlPaneShown) && strings.Contains(r.paneSource(), `<h1 id="heading" data-src="0-9">Heading</h1>`)
	})
	r.s.WaitForText(t, r.paneCorner())

	r.typeInEditor(t, "fresh ")
	r.s.WaitFor(t, "the edit in the pane", func(string) bool { return strings.Contains(r.paneSource(), "fresh ") })

	var top float32
	r.h.p.Post(func() {
		r.h.core.SetLine(len(r.h.core.Lines())-2, 0)
		r.h.cursorMoved()
	})
	r.s.WaitFor(t, "the pane at the cursor's block", func(string) bool {
		top = onLoop(r, func() float32 { v, _ := r.h.htmlView(); return v.ScrollY() })
		return top > 0
	})
}

// TestImagePreviewsLeavePreviewHTMLThePane: where the terminal could show an image (graphics
// confirmed, a browser to render with, View › Image previews on) and where it couldn't (off),
// Preview HTML opens the pane: no image is placed and no browser opened. Image previews govern
// View › Diagram alone.
func TestImagePreviewsLeavePreviewHTMLThePane(t *testing.T) {
	t.Parallel()
	r, _ := runPane(t)
	opened := make(chan string, 1)
	onLoop(r, func() bool {
		r.h.graphicsOverride = func() tuicore.Tri { return tuicore.TriYes }
		r.h.rasterizeOverride = func(_ context.Context, _ []byte, p htmlview.Page) ([]byte, error) {
			return tallPage(t, p.Width, p.MinHeight), nil
		}
		r.h.browser = func(_ context.Context, p string) error { opened <- p; return nil }
		return true
	})
	for _, images := range []bool{true, false} {
		if ok := onLoop(r, func() bool { ok, _ := r.h.imageMode(); return ok }); ok != images {
			t.Fatalf("image mode %v, want %v", ok, images)
		}
		r.h.p.Post(r.h.previewHTML)
		r.s.WaitFor(t, "the pane open", func(string) bool { return onLoop(r, r.h.htmlPaneShown) && r.paneSource() != "" })
		time.Sleep(200 * time.Millisecond) // an image or a browser would be under way by now
		if n := len(r.s.Backend.Images()); n != 0 {
			t.Fatalf("image previews %v: %d image(s) placed", images, n)
		}
		select {
		case p := <-opened:
			t.Fatalf("image previews %v: the browser was given %q", images, p)
		default:
		}
		r.h.p.Post(func() { r.h.closeHTMLPane(); r.h.toggleImagePreviews() })
		r.s.WaitFor(t, "the pane closed", func(string) bool { return !onLoop(r, r.h.htmlPaneShown) })
	}
}

// TestALinkClickedInThePaneOpens: a click on the page's web link, in the pane's cells, hands it to
// the browser; a click on its wikilink opens the note it names.
func TestALinkClickedInThePaneOpens(t *testing.T) {
	t.Parallel()
	r, _ := runPane(t)
	browsed := make(chan string, 1)
	r.h.p.Post(func() { r.h.browser = func(_ context.Context, p string) error { browsed <- p; return nil } })
	r.h.p.Post(r.h.previewHTML)
	r.s.WaitFor(t, "the links in the pane", func(string) bool { _, _, ok := r.inPane("see b and the web"); return ok })
	r.clickInPane(t, "the web")
	select {
	case p := <-browsed:
		if p != "https://example.com" {
			t.Fatalf("the browser was given %q", p)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the web link's click opened nothing")
	}
	r.clickInPane(t, "b and the web")
	r.waitOpen(t, "kb", "b.md")
}

// TestThePaneReadsInSepiaAndDark: in sepia, then in dark switched to with the pane open, the pane
// draws the note's Mermaid diagram in cells (its boxes and labels, not the notice) and its code in
// the theme's syntax colours: its go keyword in the colour the editor's page gives its own, a
// colour apart from the function after it and from the pane's paper. The pane's rows are logged,
// each theme's, for review.
func TestThePaneReadsInSepiaAndDark(t *testing.T) {
	t.Parallel()
	note := "# Title\n\n```go\nfunc main() {}\n```\n\n```mermaid\nflowchart LR\n  A[Start] --> B[Finish]\n```\n"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", note}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 140, 40)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.switchTheme("sepia"); return true })
	r.s.WaitFor(t, "sepia", func(string) bool { return onLoop(r, func() string { return r.h.theme }) == "sepia" })
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.h.p.Post(r.h.previewHTML)
	var kws []tuicore.CellColor
	for _, theme := range []string{"sepia", "dark"} {
		if theme != "sepia" {
			onLoop(r, func() bool { r.h.switchTheme(theme); return true })
			r.s.WaitFor(t, theme, func(string) bool { return onLoop(r, func() string { return r.h.theme }) == theme })
		}
		// the pane drawn in this theme: its keyword in the editor's page's colour for one
		var kw tuicore.CellColor
		r.s.WaitFor(t, theme+": the pane's code and diagram", func(string) bool {
			snap := r.s.Backend.Snapshot()
			px, py, ok := r.inPane("func main()")
			ex, ey, eok := r.leftOfPane("func main()") // the editor's page: the pane may draw it a row higher
			if !ok || !eok || !r.paneHas("Start", "Finish") {
				return false
			}
			kw = snap[py][px].Attrs.FG
			return kw == snap[ey][ex].Attrs.FG && (len(kws) == 0 || kw != kws[0])
		})
		snap := r.s.Backend.Snapshot()
		px, py, _ := r.inPane("func main()")
		if fn := snap[py][px+5].Attrs.FG; fn == kw {
			t.Fatalf("%s: the pane's keyword and the function after it share one colour %v", theme, kw)
		}
		if bg := snap[py][px].Attrs.BG; bg == kw {
			t.Fatalf("%s: the pane's keyword is its paper's colour %v", theme, kw)
		}
		if _, _, ok := r.inPane("not drawn in the terminal"); ok {
			t.Fatalf("%s: the pane says its diagram is not drawn:\n%s", theme, r.s)
		}
		kws = append(kws, kw)
		t.Logf("%s: the pane:\n%s", theme, r.paneText())
	}
}

// paneCorner is the pane's frame's top-left corner and heading as drawn in the UI's language now
// ("┌ preview" in English): where every pane helper finds the pane, so they find it after a
// language switch too.
func (r *running) paneCorner() string {
	return "┌ " + onLoop(r, func() string { return r.h.tr("autodoc.main.preview") })
}

// paneHas reports whether the pane's cells show each of texts inside a box: a row holding the text
// has a box's side (│) before it, within the pane.
func (r *running) paneHas(texts ...string) bool {
	snap := r.s.Backend.Snapshot()
	left, _, ok := find(snap, r.paneCorner())
	if !ok {
		return false
	}
	for _, text := range texts {
		x, y, ok := r.inPane(text)
		if !ok {
			return false
		}
		side := false
		for _, c := range snap[y][left+1 : x] {
			side = side || c.Content == "│"
		}
		if !side {
			return false
		}
	}
	return true
}

// paneText is the pane's cells as text, a line per row, right of its border's corner.
func (r *running) paneText() string {
	snap := r.s.Backend.Snapshot()
	left, _, ok := find(snap, r.paneCorner())
	if !ok {
		return ""
	}
	var sb strings.Builder
	for _, row := range snap {
		for _, c := range row[min(left, len(row)):] {
			sb.WriteString(c.Content)
		}
		sb.WriteString("\n")
	}
	return sb.String()
}

// leftOfPane finds text in the cells left of the pane's border: the editor's.
func (r *running) leftOfPane(text string) (int, int, bool) {
	snap := r.s.Backend.Snapshot()
	left, _, ok := find(snap, r.paneCorner())
	if !ok {
		return 0, 0, false
	}
	beside := make([][]tuicore.Cell, len(snap))
	for y, row := range snap {
		beside[y] = row[:min(left, len(row))]
	}
	return find(beside, text)
}

// inPane finds text in the pane's cells (right of its border's corner: the editor beside it may
// show the same page), answering the screen cell it starts at.
func (r *running) inPane(text string) (int, int, bool) {
	snap := r.s.Backend.Snapshot()
	left, _, ok := find(snap, r.paneCorner())
	if !ok {
		return 0, 0, false
	}
	pane := make([][]tuicore.Cell, len(snap))
	for y, row := range snap {
		if left+1 < len(row) {
			pane[y] = row[left+1:]
		}
	}
	x, y, ok := find(pane, text)
	return x + left + 1, y, ok
}

// clickInPane clicks the pane's cells where text begins.
func (r *running) clickInPane(t *testing.T, text string) {
	t.Helper()
	x, y, ok := r.inPane(text)
	if !ok {
		t.Fatalf("%q is not in the pane:\n%s", text, r.s)
	}
	r.keys(t, tuicore.MouseEvent{Kind: tuicore.MousePress, Button: tuicore.MouseLeft, X: x, Y: y},
		tuicore.MouseEvent{Kind: tuicore.MouseRelease, Button: tuicore.MouseLeft, X: x, Y: y})
}

// TestThePanesLinks: a page's link opens the note it names (a wikilink's page), a file: URL opens
// that file, and a web address goes to the browser.
func TestThePanesLinks(t *testing.T) {
	t.Parallel()
	r, kb := runPane(t)
	browsed := make(chan string, 1)
	r.h.p.Post(func() {
		r.h.browser = func(_ context.Context, p string) error { browsed <- p; return nil }
	})
	r.h.p.Post(func() { r.h.htmlLink("b") })
	r.waitOpen(t, "kb", "b.md")
	r.h.p.Post(func() { r.h.htmlLink("https://example.com") })
	if got := <-browsed; got != "https://example.com" {
		t.Errorf("the browser was given %q", got)
	}
	r.h.p.Post(func() { r.h.htmlLink("file://" + filepath.ToSlash(filepath.Join(kb, "a.md"))) })
	r.waitOpen(t, "kb", "a.md")
}

// TestThePaneFollowsTheOpenNote: with the pane open, another note opened shows its page at once
// (no typing reports it), a render of the old note still on its way never lands over it, the draft
// after a close shows an empty page, and a file that is not Markdown closes the pane.
func TestThePaneFollowsTheOpenNote(t *testing.T) {
	t.Parallel()
	r, kb := runPane(t)
	if err := os.WriteFile(filepath.Join(kb, "c.yaml"), []byte("k: v\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(r.h.previewHTML)
	r.s.WaitFor(t, "a's page", func(string) bool { return strings.Contains(r.paneSource(), ">Heading</h1>") })

	// a's render on its way when b opens
	r.h.p.Post(func() {
		r.h.renderHTMLPane()
		r.h.openAbsolute(filepath.Join(kb, "b.md"))
	})
	r.waitOpen(t, "kb", "b.md")
	r.s.WaitFor(t, "b's page", func(string) bool {
		s := r.paneSource()
		return strings.Contains(s, ">B</h1>") && !strings.Contains(s, "Heading")
	})
	time.Sleep(2 * editPause) // anything of a's still landing would have by now
	if s := r.paneSource(); !strings.Contains(s, ">B</h1>") {
		t.Fatalf("a's page landed over b's: %q", s)
	}

	r.h.p.Post(r.h.closeFile)
	r.s.WaitFor(t, "the draft's empty page", func(string) bool {
		s := r.paneSource()
		return s != "" && !strings.Contains(s, ">B</h1>")
	})
	if !onLoop(r, r.h.htmlPaneShown) {
		t.Fatal("closing the note closed the pane")
	}

	r.h.p.Post(func() { r.h.openAbsolute(filepath.Join(kb, "c.yaml")) })
	r.waitOpen(t, "kb", "c.yaml")
	r.s.WaitFor(t, "the pane closed on YAML", func(string) bool { return !onLoop(r, r.h.htmlPaneShown) })
}

// TestThePaneTakesTheKeyboardByCtrlL: the pane is at the right edge, so Ctrl+l from the page in
// Normal mode gives it the keyboard and Ctrl+h gives it back.
func TestThePaneTakesTheKeyboardByCtrlL(t *testing.T) {
	t.Parallel()
	r, _ := runPane(t)
	r.h.p.Post(r.h.previewHTML)
	r.s.WaitFor(t, "the pane open", func(string) bool { return onLoop(r, r.h.htmlPaneShown) })
	r.s.WaitFor(t, "the page has the keyboard", func(string) bool { return r.focused("editor") })
	r.keys(t, decltest.Ctrl('l'))
	r.s.WaitFor(t, "to the pane", func(string) bool { return r.focused("htmlView") })
	r.keys(t, decltest.Ctrl('h'))
	r.s.WaitFor(t, "back to the page", func(string) bool { return r.focused("editor") })
}

var _ = htmlview.DirImages

// TestThePaneOpensItsPageInTheBrowser: under its page the pane has the terminal preview's buttons,
// Open in browser and Close (q); Open in browser writes the note's page and opens it in the browser.
func TestThePaneOpensItsPageInTheBrowser(t *testing.T) {
	t.Parallel()
	r, _ := runPane(t)
	opened := make(chan string, 1)
	r.h.p.Post(func() { r.h.browser = func(_ context.Context, path string) error { opened <- path; return nil } })
	r.h.p.Post(func() { r.h.previewHTML() })
	r.s.WaitFor(t, "the pane's page", func(string) bool { return r.paneSource() != "" })
	labels := onLoop(r, func() []string {
		var out []string
		for _, b := range buttonsUnder(r.h.p, "htmlPane") {
			out = append(out, b.Label())
		}
		return out
	})
	if len(labels) != 2 || !strings.Contains(labels[0], "browser") || !strings.Contains(labels[1], "Close") {
		t.Fatalf("the pane's buttons are %q, want Open in browser and Close (q)", labels)
	}
	if !onLoop(r, func() bool { return buttonsUnder(r.h.p, "htmlPane")[0].Activate(tuicore.OriginUser) }) {
		t.Fatal("Open in browser did not press")
	}
	select {
	case path := <-opened:
		page, err := os.ReadFile(path)
		if err != nil || !strings.HasSuffix(path, ".html") || !strings.Contains(string(page), "paragraph a of the note") {
			t.Fatalf("opened %q (%v), want the note's page", path, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the browser was not opened")
	}
}

// TestThePagesChartsAreDrawnInTheBrowserAndKeptInThePane: a note's vega-lite block stays its spec in
// the pane, whose HTMLView draws it as a chart in cells, while Open in browser writes the page with
// the chart drawn as SVG by the host's Chart, as --export does. With no Chart, the browser's page
// keeps the spec.
func TestThePagesChartsAreDrawnInTheBrowserAndKeptInThePane(t *testing.T) {
	t.Parallel()
	const note = "# Fruit\n\n```vega-lite\n" + `{"mark":"bar","data":{"values":[{"k":"Apples","v":3}]},` +
		`"encoding":{"x":{"field":"k","type":"nominal"},"y":{"field":"v","type":"quantitative"}}}` + "\n```\n"
	const drawn = `<svg data-sample="drawn chart"></svg>`
	for _, tc := range []struct {
		name  string
		chart func([]byte, string) (string, error)
		want  string
	}{
		{"with a Chart", func([]byte, string) (string, error) { return drawn, nil }, drawn},
		{"without one", nil, `<pre class="vega-lite"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, kb, _, _ := runOutside(t)
			if err := os.WriteFile(filepath.Join(kb, "chart.md"), []byte(note), 0o600); err != nil {
				t.Fatal(err)
			}
			opened := make(chan string, 1)
			r.h.p.Post(func() {
				r.h.chart = tc.chart
				r.h.browser = func(_ context.Context, path string) error { opened <- path; return nil }
				r.h.openAbsolute(filepath.Join(kb, "chart.md"))
			})
			r.waitOpen(t, "kb", "chart.md")
			r.h.p.Post(func() { r.h.previewHTML() })
			r.s.WaitFor(t, "the pane's page", func(string) bool { return r.paneSource() != "" })
			if src := r.paneSource(); !strings.Contains(src, `<pre class="vega-lite"`) || strings.Contains(src, drawn) {
				t.Fatalf("the pane's page is %.300q, want the vega-lite spec its HTMLView draws, not the SVG", src)
			}
			if !onLoop(r, func() bool { return buttonsUnder(r.h.p, "htmlPane")[0].Activate(tuicore.OriginUser) }) {
				t.Fatal("Open in browser did not press")
			}
			select {
			case path := <-opened:
				page, err := os.ReadFile(path)
				if err != nil || !strings.Contains(string(page), tc.want) {
					t.Fatalf("the browser's page (%v) lacks %q:\n%.600s", err, tc.want, page)
				}
				if tc.chart != nil && strings.Contains(string(page), `<pre class="vega-lite"`) {
					t.Fatalf("the browser's page keeps the spec beside the drawn chart:\n%.600s", page)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the browser was not opened")
			}
		})
	}
}

// TestThePanesCloseClosesIt: Close (q) under the pane's page closes the pane.
func TestThePanesCloseClosesIt(t *testing.T) {
	t.Parallel()
	r, _ := runPane(t)
	r.h.p.Post(func() { r.h.previewHTML() })
	r.s.WaitFor(t, "the pane open", func(string) bool { return onLoop(r, func() bool { return r.h.panelOpen["htmlPane"] }) })
	if !onLoop(r, func() bool {
		bs := buttonsUnder(r.h.p, "htmlPane")
		return len(bs) == 2 && bs[1].Activate(tuicore.OriginUser)
	}) {
		t.Fatal("Close did not press")
	}
	r.s.WaitFor(t, "the pane closed", func(string) bool { return onLoop(r, func() bool { return !r.h.panelOpen["htmlPane"] }) })
}

// buttonsUnder are the Buttons under the node id names, in order.
func buttonsUnder(p *tuidecl.Program, id string) []*widgetcore.Button {
	root, ok := p.Tree().NodeByID(id)
	if !ok {
		return nil
	}
	var out []*widgetcore.Button
	queue := []decl.NodeID{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if c, ok := p.Adapter().Component(n); ok {
			if b, ok := c.(*widgetcore.Button); ok {
				out = append(out, b)
			}
		}
		queue = append(queue, p.Tree().Children(n)...)
	}
	return out
}

// TestThePanesKeysPressItsButtons: with the keyboard in the pane, b opens its page in the browser
// and q closes it, as its buttons do.
func TestThePanesKeysPressItsButtons(t *testing.T) {
	t.Parallel()
	r, _ := runPane(t)
	opened := make(chan string, 1)
	r.h.p.Post(func() { r.h.browser = func(_ context.Context, path string) error { opened <- path; return nil } })
	r.h.p.Post(func() { r.h.previewHTML() })
	r.s.WaitFor(t, "the pane's page", func(string) bool { return r.paneSource() != "" })
	focus := func() {
		onLoop(r, func() bool { v, _ := r.h.htmlView(); v.Context().RequestFocus(); return true })
	}
	focus()
	r.keys(t, key('b'))
	select {
	case path := <-opened:
		if !strings.HasSuffix(path, ".html") {
			t.Fatalf("b opened %q", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("b in the pane did not open the browser")
	}
	focus()
	r.keys(t, key('q'))
	r.s.WaitFor(t, "q closed the pane", func(string) bool { return onLoop(r, func() bool { return !r.h.panelOpen["htmlPane"] }) })
}

// A chart the pane doesn't draw (a geoshape, outside golib's subset) says so in the page's words
// for a chart, not a diagram's: the notice a note's page and a card's show (pageNotice). After a
// language switch, with the pane open, it says it in the new language.
func TestThePanesUndrawnChartSaysItIsAChart(t *testing.T) {
	t.Parallel()
	note := "# Map\n\n```vega-lite\n{\"data\":{\"values\":[{\"a\":1}]},\"mark\":\"geoshape\"}\n```\n"
	d := startDaemon(t, map[string][]string{"kb": {"a.md", note}})
	r := runTUISized(t, NewSession(d.sock, nil), Options{}, 140, 40)
	r.waitAttached(t, "kb")
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	r.h.p.Post(r.h.previewHTML)
	chart := onLoop(r, func() string { return r.h.pageNotice("vega-lite") })
	diagram := onLoop(r, func() string { return r.h.trf(diagramNoticeID, "vega-lite") })
	head := func(s string) string { // the sentence's start: a narrow pane wraps the rest
		rs := []rune(s)
		return string(rs[:min(len(rs), 16)])
	}
	r.s.WaitFor(t, "the pane's chart notice", func(string) bool { _, _, ok := r.inPane(head(chart)); return ok })
	if _, _, ok := r.inPane(head(diagram)); ok {
		t.Fatalf("the pane says the chart is a diagram:\n%s", r.paneText())
	}
	onLoop(r, func() bool { r.h.setLanguage("ko_KR"); return true })
	ko := ""
	r.s.WaitFor(t, "the chart notice in Korean", func(string) bool {
		ko = onLoop(r, func() string { return r.h.pageNotice("vega-lite") })
		if ko == chart {
			return false // the switch hasn't landed
		}
		_, _, ok := r.inPane(head(ko))
		return ok
	})
	if _, _, ok := r.inPane(head(onLoop(r, func() string { return r.h.trf(diagramNoticeID, "vega-lite") }))); ok {
		t.Fatalf("in Korean, the pane says the chart is a diagram:\n%s", r.paneText())
	}
	x, y, _ := r.inPane(head(ko))
	t.Logf("KOREAN-CHART-NOTICE found in the pane at %d,%d: %q", x, y, head(ko))
	t.Logf("the pane, in Korean:\n%s", r.paneText())
}
