package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/store"
)

func ptr[T any](v T) *T { return &v }

// openWith is open with the options set; the database features are on unless a test says not.
func openWith(t *testing.T, o Options) (*Workspaces, *store.Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	o.Poll, o.BatchDelay = 20*time.Millisecond, 5*time.Millisecond
	m := New(ctx, db, o)
	t.Cleanup(func() {
		m.StopAll()
		cancel()
		_ = db.Close()
	})
	return m, db
}

func kbWith(t *testing.T, m *Workspaces, files map[string]string, include ...string) {
	t.Helper()
	root := t.TempDir()
	for path, content := range files {
		if err := os.WriteFile(filepath.Join(root, path), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root, Include: include}); err != nil {
		t.Fatal(err)
	}
}

// One save renames the workspace, widens its patterns and changes its section size: it is served
// under its new name only, it indexes what the new patterns admit, and the store holds every
// setting.
func TestConfigure_AppliesEveryChangeAsItsVerbWould(t *testing.T) {
	m, db := openWith(t, Options{Databases: true})
	kbWith(t, m, map[string]string{"a.md": "# A\n", "b.txt": "plain\n"}, "**/*.md")
	indexed(t, m, "kb", 1)
	err := m.Configure(context.Background(), "kb", store.Changes{
		Name:          ptr("docs"),
		Include:       ptr([]string{"**/*.{md,txt}"}),
		Exclude:       ptr([]string{".git/**"}),
		SectionTokens: ptr(256),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := m.Get("kb"); ok {
		t.Error("still served under its old name")
	}
	indexed(t, m, "docs", 2)
	w, _ := m.Get("docs")
	if w.SectionSize() != 256 || strings.Join(w.Include, ",") != "**/*.{md,txt}" {
		t.Errorf("served with section %d, include %v", w.SectionSize(), w.Include)
	}
	list, err := db.Workspaces(context.Background())
	if err != nil || len(list) != 1 || list[0].Name != "docs" || *list[0].SectionTokens != 256 {
		t.Errorf("stored %+v, %v", list, err)
	}
}

// A refused change leaves the workspace as it was, served and stored: a malformed pattern is
// refused before the store is written, and a name another workspace has is refused too.
func TestConfigure_RefusedChangesLeaveItAsItWas(t *testing.T) {
	m, db := openWith(t, Options{Databases: true})
	kbWith(t, m, map[string]string{"a.md": "# A\n"}, "**/*.md")
	indexed(t, m, "kb", 1)
	if _, err := m.Add(context.Background(), config.Workspace{Name: "other", Root: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]store.Changes{
		"a malformed pattern": {Name: ptr("renamed"), Include: ptr([]string{"**/*.{md"}), Exclude: ptr([]string{})},
		"a taken name":        {Name: ptr("other"), SectionTokens: ptr(256)},
		"a postgres destination with no connection": {Name: ptr("renamed"), Destination: ptr(store.DestinationPostgres)},
		"a built-in text extension":                 {Name: ptr("renamed"), TextExtensions: ptr([]string{".md"})},
	} {
		if err := m.Configure(context.Background(), "kb", c); err == nil {
			t.Errorf("%s: saved", name)
		}
		if _, ok := m.Get("kb"); !ok {
			t.Fatalf("%s: kb is no longer served", name)
		}
		list, _ := db.Workspaces(context.Background())
		for _, w := range list {
			if w.Name == "renamed" || (w.Name == "kb" && w.SectionTokens != nil) {
				t.Errorf("%s: stored %+v", name, w.Workspace)
			}
		}
	}
	if err := m.Configure(context.Background(), "nope", store.Changes{Name: ptr("x")}); !errors.Is(err, store.ErrNoWorkspace) {
		t.Errorf("an unknown workspace: %v", err)
	}
}

// Without the database features (a Community build), a database setting is refused and nothing is
// saved; every other setting still is.
func TestConfigure_TheEditionGatesTheDatabaseSettings(t *testing.T) {
	m, db := openWith(t, Options{Databases: false})
	kbWith(t, m, map[string]string{"a.md": "# A\n"}, "**/*.md")
	if !strings.Contains(strings.Join(capsOf(m), ","), "databases=false") {
		t.Errorf("capabilities %v, want databases=false", capsOf(m))
	}
	for name, c := range map[string]store.Changes{
		"a destination":  {Destination: ptr(store.DestinationPostgres)},
		"a source":       {Source: &store.ConnectionSpec{Engine: store.EngineSQLite, DSN: "/x.db"}},
		"view args":      {ViewArgs: ptr(map[string]any{"a": "b"})},
		"a vector index": {VectorIndex: ptr(store.IndexHNSW)},
	} {
		c.SectionTokens = ptr(256)
		if err := m.Configure(context.Background(), "kb", c); !errors.Is(err, errs.ErrUnsupported) {
			t.Errorf("%s: %v, want ErrUnsupported", name, err)
		}
	}
	list, _ := db.Workspaces(context.Background())
	if list[0].SectionTokens != nil {
		t.Error("a refused save wrote the section size it carried")
	}
	if err := m.Configure(context.Background(), "kb", store.Changes{SectionTokens: ptr(256)}); err != nil {
		t.Errorf("a setting outside the gate: %v", err)
	}
}

// workspace.list's databases: the destination and each connection as where it points, never the
// DSN or its password.
func TestConfigure_ListsConnectionsWithoutTheirSecret(t *testing.T) {
	m, _ := openWith(t, Options{Databases: true})
	kbWith(t, m, map[string]string{"a.md": "# A\n"}, "**/*.md")
	if !strings.Contains(strings.Join(capsOf(m), ","), "databases=true") {
		t.Errorf("capabilities %v, want databases=true", capsOf(m))
	}
	err := m.Configure(context.Background(), "kb", store.Changes{
		Destination:     ptr(store.DestinationPostgres),
		DestinationConn: &store.ConnectionSpec{Engine: store.EnginePostgres, DSN: "postgres://me:hunter2@db:5432/rag", Schema: "autodoc"},
		ViewArgs:        ptr(map[string]any{"LabelGroupID": "7"}),
	})
	if err != nil {
		t.Fatal(err)
	}
	w, _ := m.Get("kb")
	d := w.Databases()
	if d.Destination != store.DestinationPostgres || len(d.UID) != 32 || d.ViewArgs["LabelGroupID"] != "7" || len(d.Connections) != 1 {
		t.Fatalf("databases %+v", d)
	}
	c := d.Connections[0]
	if c.Host != "db:5432" || c.Database != "rag" || c.User != "me" || !c.HasPassword || c.Schema != "autodoc" {
		t.Errorf("connection %+v", c)
	}
}

func capsOf(m *Workspaces) []string {
	var out []string
	for k, v := range m.Capabilities() {
		out = append(out, k+"="+map[bool]string{true: "true", false: "false"}[v])
	}
	return out
}
