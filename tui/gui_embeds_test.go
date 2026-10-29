//go:build gui

package tui

import (
	"context"
	"errors"
	"github.com/yongjohnlee80/golib/parse/mermaid"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/gui"
	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	tuiwidget "github.com/yongjohnlee80/golib/tui/widget"
)

// diagramHost stands in for the editor's: it records what it is asked and answers with answer.
type diagramHost struct {
	asked  []guiwidget.Block
	src    []string
	lang   []string
	answer guiwidget.DiagramAnswer
}

func (h *diagramHost) Diagram(_ guiwidget.Diagrammer, b guiwidget.Block, req guiwidget.DiagramRequest) guiwidget.DiagramAnswer {
	h.asked, h.src, h.lang = append(h.asked, b), append(h.src, req.Src), append(h.lang, req.Lang)
	return h.answer
}

// embedNote is a note with a diagram beside it and a table in its resources folder, told to a
// NoteEmbeds; and the note's folder.
func embedNote(t *testing.T, more map[string]string) (*NoteEmbeds, string) {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"note.md":       "# Note\n",
		"flow.mmd":      "graph TD\n  a --> b\n",
		"note/data.csv": "name,count\nclaims,\"3,000\"\n",
	}
	for name, text := range more {
		files[name] = text
	}
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n := &NoteEmbeds{}
	n.SetNote(filepath.Join(dir, "note.md"))
	return n, dir
}

var (
	embedShaper = gui.NewTextShaper(1)
	embedTheme  = guiwidget.Theme{Prose: gui.Font{Size: 16}, Mono: gui.Font{Size: 14}}
)

// layOut splits lines into blocks, as the editor does first, and lays out the block at line ln.
func layOut(r guiwidget.Renderer, lines []string, ln int, inside bool, host guiwidget.DiagramHost) (guiwidget.Block, guiwidget.BlockLayout) {
	for _, b := range r.Blocks(lines, 0, len(lines)) {
		if ln >= b.From && ln < b.To {
			return b, r.LayOut(b, lines, 600, inside, embedShaper, embedTheme, host)
		}
	}
	return guiwidget.Block{}, guiwidget.BlockLayout{}
}

// visible is a layout's drawn text, its lines' spans that are not hidden, in order.
func visible(bl guiwidget.BlockLayout) string {
	var b strings.Builder
	for _, ll := range bl.Lines {
		for _, sp := range ll.Spans {
			if !sp.Hidden {
				b.WriteString(sp.Text)
			}
		}
	}
	return b.String()
}

// The note editor draws a line that embeds a diagram or a table, off the cursor, as the file
// written in its place, natively: the diagram asked of the editor's host for the line's own block,
// the table laid out by golib's renderer. Both are the block's picture, so the line keeps its
// source identity and clicks; while typing on it the line is its source, editable.
func TestARenderedNoteDrawsItsEmbeds(t *testing.T) {
	notes, _ := embedNote(t, nil)
	r := notes.Renderer(guiwidget.Diagrams(nil))(NoteRendererSpec{})
	lines := []string{"# Note", "", "![The flow](flow.mmd)", "", "![Data](data.csv)", ""}

	// the diagram: the host's picture, asked for the line's block with the file's source
	pic := gui.ViewFunc(func(gui.Canvas) {})
	host := &diagramHost{answer: guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: pic, Size: gui.Size{W: 200, H: 120}}}
	flow, bl := layOut(r, lines, 2, false, host)
	if flow != (guiwidget.Block{From: 2, To: 3}) || bl.Picture == nil || len(bl.Lines) != 0 || bl.Height != 120 {
		t.Fatalf("the diagram embed: block %v, picture %v, %d lines, height %v; want the host's picture alone", flow, bl.Picture != nil, len(bl.Lines), bl.Height)
	}
	if len(host.asked) != 1 || host.asked[0] != flow || host.src[0] != "graph TD\n  a --> b" {
		t.Fatalf("the host was asked %v for %q, want the line's block for the file's source", host.asked, host.src)
	}

	// still drawing: its source as code, in the picture
	_, bl = layOut(r, lines, 2, false, &diagramHost{answer: guiwidget.DiagramAnswer{State: guiwidget.Pending}})
	if code, ok := bl.Picture.(laidBlock); !ok || len(bl.Lines) != 0 || !strings.Contains(visible(code.bl), "graph TD") {
		t.Fatalf("a pending diagram embed is not its source as code: %#v", bl)
	}

	// the table: golib's table, from the CSV in the note's resources folder, painted as a picture
	_, bl = layOut(r, lines, 4, false, nil)
	table, ok := bl.Picture.(laidBlock)
	if !ok || len(bl.Lines) != 0 || bl.Height <= 0 {
		t.Fatalf("the table embed is not a picture: %#v", bl)
	}
	for _, want := range []string{"name", "count", "claims", "3,000"} {
		if !strings.Contains(visible(table.bl), want) {
			t.Errorf("the table lacks %q: %q", want, visible(table.bl))
		}
	}
	rc := gui.NewRecordingCanvas(gui.Size{W: 600, H: bl.Height}, gui.Size{W: 8, H: 16})
	bl.Picture.Paint(rc)
	if !slices.Contains(rc.Ops(), "DrawText") {
		t.Errorf("the table's picture drew no text: %v", rc.Ops())
	}

	// typing on it, the line is its source
	host = &diagramHost{answer: guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: pic, Size: gui.Size{W: 1, H: 1}}}
	_, bl = layOut(r, lines, 2, true, host)
	if bl.Picture != nil || len(bl.Lines) != 1 || bl.Lines[0].Spans[0].Line != 2 || len(host.asked) != 0 {
		t.Fatalf("under the cursor the embed line is not its source: %#v", bl)
	}
}

