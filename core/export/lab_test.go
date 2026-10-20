package export_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/lab"
)

// labDoc is a lab note, plan.lab.md, in a temp folder, with the outputs given under
// plan.lab/out/<key>/ (key: the python cell's), and the same outputs under plain/out/<key>/ for a
// plain note beside it.
func labDoc(t *testing.T, key string, outputs map[string]string) (labNote, plainNote string) {
	t.Helper()
	dir := t.TempDir()
	for _, folder := range []string{"plan.lab", "plain"} {
		for name, data := range outputs {
			p := filepath.Join(dir, folder, "out", key, name)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	return filepath.Join(dir, "plan.lab.md"), filepath.Join(dir, "plain.md")
}

// A lab note's cell is followed by its outputs, in name order: text in a <pre class="output">,
// an image as the image. A fence of another language holding the very same code (the negative
// control: its key is not the cell's) shows none, and a note that is not a lab note shows none.
func TestALabNotesCellShowsItsOutputs(t *testing.T) {
	const code = "print(1)"
	png := "\x89PNG\r\n\x1a\nbytes"
	note, plain := labDoc(t, lab.CellKey("python", code), map[string]string{"01.txt": "1 <ok>\n", "02.png": png})
	source := "# Lab\n\n```python\n" + code + "\n```\n\n```text\n" + code + "\n```\n"

	page := renderEmbeds(t, source, note, false)
	out := `<pre class="output">1 &lt;ok&gt;` + "\n" + `</pre>`
	img := `<img src="data:image/png;base64,` + base64.StdEncoding.EncodeToString([]byte(png))
	if strings.Count(page, out) != 1 || strings.Count(page, img) != 1 {
		t.Fatalf("the cell's outputs, once each: text %d, image %d:\n%s", strings.Count(page, out), strings.Count(page, img), page)
	}
	cell := strings.Index(page, `<code class="language-python">`)
	textFence := strings.Index(page, `<code class="language-text">`)
	if !(cell < strings.Index(page, out) && strings.Index(page, out) < strings.Index(page, img) && strings.Index(page, img) < textFence) {
		t.Errorf("want the python cell, its text, its image, then the text fence, in that order:\n%s", page)
	}

	if page := renderEmbeds(t, source, plain, false); strings.Contains(page, `class="output"`) || strings.Contains(page, img) {
		t.Errorf("a plain note showed a cell's outputs:\n%s", page)
	}
}

// With source spans on (the live preview), an output keeps its cell's span, so the preview's
// following the cursor finds the cell's lines there, and no placeholder is left.
func TestALabCellsOutputCarriesItsCellsSpan(t *testing.T) {
	note, _ := labDoc(t, lab.CellKey("python", "x"), map[string]string{"01.txt": "x\n"})
	page := renderEmbeds(t, "```python\nx\n```\n", note, true)
	if !strings.Contains(page, `class="output"`) || strings.Count(page, `data-src="`) < 2 {
		t.Errorf("want the output, its block carrying a data-src as its cell's does:\n%s", page)
	}
}
