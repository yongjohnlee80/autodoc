//go:build gui

package tui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	guiwidget "github.com/yongjohnlee80/golib/gui/widget"

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

func bytesContains(s, sub string) bool { return bytes.Contains([]byte(s), []byte(sub)) }