// A CSV value is shown as the file has it (Lector #174 r1 F2): emphasis, code, a link, an image,
// an entity, a pipe and a backslash are its own characters, not Markdown.
func TestAnEmbeddedTableShowsItsValuesLiterally(t *testing.T) {
	value := "literal **strong** and `code`, [l](u) ![i](p) &amp; a|b c\\d"
	notes, _ := embedNote(t, map[string]string{"literal.csv": "value\n\"" + value + "\"\n"})
	r := notes.Renderer(nil)(NoteRendererSpec{})
	_, bl := layOut(r, []string{"![data](literal.csv)"}, 0, false, nil)
	table, ok := bl.Picture.(laidBlock)
	if !ok {
		t.Fatalf("no table: %#v", bl)
	}
	if got := visible(table.bl); !strings.Contains(got, value) {
		t.Fatalf("the value changed: shown %q, want %q", got, value)
	}
}

// An image alone in its paragraph is drawn as the page draws it (Lector #174 r1 F3): beside a
// heading with no blank line, with a destination in balanced parentheses, with a title. One that
// shares its line or its paragraph, a missing file, an image, and any with no note open are left
// to golib.
func TestAnEmbedAloneInItsParagraphIsDrawn(t *testing.T) {
	notes, _ := embedNote(t, map[string]string{"flow(v2).mmd": "graph TD\n  v --> two\n"})
	r := notes.Renderer(nil)(NoteRendererSpec{})
	for _, c := range []struct {
		name  string
		lines []string
		line  int
	}{
		{"after a heading", []string{"# Note", "![flow](flow.mmd)"}, 1},
		{"before a heading", []string{"![flow](flow.mmd)", "# Next"}, 0},
		{"balanced parentheses", []string{"![flow](flow(v2).mmd)"}, 0},
		{"a title", []string{`![flow](flow.mmd "The flow")`}, 0},
	} {
		if _, bl := layOut(r, c.lines, c.line, false, nil); bl.Picture == nil {
			t.Errorf("%s: %q was not drawn", c.name, c.lines)
		}
	}
	for _, lines := range [][]string{
		{"See ![The flow](flow.mmd) here."},
		{"![The flow](flow.mmd)", "more of the paragraph"},
		{"more of the paragraph", "![The flow](flow.mmd)"},
		{"![Missing](missing.mmd)"},
		{"![A picture](pic.png)"},
	} {
		if _, bl := layOut(r, lines, 0, false, nil); bl.Picture != nil {
			t.Errorf("%q was drawn as a file", lines)
		}
	}
	notes.SetNote("")
	if _, bl := layOut(r, []string{"![The flow](flow.mmd)"}, 0, false, nil); bl.Picture != nil {
		t.Error("with no note open an embed was drawn")
	}
}

// A paragraph holding an image on a line of its own is one block, laid out line by line as golib
// lays each, so an edit that joins it to the image or parts it from it changes the blocks there.
func TestAParagraphHoldingAnEmbedIsOneBlock(t *testing.T) {
	notes, _ := embedNote(t, nil)
	r := notes.Renderer(nil)(NoteRendererSpec{})
	lines := []string{"# Note", "", "![flow](flow.mmd)", "more text", "", "last"}
	blocks := r.Blocks(lines, 0, len(lines))
	if !slices.Contains(blocks, guiwidget.Block{From: 2, To: 4}) {
		t.Fatalf("the paragraph is not one block: %v", blocks)
	}
	_, bl := layOut(r, lines, 3, false, nil)
	if bl.Picture != nil || len(bl.Lines) != 2 || bl.Lines[0].Spans[0].Line != 2 || bl.Lines[1].Spans[0].Line != 3 || bl.Lines[1].Y <= 0 {
		t.Fatalf("the paragraph is not its two lines, one under the other: %#v", bl)
	}
	if !strings.Contains(visible(bl), "more text") {
		t.Errorf("the paragraph's text is not drawn: %q", visible(bl))
	}
}

