package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const tuiSchema = "version: 1\nfrontmatter:\n  type: {type: string, enum: [note, adr], required: true}\n"

func TestAnOldFrontmatterAnswerKeepsTheNewerDiagnostics(t *testing.T) {
	r := attached(t, startDaemon(t, map[string][]string{"kb": {"memo.md", "# Memo\n"}}))
	r.openByPicker(t, "memo.md")
	r.waitFile(t, "memo.md")
	checked := make(chan struct{})
	onLoop(r, func() bool {
		r.h.fmGen++
		r.h.showDiagnostics([]fmDiagnostic{{field: "type", line: 2, message: "newer text"}})
		r.h.validateNow(Ticket{current: func() bool { close(checked); return false }})
		return true
	})
	select {
	case <-checked:
	case <-time.After(3 * time.Second):
		t.Fatal("the old validation answer did not reach its ticket check")
	}
	if diagnostics := onLoop(r, func() []fmDiagnostic { return r.h.fmDiagnostics }); len(diagnostics) != 1 || diagnostics[0].message != "newer text" {
		t.Fatalf("an old answer replaced newer diagnostics: %v", diagnostics)
	}
}

// writeSchema puts the schema at root's suggested path, after the daemon's first index, as a user
// adds one to a workspace they have.
func writeSchema(t *testing.T, root string) {
	t.Helper()
	writeSchemaText(t, root, tuiSchema)
}

// writeSchemaText is writeSchema with the schema's text.
func writeSchemaText(t *testing.T, root, schema string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".autodoc"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, suggestedSchema), []byte(schema), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A file keeps its own workspace's schema when another workspace is entered. Shown again while the
// other is in use, it is checked against its own schema at its own path, never against the schema
// of the workspace in use, which does not describe it. A new schema for its workspace, saved here or
// by another client while the other is in use, checks it again; entering its own workspace again
// checks it the same way.
func TestAFileFromAnotherWorkspaceIsCheckedAgainstItsOwnSchema(t *testing.T) {
	// finance accepts the memo's type and wants a status; security refuses the type outright
	const financeSchema = "version: 1\nfrontmatter:\n  type: {type: string, enum: [memo], required: true}\n" +
		"  status: {type: string, required: true}\n"
	finance := fileDir(t, "memo.md", "---\ntype: memo\n---\nbody\n")
	security := fileDir(t, "intro.md", "---\ntype: note\n---\nbody\n")
	d := startManaged(t, map[string]string{"finance": finance, "security": security})
	writeSchemaText(t, finance, financeSchema)
	writeSchemaText(t, security, tuiSchema)
	sess := NewSession(d.sock, nil)
	var mu sync.Mutex
	var checkedIn []string // the workspace of each doc.validate of memo.md
	sess.SetBeforeCall(func(method string, params []any) {
		if method == "doc.validate" && len(params) == 3 && params[1] == "memo.md" {
			mu.Lock()
			checkedIn = append(checkedIn, fmt.Sprint(params[0]))
			mu.Unlock()
		}
	})
	r := runTUI(t, sess, Options{})
	r.waitAttached(t, "finance")
	for _, ws := range []string{"finance", "security"} {
		if _, err := r.h.session.Call(context.Background(), "workspace.configure", ws,
			map[string]any{"schema": suggestedSchema}); err != nil {
			t.Fatalf("%s's schema: %v", ws, err)
		}
	}
	const own, foreign = "⚠ frontmatter line 1: status is required", `type: "memo" is not one of [note adr]`
	ownShown := func(what string) {
		t.Helper()
		r.s.WaitFor(t, what, func(sc string) bool { return strings.Contains(sc, own) })
		if sc := r.s.String(); strings.Contains(sc, foreign) {
			t.Fatalf("%s: security's schema judged finance's file:\n%s", what, sc)
		}
	}

	r.h.p.Post(func() { r.h.openPath("memo.md") })
	r.waitOpen(t, "finance", "memo.md")
	ownShown("finance's diagnostic, finance in use")

	// security in use, its own file shown, then finance's memo shown again
	r.h.p.Post(func() { r.h.enter("security") })
	r.waitAttached(t, "security")
	r.h.p.Post(func() { r.h.openPath("intro.md") })
	r.waitOpen(t, "security", "intro.md")
	r.s.WaitFor(t, "no diagnostic over the valid intro", func(sc string) bool { return !strings.Contains(sc, "⚠ frontmatter") })
	mu.Lock()
	before := len(checkedIn)
	mu.Unlock()
	r.h.p.Post(func() { r.h.activate(r.h.bufferOf(fileRef{"finance", "memo.md"})) })
	r.waitOpen(t, "finance", "memo.md")
	r.s.WaitFor(t, "the memo checked again", func(string) bool { mu.Lock(); defer mu.Unlock(); return len(checkedIn) > before })
	ownShown("finance's diagnostic, security in use")

	// a schema saved for finance while security is in use checks the memo again
	mu.Lock()
	before = len(checkedIn)
	mu.Unlock()
	r.h.p.Post(func() { r.h.saved("finance", map[string]any{"schema": suggestedSchema}) })
	r.s.WaitFor(t, "the memo checked for finance's new schema", func(string) bool {
		mu.Lock()
		defer mu.Unlock()
		return len(checkedIn) > before
	})
	ownShown("finance's diagnostic after its schema was saved")

	// and so does another client's change to finance's schema
	mu.Lock()
	before = len(checkedIn)
	mu.Unlock()
	r.h.p.Post(func() {
		r.h.applyEvents([]peerEvent{{kind: "workspace.schema", workspace: "finance", client: "another"}})
	})
	r.s.WaitFor(t, "the memo checked for another client's schema change", func(string) bool {
		mu.Lock()
		defer mu.Unlock()
		return len(checkedIn) > before
	})
	ownShown("finance's diagnostic after another client changed its schema")

	// finance in use again, the memo still shown
	r.h.p.Post(func() { r.h.enter("finance") })
	r.waitAttached(t, "finance")
	ownShown("finance's diagnostic, finance in use again")

	mu.Lock()
	defer mu.Unlock()
	for i, ws := range checkedIn {
		if ws != "finance" {
			t.Errorf("check %d of memo.md was sent in %q, want finance: %q", i, ws, checkedIn)
		}
	}
}

