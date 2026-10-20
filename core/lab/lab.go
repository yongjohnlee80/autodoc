// Package lab is what a lab file is (ADR 1792457255): a Markdown note named *.lab.md whose fenced
// code cells run in a Jupyter kernel, and whose outputs are files in its companion folder. AutoDoc's
// export and Rendered view and the autodoc-jupyter plugin all read a lab note through this package,
// so the cell key that names a cell's outputs is computed one way only.
package lab

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
)

// Ext is a lab note's suffix. kind.Ext sees ".md", so a lab note is Markdown to the index, the
// links and every renderer, and its companion folder is its name without ".md": plan.lab.md's is
// plan.lab/, as the export's embedder already finds it.
const Ext = ".lab.md"

// Is reports whether p names a lab note: its suffix, case-insensitive, as kind.Ext lowers it.
func Is(p string) bool { return strings.HasSuffix(strings.ToLower(p), Ext) }

// OutDir is the folder of a lab note's outputs, a slash path relative to the note's own folder:
// "plan.lab/out" for plan.lab.md. Each cell's outputs are in OutDir/<CellKey>/.
func OutDir(note string) string {
	name := filepath.Base(note)
	if strings.HasSuffix(strings.ToLower(name), ".md") {
		name = name[:len(name)-len(".md")]
	}
	return path.Join(name, "out")
}

// FenceLang is a fence's language: the first word of its info string, lower-cased; "" for none.
// The plugin's cells, the export and the Rendered view all read a fence's language through it.
func FenceLang(info string) string {
	words := strings.Fields(info)
	if len(words) == 0 {
		return ""
	}
	return strings.ToLower(words[0])
}

// CellKey is a fence's key: hex of the first 8 bytes of SHA-256 over lang, a NUL, then code with
// its line endings LF and its trailing newlines trimmed. The language is in the key, so a fence of
// another language holding a run cell's code (a mermaid or text fence) has a key no run wrote, and
// shows nothing. The export's code-block Literal and the editor's lines between the fences must
// yield the same key, which is why both normalise here and nowhere else.
func CellKey(lang, code string) string {
	code = strings.TrimRight(strings.ReplaceAll(code, "\r\n", "\n"), "\n")
	sum := sha256.Sum256([]byte(lang + "\x00" + code))
	return hex.EncodeToString(sum[:8])
}

// Outputs are the output files of the cell with key under dir (OutDir, in fsys), as slash paths in
// name order: 01.txt, 02.png, …. A file being written (".part") or hidden is not one yet. None when
// the cell has no outputs: it has not run since its code last changed.
func Outputs(fsys fs.FS, dir, key string) []string {
	cell := path.Join(dir, key)
	entries, err := fs.ReadDir(fsys, cell)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries { // fs.ReadDir sorts by name
		name := e.Name()
		if !e.Type().IsRegular() || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".part") {
			continue
		}
		out = append(out, path.Join(cell, name))
	}
	return out
}