// Only the note editor draws embeds (Lector #174 r1 F1): main.qml's document Editor declares
// NoteRenderer, and the search preview, which shows other files, keeps golib's MarkdownRenderer, so
// the window's note renderer is made for the note editor alone.
func TestOnlyTheNoteEditorDrawsEmbeds(t *testing.T) {
	d := startDaemonWith(t, "", map[string][]string{
		"kb": {"a.md", "# A\n\n![flow](flow.mmd)\n", "b.md", "# B\n\n![flow](flow.mmd)\n"},
	}, daemonOpts{})
	notes := &NoteEmbeds{}
	var mu sync.Mutex
	var made []tuidecl.RendererSpec
	factory := notes.Renderer(nil)
	note := guidecl.WithRendererFor(NoteRendererKind, func(s tuidecl.RendererSpec) guiwidget.Renderer {
		mu.Lock()
		made = append(made, s)
		mu.Unlock()
		return factory(s)
	})
	r := runTUI(t, NewSession(d.sock, nil), Options{NoteFile: notes.SetNote,
		ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(guidecl.Native(note))}})
	r.connected(t)
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	onLoop(r, func() bool { r.h.openPath("a.md"); return true })
	r.waitFile(t, "a.md")
	onLoop(r, func() bool { r.h.openSearch(); return true })
	onLoop(r, func() bool { r.h.searchPick.previewItem(hit{path: "b.md"}); return true })
	r.s.WaitFor(t, "b's preview", func(string) bool {
		return onLoop(r, func() bool {
			title, _ := r.h.p.Tree().Source("App.searchPreviewTitle")
			return title.Raw == "b.md"
		})
	})
	mu.Lock()
	defer mu.Unlock()
	if len(made) != 1 {
		t.Fatalf("the note renderer was made %d times, want once, for the note editor", len(made))
	}
	if spec, ok := made[0].(NoteRendererSpec); !ok || !spec.Mermaid {
		t.Errorf("the note renderer was made for %#v, want main.qml's NoteRenderer { mermaid: true }", made[0])
	}
}

// nativeBackend is a test terminal that draws native views, keeping the last frame's.
type nativeBackend struct {
	*tuicore.TestBackend
	mu         sync.Mutex
	placements []tuicore.NativePlacement
}

func (b *nativeBackend) PlaceNatives(ps []tuicore.NativePlacement) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.placements = append([]tuicore.NativePlacement(nil), ps...)
}

// countingRenderer records, by a block's first line, whether its last layout was a picture.
type countingRenderer struct {
	guiwidget.Renderer
	picture map[int]bool
}

func (c *countingRenderer) LayOut(b guiwidget.Block, lines []string, width float32, inside bool, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	bl := c.Renderer.LayOut(b, lines, width, inside, t, th, host)
	c.picture[b.From] = bl.Picture != nil
	return bl
}

// paintedEditor is golib's own editor in the Rendered view on r, holding text, run in an app on a
// terminal that draws native views: onApp runs on its loop, and paint paints its views as a window
// does, which lays out the blocks it shows.
func paintedEditor(t *testing.T, r guiwidget.Renderer, text string) (editor *guiwidget.Editor, onApp func(func()), paint func()) {
	t.Helper()
	editor = guiwidget.NewEditor(guiwidget.WithRenderer(r), guiwidget.WithView(tuiwidget.ViewRendered))
	editor.SetValue(text)
	backend := &nativeBackend{TestBackend: tuicore.NewTestBackend(100, 50, tuicore.WithTestCapabilities(tuicore.Capabilities{NativeViews: true}))}
	app := tuicore.NewApp(editor, tuicore.WithBackend(backend))
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { finished <- app.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-finished })
	deadline := time.Now().Add(3 * time.Second)
	for backend.Flushes() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if backend.Flushes() == 0 {
		t.Fatal("no frame")
	}
	onApp = func(do func()) {
		t.Helper()
		done := make(chan struct{})
		app.Update(func() { do(); close(done) })
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("the loop stalled")
		}
	}
	paint = func() {
		backend.mu.Lock()
		ps := append([]tuicore.NativePlacement(nil), backend.placements...)
		backend.mu.Unlock()
		for _, p := range ps {
			if v, ok := p.View.(gui.View); ok {
				v.Paint(gui.NewRecordingCanvas(gui.Size{W: float32(p.Cols) * 8, H: float32(p.Rows) * 16}, gui.Size{W: 8, H: 16}))
			}
		}
	}
	return editor, onApp, paint
}