// A note whose frontmatter breaks the schema shows the problem over the page, and fixing the text
// clears it without a save.
func TestFrontmatterDiagnosticsFollowTheText(t *testing.T) {
	root := fileDir(t, "memo.md", "---\ntype: memo\n---\nbody\n")
	d := startManaged(t, map[string]string{"kb": root})
	writeSchema(t, root)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.waitAttached(t, "kb")
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

// A schema's standing reads as it did before it had a catalog: each state, each error's line, and
// whether the last valid schema stays in use, in English's words.
func TestASchemasStandingIsASentenceEach(t *testing.T) {
	for _, c := range []struct {
		s    schemaInfo
		want string
	}{
		{schemaInfo{}, "no schema: files are not checked, and field:value searches as words"},
		{schemaInfo{path: "s.yml", active: true, fields: 3}, "active: 3 fields"},
		{schemaInfo{path: "s.yml"}, "not active"},
		{schemaInfo{path: "s.yml", active: true, fields: 3, err: "bad", line: 4}, "active: 3 fields · line 4: bad (the last valid schema stays in use)"},
		{schemaInfo{path: "s.yml", err: "bad", line: 4}, "not active · line 4: bad"},
		{schemaInfo{path: "s.yml", active: true, fields: 3, err: "bad"}, "active: 3 fields · bad (the last valid schema stays in use)"},
		{schemaInfo{path: "s.yml", err: "bad"}, "not active · bad"},
	} {
		if got := c.s.state(inEnglish{}); got != c.want {
			t.Errorf("%+v reads %q, want %q", c.s, got, c.want)
		}
	}
}
