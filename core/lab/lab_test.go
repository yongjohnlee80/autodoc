package lab

import (
	"slices"
	"testing"
	"testing/fstest"
)

func TestIsAndOutDir(t *testing.T) {
	for p, want := range map[string]bool{"plan.lab.md": true, "a/b/Plan.LAB.md": true, "plan.md": false, "plan.lab": false, "lab.md": false} {
		if Is(p) != want {
			t.Errorf("Is(%q) = %v, want %v", p, !want, want)
		}
	}
	for note, want := range map[string]string{"plan.lab.md": "plan.lab/out", "/w/notes/x.lab.MD": "x.lab/out"} {
		if got := OutDir(note); got != want {
			t.Errorf("OutDir(%q) = %q, want %q", note, got, want)
		}
	}
}

func TestFenceLang(t *testing.T) {
	for info, want := range map[string]string{"python": "python", "Python {.x}": "python", "  r  ": "r", "": "", "   ": ""} {
		if got := FenceLang(info); got != want {
			t.Errorf("FenceLang(%q) = %q, want %q", info, got, want)
		}
	}
}

// The export hands a code block's Literal (lines joined, a trailing newline), the Rendered view the
// lines between the fences (joined, none), a CRLF file its own endings: one key for all of them.
// Another language, or other code, is another key.
func TestCellKeyAgreesAcrossItsSources(t *testing.T) {
	key := CellKey("python", "x = 1\nprint(x)")
	for _, code := range []string{"x = 1\nprint(x)\n", "x = 1\r\nprint(x)\r\n", "x = 1\nprint(x)\n\n"} {
		if got := CellKey("python", code); got != key {
			t.Errorf("CellKey(%q) = %s, want %s", code, got, key)
		}
	}
	if len(key) != 16 {
		t.Errorf("key %q: want 16 hex digits", key)
	}
	if CellKey("mermaid", "x = 1\nprint(x)") == key {
		t.Error("a mermaid fence with a python cell's body has the cell's key")
	}
	if CellKey("python", "x = 2\nprint(x)") == key {
		t.Error("other code has the same key")
	}
	if CellKey("python", " x = 1\nprint(x)") == key {
		t.Error("leading space is the cell's own: it must change the key")
	}
}

// A cell's outputs are its folder's files in name order; a file being written, a hidden file and a
// folder are not; a cell with no folder has none.
func TestOutputs(t *testing.T) {
	fsys := fstest.MapFS{
		"p.lab/out/k1/02.png":      {Data: []byte("png")},
		"p.lab/out/k1/01.txt":      {Data: []byte("hi")},
		"p.lab/out/k1/03.csv.part": {Data: []byte("a")},
		"p.lab/out/k1/.DS_Store":   {Data: []byte("x")},
		"p.lab/out/k1/sub/x.txt":   {Data: []byte("x")},
	}
	got := Outputs(fsys, "p.lab/out", "k1")
	if want := []string{"p.lab/out/k1/01.txt", "p.lab/out/k1/02.png"}; !slices.Equal(got, want) {
		t.Errorf("Outputs = %q, want %q", got, want)
	}
	if got := Outputs(fsys, "p.lab/out", "none"); got != nil {
		t.Errorf("a cell that has not run: %q", got)
	}
}
