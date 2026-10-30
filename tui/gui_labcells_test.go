//go:build gui

package tui

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/lab"
)

// pngOf is a w×h PNG.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{R: 255, A: 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// labNote is a NoteEmbeds told the lab note plan.lab.md, in a folder holding files.
func labNote(t *testing.T, note string, files map[string][]byte) *NoteEmbeds {
	t.Helper()
	dir := t.TempDir()
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	n := &NoteEmbeds{}
	n.SetNote(filepath.Join(dir, note))
	return n
}

var labLines = []string{"```python", "print(1)", "```", "", "```text", "print(1)", "```"}

// A lab note's cell, off the cursor, is its fence with the outputs a run left under it: the fence's
// lines are still the block's (its source and clicks), and the outputs are its picture, one under
// another (a text fence, then the image at its own size, the block wide at most).
func TestALabCellDrawsItsOutputsUnderItsFence(t *testing.T) {
	key := lab.CellKey("python", "print(1)")
	notes := labNote(t, "plan.lab.md", map[string][]byte{
		"plan.lab/out/" + key + "/01.txt": []byte("1\n"),
		"plan.lab/out/" + key + "/02.png": pngOf(t, 40, 20),
	})
	r := notes.Renderer(nil)(NoteRendererSpec{})
	_, bl := layOut(r, labLines, 0, false, nil)
	stack, ok := bl.Picture.(cellStack)
	if !ok || len(stack.items) != 2 {
		t.Fatalf("the cell's picture: %T %+v; want a stack of its two outputs", bl.Picture, bl.Picture)
	}
	if pic, ok := stack.items[1].pic.(imagePicture); !ok || pic.size != (stack.items[1].size) || pic.size.W != 40 || pic.size.H != 20 {
		t.Errorf("the second output: %T at %+v; want the 40×20 image", stack.items[1].pic, stack.items[1].size)
	}
	if !bytesContains(visible(bl), "print(1)") {
		t.Errorf("the fence's own lines are not the block's: %q", visible(bl))
	}
	_, plain := layOut(plainRenderer(t), labLines, 0, false, nil)
	if bl.Height != plain.Height+stack.height {
		t.Errorf("the block is %v high; want the fence's %v and the outputs' %v", bl.Height, plain.Height, stack.height)
	}

	// the negative control: a text fence holding the cell's very code has another key, and no outputs
	if _, other := layOut(r, labLines, 4, false, nil); other.Picture != nil {
		t.Errorf("a text fence with the cell's body drew %T", other.Picture)
	}
	// on the cursor the cell is its source alone
	if _, on := layOut(r, labLines, 0, true, nil); on.Picture != nil {
		t.Errorf("the cell on the cursor drew %T", on.Picture)
	}
}

// golib's gui Editor is the editor whose Rendered view view.redraw lays out again.
var _ relayouter = (*guiwidget.Editor)(nil)

// A run writes a cell's outputs beside the note, its text unchanged, so the editor keeps the cell as
// it was laid out; once the host has the view laid out again (view.redraw, which the Jupyter plugin
// runs after it keeps a cell's outputs), the outputs are drawn under the fence. On golib's own
// editor, painted natively.
func TestARunsOutputsAreDrawnOnceTheViewIsLaidOutAgain(t *testing.T) {
	notes := labNote(t, "plan.lab.md", nil)
	r := &countingRenderer{Renderer: notes.Renderer(nil)(NoteRendererSpec{}), picture: map[int]bool{}}
	editor, onApp, paint := paintedEditor(t, r, "# Plan\n\n```python\nprint(1)\n```\n")
	onApp(paint)
	onApp(func() {
		if drawn, laid := r.picture[2]; !laid || drawn {
			t.Fatalf("before the run the cell is laid out %v, drawn as a picture %v; want its fence alone", laid, drawn)
		}
	})
	out := filepath.Join(filepath.Dir(notes.file()), "plan.lab", "out", lab.CellKey("python", "print(1)"))
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(out, "01.txt"), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	onApp(func() {
		paint()
		if r.picture[2] {
			t.Fatal("the run's outputs were drawn with nothing said: the editor no longer keeps a block's layout")
		}
		(&Host{editor: editor}).redrawRendered()
		paint()
		if !r.picture[2] {
			t.Error("laid out again, the cell does not draw the run's outputs")
		}
	})
}