// An edit to the line after an embed lays the embed out again (Lector #174 r1 F4), both ways: text
// typed on the blank line under it joins it to a paragraph, and it is drawn as its source; that
// text deleted, it is alone again, and drawn as its file. On golib's own editor, painted natively.
func TestEditingTheLineAfterAnEmbedLaysItOutAgain(t *testing.T) {
	notes, _ := embedNote(t, nil)
	r := &countingRenderer{Renderer: notes.Renderer(nil)(NoteRendererSpec{}), picture: map[int]bool{}}
	editor, onApp, paint := paintedEditor(t, r, "# Note\n\n![data](data.csv)\n\nlast\n")
	onApp(paint)
	onApp(func() {
		if !r.picture[2] {
			t.Fatal("the embed was not drawn as its file to begin with")
		}
		if _, err := editor.Core().Insert(tuiwidget.TextPos{Line: 3, Col: 0}, "continuation"); err != nil {
			t.Fatal(err)
		}
		paint()
		if r.picture[2] {
			t.Error("text typed under the embed: it is still drawn as its file")
		}
		if err := editor.Core().Delete(tuiwidget.TextRange{From: tuiwidget.TextPos{Line: 3, Col: 0}, To: tuiwidget.TextPos{Line: 3, Col: len("continuation")}}); err != nil {
			t.Fatal(err)
		}
		paint()
		if !r.picture[2] {
			t.Error("the text under the embed deleted: it is not drawn as its file again")
		}
	})
}

// An embed by reference (![data][file]) stays as written in the Rendered view (Lector #175 r1 A2):
// its destination is its definition's, which may be anywhere in the note, and the editor keeps a
// line's layout while the lines around it are unchanged. The page still draws it.
func TestAnEmbedByReferenceStaysAsWritten(t *testing.T) {
	notes, _ := embedNote(t, nil)
	r := notes.Renderer(nil)(NoteRendererSpec{})
	for _, lines := range [][]string{
		{"# Note", "", "![data][file]", "", "[file]: note/data.csv"},
		{"# Note", "", "![data][]", "", "[data]: note/data.csv"},
		{"# Note", "", "![data]", "", "[data]: note/data.csv"},
	} {
		if _, bl := layOut(r, lines, 2, false, nil); bl.Picture != nil {
			t.Errorf("%q was drawn as its file", lines[2])
		}
	}
}

// Finding a note's embeds costs what its length does (Lector #175 r1 A1): Blocks on five times the
// paragraphs holding an image allocates about five times as much, not twenty-five.
func TestBlocksScaleWithTheNote(t *testing.T) {
	notes, _ := embedNote(t, nil)
	r := notes.Renderer(nil)(NoteRendererSpec{})
	allocated := func(paragraphs int) uint64 {
		lines := strings.Split(strings.Repeat("![flow](flow.mmd)\ncontinuation\n\n", paragraphs), "\n")
		r.Blocks(lines, 0, len(lines)) // warm
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		blocks := r.Blocks(lines, 0, len(lines))
		runtime.ReadMemStats(&after)
		if len(blocks) != 2*paragraphs+1 {
			t.Fatalf("%d paragraphs: %d blocks, want each paragraph one and its blank", paragraphs, len(blocks))
		}
		return after.TotalAlloc - before.TotalAlloc
	}
	small, large := allocated(1000), allocated(5000)
	if ratio := float64(large) / float64(small); ratio > 10 {
		t.Errorf("5× the paragraphs allocated %.1f× the bytes (%d vs %d): Blocks grows faster than the note", ratio, large, small)
	}
}

