package tui

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
	serving "github.com/yongjohnlee80/autodoc/internal/daemon"
)

// openSettings opens name's settings on tab and waits for the dialog.
func (r *running) openSettings(t *testing.T, name string, tab int) {
	t.Helper()
	r.h.p.Post(func() { r.h.openSettings(name, tab) })
	r.s.WaitForText(t, "workspace settings · "+name)
}

// saveForm saves the open dialog as it was filled, after edit: what typing into it and pressing
// Save would send, the dialog closed first as Save closes it.
func (r *running) saveForm(edit func(f *settingsForm)) {
	r.h.p.Post(func() {
		f := r.h.settings.form()
		edit(&f)
		r.h.closeDialog("workspaceSettings")
		r.h.saveSettings(f)
	})
}

func storedOnly(t *testing.T, d *managedDaemon) store.WorkspaceInfo {
	t.Helper()
	ws, err := d.db.Workspaces(context.Background())
	if err != nil || len(ws) != 1 {
		t.Fatalf("workspaces: %+v, %v", ws, err)
	}
	return ws[0]
}

// ---- what a save sends ----

func baseFor(w wsInfo) settingsBase {
	return settingsBase{w: w, providers: []string{"", "local"}, databases: true}
}

func kbInfo() wsInfo {
	return wsInfo{name: "kb", root: "/kb", state: "ready", include: []string{"**/*.md", "**/*.txt", "**/*.yaml", "**/*.yml"},
		exclude: []string{".git/**"}, sectionTokens: 512, embeddingPolicy: store.EmbeddingAlways,
		db: databasesInfo{destination: store.DestinationLocal}}
}

// A save sends only what differs from the workspace as the dialog opened on it: nothing, for a
// form left as it was; the file types and text types folded into one change of the rules.
func TestSettingsChanges(t *testing.T) {
	pg := kbInfo()
	pg.db = databasesInfo{destination: store.DestinationPostgres, vectorIndex: store.IndexHNSW,
		destConn: &connInfo{engine: "postgres", host: "db", database: "rag", schema: "autodoc", hasPassword: true},
		source:   &connInfo{engine: "postgres", host: "src", database: "labels"}, viewArgs: map[string]any{"a": "1"}}
	for name, tc := range map[string]struct {
		w    wsInfo
		edit func(f *settingsForm)
		want map[string]any
	}{
		"nothing":            {kbInfo(), func(*settingsForm) {}, map[string]any{}},
		"nothing, databases": {pg, func(*settingsForm) {}, map[string]any{}},
		"a name and a section": {kbInfo(), func(f *settingsForm) { f.name, f.section = " docs ", "256" },
			map[string]any{"name": "docs", "section_tokens": int64(256)}},
		"plain text off": {kbInfo(), func(f *settingsForm) { f.txt = 1 },
			map[string]any{"include": []any{"**/*.md", "**/*.yaml", "**/*.yml"}, "exclude": []any{".git/**", "**/*.txt"}}},
		"a text type, admitted": {kbInfo(), func(f *settingsForm) { f.texts = "log" },
			map[string]any{"text_extensions": []any{".log"}, "include": []any{"**/*.md", "**/*.txt", "**/*.yaml", "**/*.yml", "**/*.log"},
				"exclude": []any{".git/**"}}},
		"the policy and the provider": {kbInfo(), func(f *settingsForm) { f.policy, f.provider = 2, 1 },
			map[string]any{"embedding_policy": store.EmbeddingNever, "provider": "local"}},
		"to postgres": {kbInfo(), func(f *settingsForm) { f.dest, f.destDSN, f.destSchema, f.index = 1, "postgres://db/rag", "autodoc", 2 },
			map[string]any{"destination": "postgres", "vector_index": "ivfflat",
				"destination_connection": map[string]any{"engine": "postgres", "dsn": "postgres://db/rag", "schema": "autodoc"}}},
		// back to the local store: the connection goes, and the vector index with it
		"back to the local store": {pg, func(f *settingsForm) { f.dest = 0 },
			map[string]any{"destination": "sqlite", "vector_index": "", "destination_connection": map[string]any{"remove": true}}},
		// a blank DSN keeps the stored one: only the schema is sent new
		"the destination's schema": {pg, func(f *settingsForm) { f.destSchema = "rag2" },
			map[string]any{"destination_connection": map[string]any{"engine": "postgres", "dsn": "", "schema": "rag2"}}},
		"no source": {pg, func(f *settingsForm) { f.src = 0 }, map[string]any{"source": map[string]any{"remove": true}}},
		"view args": {pg, func(f *settingsForm) { f.viewArgs = "b = 2; a=1" }, map[string]any{"view_args": map[string]any{"a": "1", "b": "2"}}},
	} {
		b := baseFor(tc.w)
		f := b.form()
		tc.edit(&f)
		got, err := b.changes(f)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s:\n got %v\nwant %v", name, got, tc.want)
		}
	}
}