// plainRenderer is the renderer of a plain note with nothing beside it: golib's layout of a fence.
func plainRenderer(t *testing.T) guiwidget.Renderer {
	return labNote(t, "plain.md", nil).Renderer(nil)(NoteRendererSpec{})
}

// A note that is not a lab note draws no outputs, though its folder holds some for the same key.
func TestAPlainNoteDrawsNoCellOutputs(t *testing.T) {
	key := lab.CellKey("python", "print(1)")
	notes := labNote(t, "plan.md", map[string][]byte{"plan/out/" + key + "/01.txt": []byte("1\n")})
	if _, bl := layOut(notes.Renderer(nil)(NoteRendererSpec{}), labLines, 0, false, nil); bl.Picture != nil {
		t.Errorf("a plain note's fence drew %T", bl.Picture)
	}
}

// Any note's image, embedded alone on its line, is drawn as the image off the cursor, scaled to the
// block's width at most; on the cursor it is its source.
func TestAnEmbeddedImageIsDrawn(t *testing.T) {
	notes := labNote(t, "note.md", map[string][]byte{"note/pic.png": pngOf(t, 900, 300)})
	r := notes.Renderer(nil)(NoteRendererSpec{})
	lines := []string{"# Note", "", "![a picture](pic.png)"}
	_, bl := layOut(r, lines, 2, false, nil)
	pic, ok := bl.Picture.(imagePicture)
	if !ok || pic.size.W != 600 || pic.size.H != 200 || bl.Height != 200 {
		t.Fatalf("the image line: %T %+v, height %v; want the image scaled to 600×200", bl.Picture, bl.PictureSize, bl.Height)
	}
	if _, on := layOut(r, lines, 2, true, nil); on.Picture != nil {
		t.Errorf("the image line on the cursor drew %T", on.Picture)
	}
}

// partsDiagrammer draws each diagram at once, a picture as high as its source is long, but for those
// hold names, which it answers Pending, their ready kept (and called while asked, when during is set).
type partsDiagrammer struct {
	hold    func(guiwidget.DiagramRequest) bool
	during  bool
	asked   []guiwidget.DiagramRequest
	readies []func()
}

func (d *partsDiagrammer) Diagram(_ context.Context, req guiwidget.DiagramRequest, ready func()) guiwidget.DiagramAnswer {
	d.asked = append(d.asked, req)
	if d.hold == nil || !d.hold(req) {
		return guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: partPic(req.Src), Size: gui.Size{W: 100, H: float32(len(req.Src))}}
	}
	d.readies = append(d.readies, ready)
	if d.during {
		ready()
	}
	return guiwidget.DiagramAnswer{State: guiwidget.Pending}
}

type partPic string

func (partPic) Paint(gui.Canvas) {}

// forwardHost asks the Diagrammer at once, as the window's host does, and keeps what it was asked.
type forwardHost struct{ asked []guiwidget.DiagramRequest }

func (h *forwardHost) Diagram(d guiwidget.Diagrammer, _ guiwidget.Block, req guiwidget.DiagramRequest) guiwidget.DiagramAnswer {
	h.asked = append(h.asked, req)
	return d.Diagram(context.Background(), req, func() {})
}

