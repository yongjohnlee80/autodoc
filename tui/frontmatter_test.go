package tui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tuiSchema = "version: 1\nfrontmatter:\n  type: {type: string, enum: [note, adr], required: true}\n"

// writeSchema puts the schema at root's suggested path, after the daemon's first index, as a user
// adds one to a workspace they have.
func writeSchema(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".autodoc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, suggestedSchema), []byte(tuiSchema), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A note whose frontmatter breaks the schema shows the problem over the page, and fixing the text
// clears it without a save.
func TestFrontmatterDiagnosticsFollowTheText(t *testing.T) {
	root := fileDir(t, "memo.md", "---\ntype: memo\n---\nbody\n")
	d := startManaged(t, map[string]string{"kb": root})
	writeSchema(t, root)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 0)
	r.saveForm(func(f *settingsForm) { f.schema = suggestedSchema })
	r.waitNoticed(t, "kb: saved schema")
	r.h.p.Post(func() { r.h.openPath("memo.md") })
	r.waitFile(t, "memo.md")
	r.s.WaitFor(t, "the diagnostic over the page", func(sc string) bool {
		return strings.Contains(sc, `⚠ frontmatter line 2: type: "memo" is not one of [note adr]`)
	})
	if !onLoop(r, func() bool { return r.h.file.open && !r.h.file.dirty }) {
		t.Fatal("a diagnostic changed the note")
	}
	r.h.p.Post(func() {
		r.h.editor.SetValue("---\ntype: adr\n---\nbody\n")
		r.h.edited()
	})
	r.s.WaitFor(t, "the diagnostic gone once the text is valid", func(sc string) bool {
		return !strings.Contains(sc, "⚠ frontmatter")
	})
	// a save is never refused for a diagnostic
	r.h.p.Post(func() {
		r.h.editor.SetValue("---\ntype: memo\n---\nbody\n")
		r.h.edited()
		r.h.save()
	})
	r.s.WaitFor(t, "saved despite the diagnostic", func(string) bool {
		b, _ := os.ReadFile(filepath.Join(root, "memo.md"))
		return string(b) == "---\ntype: memo\n---\nbody\n" && !r.file().dirty
	})
	r.s.WaitForText(t, "⚠ frontmatter line 2")
}