// What a save refuses before it is sent, with a reason a user can act on.
func TestSettingsChangesRefused(t *testing.T) {
	pg := kbInfo()
	pg.db = databasesInfo{destination: store.DestinationPostgres, source: &connInfo{engine: "postgres", host: "src"}}
	for name, tc := range map[string]struct {
		w    wsInfo
		edit func(f *settingsForm)
		want string
	}{
		"a section out of range":      {kbInfo(), func(f *settingsForm) { f.section = "4096" }, "128–2048"},
		"an empty rule":               {kbInfo(), func(f *settingsForm) { f.include = "**/*.md;;**/*.txt" }, "empty rule"},
		"a built-in text type":        {kbInfo(), func(f *settingsForm) { f.texts = ".md" }, ".md"},
		"view args without =":         {pg, func(f *settingsForm) { f.viewArgs = "a=1; b" }, "key=value"},
		"a new engine on the old DSN": {pg, func(f *settingsForm) { f.src = 2 }, "needs its connection"},
	} {
		b := baseFor(tc.w)
		f := b.form()
		tc.edit(&f)
		if _, err := b.changes(f); err == nil || !strings.Contains(err.Error(), tc.want) || strings.HasPrefix(err.Error(), "kind:") {
			t.Errorf("%s: %v, want a reason with %q", name, err, tc.want)
		}
	}
}

// A workspace whose own provider is gone from the stored ones keeps it through a save of
// something else.
func TestAGoneProviderIsKeptBySaves(t *testing.T) {
	w := kbInfo()
	w.provider = "retired"
	b := settingsBase{w: w, providers: withOwnProvider([]string{"", "local"}, w.provider)}
	f := b.form()
	f.section = "256"
	if got, err := b.changes(f); err != nil || !reflect.DeepEqual(got, map[string]any{"section_tokens": int64(256)}) {
		t.Errorf("changes %v, %v; want the section alone", got, err)
	}
}

// Without the database features nothing about them is sent, whatever the form holds.
func TestSettingsChangesWithoutDatabases(t *testing.T) {
	b := baseFor(kbInfo())
	b.databases = false
	f := b.form()
	f.dest, f.src, f.viewArgs = 1, 1, "a=1"
	if got, err := b.changes(f); err != nil || len(got) != 0 {
		t.Errorf("changes %v, %v; want none", got, err)
	}
}

// ---- the dialog against a daemon ----