// A chart embed (.chart.json) is asked of the host as a vega-lite block, for the line's own block,
// and drawn as its picture; a chart the chain declines (outside golib's subset) is its spec as
// code, in the picture, so the line keeps its identity.
func TestARenderedNoteDrawsItsCharts(t *testing.T) {
	spec := `{"data":{"values":[{"a":"x","b":1}]},"mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`
	notes, _ := embedNote(t, map[string]string{"note/sales.chart.json": spec + "\n"})
	r := notes.Renderer(guiwidget.Diagrams(nil))(NoteRendererSpec{})
	lines := []string{"# Note", "", "![Sales](sales.chart.json)", ""}
	pic := gui.ViewFunc(func(gui.Canvas) {})
	host := &diagramHost{answer: guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: pic, Size: gui.Size{W: 300, H: 200}}}
	b, bl := layOut(r, lines, 2, false, host)
	if bl.Picture == nil || len(bl.Lines) != 0 || bl.Height != 200 {
		t.Fatalf("the chart embed: picture %v, %d lines, height %v; want the host's picture", bl.Picture != nil, len(bl.Lines), bl.Height)
	}
	if len(host.asked) != 1 || host.asked[0] != b || host.lang[0] != "vega-lite" || host.src[0] != spec {
		t.Fatalf("the host was asked %v for %q %q; want the line's block, vega-lite, the file's spec", host.asked, host.lang, host.src)
	}
	_, bl = layOut(r, lines, 2, false, &diagramHost{answer: guiwidget.DiagramAnswer{State: guiwidget.Declined}})
	if code, ok := bl.Picture.(laidBlock); !ok || len(bl.Lines) != 0 || !strings.Contains(visible(code.bl), `"mark":"bar"`) {
		t.Fatalf("a declined chart is not its spec as code: %#v", bl)
	}
}

// A csv fence, off the cursor and closed, is drawn as a .csv embed's table; under the cursor, or
// while its closing fence is not written, it is its source.
func TestACSVFenceIsATable(t *testing.T) {
	notes, _ := embedNote(t, nil)
	r := notes.Renderer(nil)(NoteRendererSpec{})
	lines := []string{"# Note", "", "```csv", "name, age, address", "john, 25, 1234 Howe St.", "```", ""}
	b, bl := layOut(r, lines, 3, false, nil)
	table, ok := bl.Picture.(laidBlock)
	if b != (guiwidget.Block{From: 2, To: 6}) || !ok || len(bl.Lines) != 0 {
		t.Fatalf("block %v: %#v, want the fence's block drawn as a table", b, bl)
	}
	for _, want := range []string{"name", "age", "address", "john", "25", "1234 Howe St."} {
		if !strings.Contains(visible(table.bl), want) {
			t.Errorf("the table lacks %q: %q", want, visible(table.bl))
		}
	}
	if strings.Contains(visible(table.bl), "```") {
		t.Errorf("the fence lines were drawn: %q", visible(table.bl))
	}
	if _, bl = layOut(r, lines, 3, true, nil); bl.Picture != nil || len(bl.Lines) != 4 {
		t.Errorf("under the cursor: %#v, want the fence's four lines", bl)
	}
	open := lines[:5]
	if _, bl = layOut(r, open, 3, false, nil); bl.Picture != nil {
		t.Error("an unclosed csv fence was drawn as a table")
	}
	tilde := []string{"~~~~ CSV", "a,b", "1,2", "~~~~~"}
	if _, bl = layOut(r, tilde, 0, false, nil); bl.Picture == nil {
		t.Error("a tilde fence, CSV in capitals, was not drawn as a table")
	}
	other := []string{"```go", "a,b", "```"}
	if _, bl = layOut(r, other, 0, false, nil); bl.Picture != nil {
		t.Error("a go fence was drawn as a table")
	}
}

// realHost answers as the editor's host does: it asks the Diagrammer it is given, and its ready
// is the test's.
type realHost struct{ ready chan struct{} }

func (h *realHost) Diagram(d guiwidget.Diagrammer, _ guiwidget.Block, req guiwidget.DiagramRequest) guiwidget.DiagramAnswer {
	return d.Diagram(context.Background(), req, func() {
		select {
		case h.ready <- struct{}{}:
		default:
		}
	})
}