// Every diagram and chart a cell's run left is drawn under its fence, each in its output's place:
// the host is asked once for the cell, its one request per block, and the fence, a cell, asks for
// none of its own.
func TestEveryDiagramOfACellIsDrawn(t *testing.T) {
	key := lab.CellKey("python", "print(1)")
	out := "plan.lab/out/" + key + "/"
	notes := labNote(t, "plan.lab.md", map[string][]byte{
		out + "01.mmd":        []byte("graph TD\n  A-->B"),
		out + "02.txt":        []byte("1\n"),
		out + "03.mmd":        []byte("graph TD\n  C-->D-->E"),
		out + "04.chart.json": []byte(`{"mark": "bar"}`),
	})
	d, host := &partsDiagrammer{}, &forwardHost{}
	r := notes.Renderer(d)(NoteRendererSpec{tuidecl.MarkdownSpec{Mermaid: true}})
	_, bl := layOut(r, labLines, 0, false, host)
	if len(host.asked) != 1 || host.asked[0].Lang != cellDiagramLang {
		t.Fatalf("the host was asked %+v; want the cell's one request", host.asked)
	}
	stack, ok := bl.Picture.(cellStack)
	if !ok || len(stack.items) != 4 {
		t.Fatalf("the cell's picture: %T %+v; want its four outputs", bl.Picture, bl.Picture)
	}
	for i, want := range map[int]partPic{0: "graph TD\n  A-->B", 2: "graph TD\n  C-->D-->E", 3: `{"mark": "bar"}`} {
		if got, ok := stack.items[i].pic.(partPic); !ok || got != want || stack.items[i].size.H != float32(len(want)) {
			t.Errorf("output %d: %T %q at %+v; want the picture of %q", i+1, stack.items[i].pic, got, stack.items[i].size, want)
		}
	}
	if _, ok := stack.items[1].pic.(laidBlock); !ok {
		t.Errorf("the text output: %T; want it laid out as text", stack.items[1].pic)
	}
}

// A cell's diagrams are one request of the host: Ready when every part is, and otherwise Pending,
// its ready called once, when the last part pending is ready (and not before), whenever it is.
func TestACellsDiagramsAreReadyTogether(t *testing.T) {
	parts := []guiwidget.DiagramRequest{{Lang: "mermaid", Src: "a"}, {Lang: "vega-lite", Src: "{}"}, {Lang: "mermaid", Src: "c"}}
	ask := func(d *partsDiagrammer) (guiwidget.DiagramAnswer, *int) {
		calls := new(int)
		return cellDiagrams{d: d, parts: parts}.Diagram(context.Background(), cellRequest(parts), func() { *calls++ }), calls
	}

	a, calls := ask(&partsDiagrammer{})
	if drawn, _ := a.Pic.(cellAnswers); a.State != guiwidget.Ready || len(drawn) != 3 || *calls != 0 || a.Size.H != 4 {
		t.Errorf("every part drawn at once: %v %+v, ready called %d times", a.State, a.Pic, *calls)
	}

	d := &partsDiagrammer{hold: func(r guiwidget.DiagramRequest) bool { return r.Lang == "mermaid" }}
	a, calls = ask(d)
	drawn, _ := a.Pic.(cellAnswers)
	if a.State != guiwidget.Pending || len(drawn) != 3 || drawn[1].State != guiwidget.Ready || len(d.readies) != 2 {
		t.Fatalf("two parts pending: %v %+v, %d readies", a.State, drawn, len(d.readies))
	}
	d.readies[0]()
	d.readies[0]()
	if *calls != 0 {
		t.Fatalf("ready called with a part still pending")
	}
	d.readies[1]()
	if *calls != 1 {
		t.Errorf("ready called %d times once every part was ready; want once", *calls)
	}

	_, calls = ask(&partsDiagrammer{hold: func(guiwidget.DiagramRequest) bool { return true }, during: true})
	if *calls != 1 {
		t.Errorf("parts ready while asked: ready called %d times; want once", *calls)
	}

	other := []guiwidget.DiagramRequest{{Lang: "mermaid", Src: "a\n"}, {Lang: "vega-lite", Src: "{}"}, {Lang: "mermaid", Src: "c"}}
	if cellRequest(parts) == cellRequest(other) {
		t.Error("two cells' diagrams are one request")
	}
}

func bytesContains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