// Every field of both tabs reaches the save in its place: each is filled with a value of its own,
// Save is pressed in the dialog, and the store holds each one where it belongs. A field read into
// another's place would show as a value stored in the wrong setting, or refused.
func TestTheSettingsDialogSavesEveryField(t *testing.T) {
	root := fileDir(t, "a.md", "# A\n")
	schema := filepath.Join(t.TempDir(), "s.yaml") // outside the root, which indexes YAML
	if err := os.WriteFile(schema, []byte(tuiSchema), 0o600); err != nil {
		t.Fatal(err)
	}
	d := startManagedWith(t, map[string]string{"kb": root}, serving.Options{Databases: true})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 1)
	r.s.WaitForText(t, "source database") // the edition offers the databases
	r.h.p.Post(func() {
		r.h.fillSettings(settingsForm{name: "docs", root: "/ignored", schema: schema, texts: ".log",
			include: "**/*.md", exclude: ".git/**", md: 0, txt: 1, yaml: 1, section: "256", policy: 2, provider: 0,
			dest: 1, destDSN: "postgres://me:pw@db:5432/rag", destSchema: "autodoc", index: 2,
			src: 2, srcDSN: "/data/labels.db", srcSchema: "main", viewArgs: "LabelGroupID=7"})
	})
	r.keys(t, tab(), enter()) // into the first field of the tab, and Enter saves
	r.waitNoticed(t, "docs: saved")

	w := storedOnly(t, d)
	if w.Name != "docs" || w.SchemaPath == nil || *w.SchemaPath != schema {
		t.Errorf("name %q, schema %v", w.Name, w.SchemaPath)
	}
	if w.TextExtensions == nil || *w.TextExtensions != `[".log"]` {
		t.Errorf("text types %v", w.TextExtensions)
	}
	if !slices.Equal(w.Include, []string{"**/*.md", "**/*.log"}) || !slices.Equal(w.Exclude, []string{".git/**", "**/*.txt", "**/*.yaml", "**/*.yml"}) {
		t.Errorf("rules %v / %v", w.Include, w.Exclude)
	}
	if w.SectionTokens == nil || *w.SectionTokens != 256 || w.EmbeddingPolicy != store.EmbeddingNever {
		t.Errorf("section %v, policy %q", w.SectionTokens, w.EmbeddingPolicy)
	}
	if w.Destination != store.DestinationPostgres || w.VectorIndex == nil || *w.VectorIndex != store.IndexIVFFlat {
		t.Errorf("destination %q, index %v", w.Destination, w.VectorIndex)
	}
	if w.ViewArgs == nil || !strings.Contains(*w.ViewArgs, `"LabelGroupID":"7"`) {
		t.Errorf("view args %v", w.ViewArgs)
	}
	conns, err := d.db.Connections(context.Background(), w.ID)
	if err != nil || len(conns) != 2 {
		t.Fatalf("connections %+v, %v", conns, err)
	}
	for _, c := range conns {
		switch c.Role {
		case store.RoleDestination:
			if c.Engine != store.EnginePostgres || c.Host != "db:5432" || c.Database != "rag" || c.Schema != "autodoc" || !c.HasPassword {
				t.Errorf("destination %+v", c)
			}
		case store.RoleSource:
			if c.Engine != store.EngineSQLite || c.Database != "/data/labels.db" || c.Schema != "main" {
				t.Errorf("source %+v", c)
			}
		}
	}
	if strings.Contains(r.s.String(), "pw@") {
		t.Error("a DSN's password is on screen")
	}
}

// A refused save changes nothing, and opens the dialog again as it was typed, on the tab it was
// on, the reason on its help line: a section out of range is refused before it is sent, and a
// root gone from disk by the daemon, the text types with the rules (one save, not two).
func TestARefusedSaveChangesNothing(t *testing.T) {
	root := fileDir(t, "n.md", "# Notes\n")
	d := startManaged(t, map[string]string{"kb": root})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 1)
	r.saveForm(func(f *settingsForm) { f.name, f.section = "renamed", "9999" })
	r.s.WaitForText(t, "not saved: section size must be 128–2048")
	if got := onLoop(r, func() string { return r.h.settings.w.name }); got != "kb" {
		t.Errorf("the dialog reopened on %q", got)
	}
	if got := onLoop(r, func() int { return r.h.settingsTab }); got != 1 {
		t.Errorf("the dialog reopened on tab %d, want Advanced", got)
	}

	if err := os.RemoveAll(root); err != nil { // the rules check the root
		t.Fatal(err)
	}
	r.saveForm(func(f *settingsForm) { f.texts = ".log" })
	r.s.WaitForText(t, "not saved:")
	if w := storedOnly(t, d); w.TextExtensions != nil || slices.Contains(w.Include, "**/*.log") || w.Name != "kb" {
		t.Fatalf("a refused save changed the store: %+v", w)
	}
}

