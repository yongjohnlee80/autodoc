package tui

import (
	"context"
	"os"
	"path/filepath"
	"slices"
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

// The manager's Schema… suggests the root's .autodoc/schema.yaml; saving it activates the schema,
// and a broken file is reported with its line.
func TestWorkspaceSchemaDialog(t *testing.T) {
	root := noteDir(t, "a.md", "---\ntype: adr\n---\nalpha\n")
	d := startManaged(t, map[string]string{"kb": root})
	writeSchema(t, root)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.manageWorkspaces() })
	r.s.WaitForText(t, "Schema…")
	r.h.p.Post(func() { r.h.startSchema(0) })
	r.s.WaitForText(t, "frontmatter schema · kb")
	r.s.WaitForText(t, suggestedSchema) // the suggestion fills the path field
	r.s.WaitForText(t, "no schema: notes are not checked")
	r.h.p.Post(func() { r.h.saveSchema(suggestedSchema) })
	r.s.WaitForText(t, "kb: schema active, 1 fields")
	ws, err := d.db.Workspaces(context.Background())
	if err != nil || ws[0].SchemaPath == nil || *ws[0].SchemaPath != suggestedSchema {
		t.Fatalf("stored schema = %+v, %v", ws, err)
	}

	if err := os.WriteFile(filepath.Join(root, "broken.yaml"), []byte("version: 1\nfrontmatter:\n  type: {type: nope}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() { r.h.saveSchema("broken.yaml") })
	r.s.WaitFor(t, "the broken schema reported with its line", func(sc string) bool {
		return strings.Contains(sc, "schema saved; active: 1 fields · line 3:")
	})
}

// A note whose frontmatter breaks the schema shows the problem over the page, and fixing the text
// clears it without a save.
func TestFrontmatterDiagnosticsFollowTheText(t *testing.T) {
	root := noteDir(t, "memo.md", "---\ntype: memo\n---\nbody\n")
	d := startManaged(t, map[string]string{"kb": root})
	writeSchema(t, root)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.schemaWorkspace = "kb"; r.h.saveSchema(suggestedSchema) })
	r.s.WaitForText(t, "schema active")
	r.h.p.Post(func() { r.h.openPath("memo.md") })
	r.waitNote(t, "memo.md")
	r.s.WaitFor(t, "the diagnostic over the page", func(sc string) bool {
		return strings.Contains(sc, `⚠ frontmatter line 2: type: "memo" is not one of [note adr]`)
	})
	if !onLoop(r, func() bool { return r.h.note.open && !r.h.note.dirty }) {
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
		return string(b) == "---\ntype: memo\n---\nbody\n" && !r.note().dirty
	})
	r.s.WaitForText(t, "⚠ frontmatter line 2")
}

// Your own text types: declared and admitted in one action, previewed as indexed, refused for a
// Pro format, and their include dropped when removed.
func TestCustomTextTypes(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": noteDir(t, "n.md", "# Notes\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.openFileTypes() })
	r.s.WaitForText(t, "none: e.g. .log, .rst")
	r.h.p.Post(func() { r.h.setCustomTypes("log") })
	r.s.WaitFor(t, ".log declared and admitted", func(string) bool {
		ws, err := d.db.Workspaces(context.Background())
		return err == nil && len(ws) == 1 && ws[0].TextExtensions != nil && *ws[0].TextExtensions == `[".log"]` &&
			slices.Contains(ws[0].Include, "**/*.log")
	})
	r.s.WaitForText(t, "sample.log: indexed")

	r.h.p.Post(func() { r.h.setCustomTypes(".log, .pdf") })
	r.s.WaitForText(t, "not changed:")
	r.s.WaitForText(t, "Pro document format")

	r.h.p.Post(func() { r.h.setCustomTypes("") })
	r.s.WaitFor(t, ".log removed with its include", func(string) bool {
		ws, err := d.db.Workspaces(context.Background())
		return err == nil && len(ws) == 1 && ws[0].TextExtensions == nil && !slices.Contains(ws[0].Include, "**/*.log")
	})
}