// End to end, with golib's own chain: a chart embed is drawn as a chart, laid out by
// gui/vegalite. It is Pending first, then, once laid, a picture that paints its labels; a spec
// outside golib's subset is a notice of a few lines, not its JSON; a spec that is not JSON stays
// code, to be fixed.
func TestAChartEmbedIsDrawnByGolib(t *testing.T) {
	spec := `{"data":{"values":[{"a":"apples","b":3},{"a":"pears","b":5}]},"mark":"bar","encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`
	notes, _ := embedNote(t, map[string]string{"note/fruit.chart.json": spec, "note/other.chart.json": `{"data":{"values":[]},"mark":"bar","transform":[]}`,
		"note/broken.chart.json": `{"mark": "bar",`})
	r := noteRenderer(notes)(NoteRendererSpec{}) // the renderer GUIStyle installs
	lines := []string{"![Fruit](fruit.chart.json)", "", "![Other](other.chart.json)", "", "![Broken](broken.chart.json)"}
	draw := func(ln int) guiwidget.BlockLayout {
		host := &realHost{ready: make(chan struct{}, 1)}
		layOut(r, lines, ln, false, host) // starts the chain: its first layout is the placeholder
		select {
		case <-host.ready:
		case <-time.After(10 * time.Second):
			t.Fatalf("line %d: the chain never called ready", ln)
		}
		_, bl := layOut(r, lines, ln, false, host)
		return bl
	}
	bl := draw(0)
	if bl.Picture == nil || bl.Height <= 0 {
		t.Fatalf("the chart: %#v, want its picture", bl)
	}
	if _, code := bl.Picture.(laidBlock); code {
		t.Fatalf("the chart was drawn as its source: %q", visible(bl.Picture.(laidBlock).bl))
	}
	rc := gui.NewRecordingCanvas(gui.Size{W: 600, H: bl.Height}, gui.Size{W: 8, H: 16})
	bl.Picture.Paint(rc)
	if !slices.Contains(rc.Ops(), "DrawText") || !slices.Contains(rc.Ops(), "FillPath") {
		t.Errorf("the chart painted %v: want its bars and labels", rc.Ops())
	}
	bl = draw(2)
	if n, ok := bl.Picture.(noticeView); !ok || !strings.HasPrefix(n.text, "chart not drawn: ") || !strings.Contains(n.text, "a spec outside the subset drawn") {
		t.Fatalf("a chart outside the subset is not the notice of golib's reason: %#v", bl)
	}
	if lh := max(embedTheme.Prose.Size, 10) * 1.4; bl.Height <= lh || bl.Height > 5*lh {
		t.Errorf("the notice is %v high, want a few lines (a line is %v)", bl.Height, lh)
	}
	rc = gui.NewRecordingCanvas(gui.Size{W: 600, H: bl.Height}, gui.Size{W: 8, H: 16})
	bl.Picture.Paint(rc)
	if !slices.Contains(rc.Ops(), "DrawText") {
		t.Errorf("the notice painted %v: want its text", rc.Ops())
	}
	bl = draw(4)
	if code, ok := bl.Picture.(laidBlock); !ok || !strings.Contains(visible(code.bl), `"mark"`) {
		t.Errorf("a chart that is not JSON is not its source as code: %#v", bl)
	}
}