// The Edit tab's schema: the state line says there is none and suggests a path; a schema saved
// is active, and a broken one is reported with its line when the dialog opens again.
func TestTheSettingsSchema(t *testing.T) {
	root := fileDir(t, "a.md", "---\ntype: adr\n---\nalpha\n")
	d := startManaged(t, map[string]string{"kb": root})
	writeSchema(t, root)
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 0)
	r.s.WaitForText(t, "no schema: files are not checked")
	r.s.WaitForText(t, suggestedSchema)
	r.saveForm(func(f *settingsForm) { f.schema = suggestedSchema })
	r.waitNoticed(t, "kb: saved schema")
	if w := storedOnly(t, d); w.SchemaPath == nil || *w.SchemaPath != suggestedSchema {
		t.Fatalf("stored schema %v", w.SchemaPath)
	}
	r.openSettings(t, "kb", 0)
	r.s.WaitForText(t, "active: 1 fields")

	if err := os.WriteFile(filepath.Join(root, "broken.yaml"), []byte("version: 1\nfrontmatter:\n  type: {type: nope}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r.saveForm(func(f *settingsForm) { f.schema = "broken.yaml" })
	r.waitNoticed(t, "kb: saved schema")
	r.openSettings(t, "kb", 0)
	r.s.WaitForText(t, "line 3:")
}

// Your own text types: declared and admitted in one save, refused for a Pro format with the
// format named, and their include dropped when removed.
func TestTheSettingsTextTypes(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "n.md", "# Notes\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 0)
	r.saveForm(func(f *settingsForm) { f.texts = "log" })
	r.waitNoticed(t, "kb: saved")
	if w := storedOnly(t, d); w.TextExtensions == nil || *w.TextExtensions != `[".log"]` || !slices.Contains(w.Include, "**/*.log") {
		t.Fatalf("stored %+v", w)
	}
	r.openSettings(t, "kb", 0)
	r.saveForm(func(f *settingsForm) { f.texts = ".log, .pdf" })
	r.s.WaitForText(t, "not saved:")
	r.s.WaitForText(t, "Pro document format")
	r.openSettings(t, "kb", 0)
	r.saveForm(func(f *settingsForm) { f.texts = "" })
	r.s.WaitFor(t, ".log removed with its include", func(string) bool {
		ws, err := d.db.Workspaces(context.Background())
		return err == nil && len(ws) == 1 && ws[0].TextExtensions == nil && !slices.Contains(ws[0].Include, "**/*.log")
	})
}

// Advanced's provider: the daemon's first, then the stored providers; one that does not set up is
// refused with the reason, and the workspace keeps the daemon's.
func TestTheSettingsProvider(t *testing.T) {
	ollama := newFakeOllama(t, "embedder")
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n\nalpha\n")})
	ctx := context.Background()
	for _, sp := range []store.ProviderSpec{
		{Name: "local", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "embedder"},
		{Name: "missing", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "no-such-model"},
	} {
		if _, err := d.db.AddProvider(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 1)
	r.s.WaitForText(t, "uses the daemon's provider")
	providers := onLoop(r, func() []string { return r.h.settings.providers })
	if len(providers) != 3 || providers[0] != "" {
		t.Fatalf("choices %q, want the daemon's then the two providers", providers)
	}
	r.saveForm(func(f *settingsForm) { f.provider = slices.Index(providers, "missing") })
	r.s.WaitForText(t, "not saved:")
	r.openSettings(t, "kb", 1)
	r.saveForm(func(f *settingsForm) { f.provider = slices.Index(providers, "local") })
	r.waitNoticed(t, "kb: saved provider")
	ws, _ := d.db.Workspaces(ctx)
	if p, err := d.db.WorkspaceProvider(ctx, ws[0].ID); err != nil || p != "local" {
		t.Fatalf("stored = %q, %v", p, err)
	}
	r.openSettings(t, "kb", 1)
	r.s.WaitForText(t, "uses its own: local")
}

// Opened again at once after a save, while the save's relisting is held back, the dialog shows
// what was saved: it reads the workspace from the daemon, not from the last listing.
func TestTheSettingsReopenedAtOnceShowTheSave(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	var holding atomic.Bool
	release := make(chan struct{})
	sess := NewSession(d.sock, nil)
	sess.beforeCall = func(method string, _ []any) {
		if method == "workspace.list" && holding.Load() {
			<-release
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 1)
	holding.Store(true) // from here every listing waits: the save's relisting cannot land first
	r.saveForm(func(f *settingsForm) { f.policy = 2 })
	r.waitNoticed(t, "kb: saved embedding")
	r.h.p.Post(func() { r.h.openSettings("kb", 1) })
	time.Sleep(100 * time.Millisecond) // the reopen is under way, its listing held with the relisting
	close(release)
	r.s.WaitFor(t, "the saved policy", func(string) bool {
		return onLoop(r, func() string { return r.h.settings.w.embeddingPolicy }) == store.EmbeddingNever
	})
}

// heldFirstList runs the TUI over a daemon with kb, where the first workspace.list after arm()
// waits until the returned release is called; the manager is open.
func heldFirstList(t *testing.T) (r *running, arm func(), held func() bool, release func()) {
	t.Helper()
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	var n atomic.Int32
	var armed atomic.Bool
	ch := make(chan struct{})
	sess := NewSession(d.sock, nil)
	sess.beforeCall = func(method string, _ []any) {
		if method == "workspace.list" && armed.Load() && n.Add(1) == 1 {
			<-ch
		}
	}
	r = runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.manageWorkspaces() })
	r.s.WaitForText(t, "Advanced…")
	return r, func() { armed.Store(true) }, func() bool { return n.Load() >= 1 }, func() { close(ch) }
}

// Edit… asked for, then Advanced… while Edit's read is held: the late Edit never takes the dialog
// back to its tab.
func TestALateSettingsOpenNeverReplacesALaterOne(t *testing.T) {
	r, arm, held, release := heldFirstList(t)
	arm()
	r.h.p.Post(func() { r.h.startEditWorkspace(0) }) // its read is held
	r.s.WaitFor(t, "the Edit read held", func(string) bool { return held() })
	r.h.p.Post(func() { r.h.startAdvancedWorkspace(0) }) // its read answers at once
	r.s.WaitForText(t, "section size (128–2048")
	release()
	r.h.p.Post(func() {})
	time.Sleep(150 * time.Millisecond)
	if got := onLoop(r, func() int { return r.h.settingsTab }); got != 1 {
		t.Fatalf("the late Edit open moved the dialog to tab %d", got)
	}
}

// A dialog opened at once after a pending Edit… stays: Delete… is not replaced when the read
// answers late.
func TestALateSettingsOpenNeverReplacesALaterSyncDialog(t *testing.T) {
	r, arm, held, release := heldFirstList(t)
	arm()
	r.h.p.Post(func() { r.h.startEditWorkspace(0) })
	r.s.WaitFor(t, "the Edit read held", func(string) bool { return held() })
	r.h.p.Post(func() { r.h.startRemoveWorkspace(0) })
	r.s.WaitForText(t, "delete the workspace?")
	release()
	time.Sleep(150 * time.Millisecond)
	r.s.WaitForText(t, "delete the workspace?")
	if strings.Contains(r.s.String(), "workspace settings · kb") {
		t.Fatal("the late Edit open replaced Delete")
	}
}

// Closing the manager while Edit… reads leaves nothing to open over the page.
func TestALateSettingsOpenNeverOpensAfterTheManagerCloses(t *testing.T) {
	r, arm, held, release := heldFirstList(t)
	arm()
	r.h.p.Post(func() { r.h.startEditWorkspace(0) })
	r.s.WaitFor(t, "the Edit read held", func(string) bool { return held() })
	r.keys(t, esc()) // the manager's own dismissal
	r.s.WaitFor(t, "the manager closed", func(sc string) bool { return !strings.Contains(sc, "Advanced…") })
	release()
	time.Sleep(150 * time.Millisecond)
	if strings.Contains(r.s.String(), "workspace settings · kb") {
		t.Fatal("the late Edit open came up after the manager was closed")
	}
}

// Options › Preferences › File types… opens the workspace in use on its Edit tab, with the file
// types and the Pro boundary.
func TestFileTypesOpenTheEditTab(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "a\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(r.h.openActiveSettings)
	r.s.WaitFor(t, "the Edit tab", func(sc string) bool {
		return strings.Contains(sc, "workspace settings · kb") && strings.Contains(sc, "Markdown (.md)") &&
			strings.Contains(sc, "Plain text (.txt)") && strings.Contains(sc, "YAML (.yaml, .yml)") &&
			strings.Contains(sc, "Pro: .doc, .docx, .odt, .pdf")
	})
}

// The manager lists each workspace by title and directory, and beside the list the settings of the
// one under the cursor, moving with it.
func TestTheManagerShowsTheSettingsBesideTheList(t *testing.T) {
	d := startManaged(t, map[string]string{"alpha": fileDir(t, "a.md", "a\n"), "beta": fileDir(t, "b.md", "b\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{Workspace: "alpha"})
	r.s.WaitForText(t, "· alpha")
	r.h.p.Post(r.h.manageWorkspaces)
	r.s.WaitForText(t, "DIRECTORY")
	r.s.WaitForText(t, "alpha · ready")
	r.s.WaitForText(t, "512 estimated tokens")
	r.keys(t, key('j'))
	r.s.WaitForText(t, "beta · ready")
}

// Opened on Advanced, cancelled, then opened on Edit: the dialog is closed when its tab is chosen,
// and opens on Edit (a tab chosen while the dialog was closed once took the program down).
func TestTheSettingsOpenOnTheTabAskedForAfterACancel(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.openSettings(t, "kb", 1)
	r.s.WaitForText(t, "section size (128–2048")
	r.keys(t, esc())
	r.s.WaitFor(t, "the dialog closed", func(sc string) bool { return !strings.Contains(sc, "workspace settings") })
	r.openSettings(t, "kb", 0)
	r.s.WaitFor(t, "the Edit tab", func(sc string) bool {
		return strings.Contains(sc, "Markdown (.md)") && !strings.Contains(sc, "section size (128–2048")
	})
	r.keys(t, esc())
	r.openSettings(t, "kb", 1)
	r.s.WaitForText(t, "section size (128–2048")
}

// The manager's pane lists the database settings only where the edition offers them.
func TestTheManagerDetail(t *testing.T) {
	w := kbInfo()
	w.db = databasesInfo{destination: store.DestinationPostgres, vectorIndex: store.IndexHNSW,
		destConn: &connInfo{engine: "postgres", host: "db:5432", database: "rag", user: "me", hasPassword: true},
		viewArgs: map[string]any{"LabelGroupID": "7"}}
	without, with := managerDetail(w, false), managerDetail(w, true)
	for _, s := range []string{"source", "view args", "destination"} {
		if strings.Contains(without, s) {
			t.Errorf("without the databases, the pane lists %q:\n%s", s, without)
		}
	}
	for _, s := range []string{"EDIT", "ADVANCED", "512 estimated tokens", "source       none", "view args    LabelGroupID=7",
		"destination  postgres · me@db:5432/rag · password kept · hnsw"} {
		if !strings.Contains(with, s) {
			t.Errorf("the pane lacks %q:\n%s", s, with)
		}
	}
}

// After a rename, and before the relisting answers, Delete… and Edit… act on the workspace by its
// new name: the list holds it at once (a delete asked for by the old name was refused as no such
// workspace).
func TestARenameIsInTheListBeforeTheRelisting(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	var holding atomic.Bool
	release := make(chan struct{})
	sess := NewSession(d.sock, nil)
	sess.beforeCall = func(method string, _ []any) {
		if method == "workspace.list" && holding.Load() {
			<-release
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(r.h.manageWorkspaces)
	r.s.WaitForText(t, "Advanced…")
	r.openSettings(t, "kb", 0)
	holding.Store(true) // from here every listing waits
	defer close(release)
	r.saveForm(func(f *settingsForm) { f.name = "docs" })
	r.waitNoticed(t, "docs: saved name")
	r.h.p.Post(func() { r.h.startRemoveWorkspace(0) })
	r.s.WaitForText(t, "Delete the workspace docs?")
	if got := onLoop(r, func() string { return r.h.removing }); got != "docs" {
		t.Fatalf("Delete… asked about %q, want docs", got)
	}
}