// A chart's notice is drawn on the UI loop, in a layout, so it never waits for a pooled measurer:
// with as many measurers held at its scale as the pool keeps (golib's diagram workers, a note of
// several charts laying out), the notice is still answered at once.
func TestTheChartNoticeNeverWaitsForAMeasurer(t *testing.T) {
	const held = 4 // what the pool lends per scale (golib gui's measurerMax): a fifth would wait here
	var releases []func()
	for range held {
		_, release := gui.AcquireMeasurer(1.75)
		releases = append(releases, release)
	}
	defer func() {
		for _, release := range releases {
			release()
		}
	}()
	done := make(chan guiwidget.DiagramAnswer, 1)
	go func() {
		done <- chartNotice{}.Diagram(context.Background(), guiwidget.DiagramRequest{Lang: "vega-lite", Width: 400, Scale: 1.75}, func() {})
	}()
	select {
	case a := <-done:
		if a.State != guiwidget.Ready {
			t.Errorf("the notice: %+v", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the notice waited for a measurer the diagram workers held")
	}
}

// The notice says why in golib's words when golib gave a reason (the error it declined the chart
// with), and the general notice when it gave none.
func TestTheChartNoticeSaysGolibsReason(t *testing.T) {
	why := errors.New("vegalite: a spec outside the subset drawn: a rule coloured by a field")
	a := chartNotice{}.Diagram(context.Background(), guiwidget.DiagramRequest{Lang: "vega-lite", Width: 400, Scale: 1, Declined: why}, func() {})
	if n, ok := a.Pic.(noticeView); a.State != guiwidget.Ready || !ok ||
		n.text != "chart not drawn: vegalite: a spec outside the subset drawn: a rule coloured by a field. Its source is in the Raw view, or in its .chart.json file." {
		t.Errorf("a chart golib declined with a reason: %+v", a)
	}
	a = chartNotice{}.Diagram(context.Background(), guiwidget.DiagramRequest{Lang: "vega-lite", Width: 400, Scale: 1}, func() {})
	if n, ok := a.Pic.(noticeView); !ok || n.text != englishText(chartNoticeUnsupported) || !strings.HasPrefix(n.text, "This Vega-Lite chart isn't supported") {
		t.Errorf("a chart declined with no reason: %+v", a)
	}
}

// The notice is the UI's, in its language: each sentence translated whole, golib's error filled in
// as golib wrote it. In Korean, a chart and a diagram golib declined with a reason, and a chart with
// none, are the Korean catalog's sentences, with no English framing left.
func TestTheChartNoticeIsInTheUIsLanguage(t *testing.T) {
	ko := func(id string, args ...any) string {
		text, ok := englishCatalog().Lookup("ko_KR", id)
		if !ok {
			t.Fatalf("%s is not in the Korean catalog", id)
		}
		return fill(text, args...)
	}
	why := errors.New("unsupported: vegalite: a spec outside the subset drawn: a rule coloured by a field")
	for _, c := range []struct {
		req  guiwidget.DiagramRequest
		want string
	}{
		{guiwidget.DiagramRequest{Lang: "vega-lite", Declined: why}, "차트를 그리지 못했습니다: " + why.Error() + ". 소스는 원본 보기나 .chart.json 파일에 있습니다."},
		{guiwidget.DiagramRequest{Lang: "mermaid", Declined: why}, "다이어그램을 그리지 못했습니다: " + why.Error() + ". 소스는 원본 보기나 .mmd 파일에 있습니다."},
		{guiwidget.DiagramRequest{Lang: "vegalite"}, ko(chartNoticeUnsupported)},
	} {
		c.req.Width, c.req.Scale = 400, 1
		a := chartNotice{trf: ko}.Diagram(context.Background(), c.req, func() {})
		if n, ok := a.Pic.(noticeView); a.State != guiwidget.Ready || !ok || n.text != c.want || strings.Contains(n.text, "Its source") {
			t.Errorf("%s in Korean: %+v; want %q", c.req.Lang, a, c.want)
		}
	}

	// the Rendered view's notices are in the host's language once it is told (the host tells
	// NoteTranslator its trf as it is made); English before
	notes := &NoteEmbeds{}
	if got := notes.trf(chartNoticeUnsupported); got != englishText(chartNoticeUnsupported) {
		t.Errorf("before the host's trf: %q", got)
	}
	notes.SetTranslator(ko)
	if got := notes.trf(chartNoticeUnsupported); got != ko(chartNoticeUnsupported) {
		t.Errorf("once told a Korean trf: %q", got)
	}
	var told func(id string, args ...any) string
	newHost(NewSession("unused", nil), Options{NoteTranslator: func(trf func(string, ...any) string) { told = trf }})
	if told == nil || told(chartNoticeUnsupported) != englishText(chartNoticeUnsupported) {
		t.Error("the host did not tell NoteTranslator its trf")
	}
}

// A Mermaid diagram golib's engine declines (a kind outside its subset: a pie) is a notice of golib's
// reason, the parser's own words, as a chart is; a malformed one is still golib's picture of the
// syntax error, never the notice.
func TestADeclinedMermaidDiagramSaysWhy(t *testing.T) {
	_, release := gui.AcquireMeasurer(1)
	release()
	d := nativeDiagrams(nil)
	ask := func(src string) guiwidget.DiagramAnswer {
		t.Helper()
		req := guiwidget.DiagramRequest{Lang: "mermaid", Src: src, Width: 400, Scale: 1, Theme: embedTheme}
		ready := make(chan struct{}, 1)
		a := d.Diagram(context.Background(), req, func() { ready <- struct{}{} })
		if a.State == guiwidget.Pending {
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("the chain never called ready")
			}
			a = d.Diagram(context.Background(), req, func() {})
		}
		return a
	}
	pie := "pie\n  \"a\": 1"
	_, why := mermaid.Parse(pie)
	if why == nil {
		t.Fatal("golib's parser takes a pie: the cell needs a kind outside its subset")
	}
	a := ask(pie)
	if n, ok := a.Pic.(noticeView); a.State != guiwidget.Ready || !ok || !strings.HasPrefix(n.text, "diagram not drawn: ") || !strings.Contains(n.text, why.Error()) {
		t.Errorf("a pie: %+v; want the notice of golib's %q", a, why)
	}
	a = ask("flowchart TB\n  A[unclosed --> B")
	if _, notice := a.Pic.(noticeView); a.State != guiwidget.Ready || a.Pic == nil || notice {
		t.Errorf("a malformed flowchart: %+v; want golib's picture of its error, not the notice", a)
	}
}

// The notice answers only a chart, and a diagram golib declined with a reason: whatever else is
// declined with none stays declined, drawn as its source.
func TestTheChartNoticeAnswersOnlyACharts(t *testing.T) {
	for lang, want := range map[string]guiwidget.DiagramState{"vega-lite": guiwidget.Ready, "vegalite": guiwidget.Ready, "mermaid": guiwidget.Declined} {
		a := chartNotice{}.Diagram(context.Background(), guiwidget.DiagramRequest{Lang: lang, Width: 400, Scale: 1}, func() {})
		if a.State != want {
			t.Errorf("%s: %v, want %v", lang, a.State, want)
		}
	}
}

// A layered chart embed, as xmd's own design document has (bars, a rule a slider places, bars
// faded by a legend selection), is drawn by golib as the page first shows it, not as its JSON.
func TestALayeredChartEmbedIsDrawnAsAStill(t *testing.T) {
	spec := `{"width":"container","height":120,
		"params":[{"name":"budget","value":500,"bind":{"input":"range","min":0,"max":5500}}],
		"data":{"values":[{"part":"main.js","kb":62,"loads":"Always"},{"part":"Mermaid","kb":5165,"loads":"First diagram"}]},
		"layer":[
			{"mark":"bar","encoding":{"y":{"field":"part","type":"nominal","sort":"-x"},"x":{"field":"kb","type":"quantitative"},
				"color":{"field":"loads","type":"nominal"},"opacity":{"condition":{"param":"pick","value":1},"value":0.25}},
			 "params":[{"name":"pick","select":{"type":"point","fields":["loads"]},"bind":"legend"}]},
			{"mark":{"type":"rule","strokeDash":[4,3]},"encoding":{"x":{"datum":{"expr":"budget"},"type":"quantitative"}}}]}`
	notes, _ := embedNote(t, map[string]string{"note/bundle-size.chart.json": spec})
	r := notes.Renderer(guiwidget.Diagrams(nil))(NoteRendererSpec{})
	lines := []string{"![Bundle size by part](bundle-size.chart.json)"}
	host := &realHost{ready: make(chan struct{}, 1)}
	_, bl := layOut(r, lines, 0, false, host)
	select {
	case <-host.ready:
		_, bl = layOut(r, lines, 0, false, host)
	case <-time.After(10 * time.Second):
		t.Fatal("the chain never called ready")
	}
	if bl.Picture == nil {
		t.Fatalf("no picture: %#v", bl)
	}
	if code, isCode := bl.Picture.(laidBlock); isCode {
		t.Fatalf("the layered chart was drawn as its JSON: %q", visible(code.bl))
	}
	rc := gui.NewRecordingCanvas(gui.Size{W: 600, H: bl.Height}, gui.Size{W: 8, H: 16})
	bl.Picture.Paint(rc)
	if !slices.Contains(rc.Ops(), "StrokePath") || !slices.Contains(rc.Ops(), "FillPath") {
		t.Errorf("the chart painted %v: want its bars and its dashed rule", rc.Ops())
	}
}

// A chart with a subtitle and shaped points, as an ADR's boundary chart has, is drawn by golib
// (gui v0.1.41), not answered with the notice.
func TestASubtitledShapedChartEmbedIsDrawn(t *testing.T) {
	spec := `{"title":{"text":"The window is half-open","subtitle":"min in, before out"},"width":300,"height":80,
		"data":{"values":[{"case":"first instant","d":"2019-01-01","in":"yes"},{"case":"after","d":"2020-01-01","in":"no"}]},
		"mark":{"type":"point","filled":true,"size":220},
		"encoding":{"x":{"field":"d","type":"temporal"},"y":{"field":"in","type":"nominal","sort":["yes","no"]},
			"color":{"field":"in","type":"nominal","scale":{"domain":["yes","no"],"range":["#2e9e6a","#d64545"]},"legend":null},
			"shape":{"field":"case","type":"nominal"}}}`
	notes, _ := embedNote(t, map[string]string{"note/window.chart.json": spec})
	r := noteRenderer(notes)(NoteRendererSpec{})
	lines := []string{"![Window](window.chart.json)"}
	host := &realHost{ready: make(chan struct{}, 1)}
	_, bl := layOut(r, lines, 0, false, host)
	select {
	case <-host.ready:
		_, bl = layOut(r, lines, 0, false, host)
	case <-time.After(10 * time.Second):
		t.Fatal("the chain never called ready")
	}
	switch p := bl.Picture.(type) {
	case nil:
		t.Fatalf("no picture: %#v", bl)
	case noticeView:
		t.Fatal("the chart was answered with the notice: golib declined it")
	case laidBlock:
		t.Fatalf("the chart was drawn as its source: %q", visible(p.bl))
	}
	rc := gui.NewRecordingCanvas(gui.Size{W: 600, H: bl.Height}, gui.Size{W: 8, H: 16})
	bl.Picture.Paint(rc)
	if !slices.Contains(rc.Ops(), "DrawText") || !slices.Contains(rc.Ops(), "FillPath") {
		t.Errorf("the chart painted %v: want its symbols and labels", rc.Ops())
	}
}
