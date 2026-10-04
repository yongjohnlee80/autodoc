package daemon

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/index"
)

const daemonSchema = `version: 1
frontmatter:
  type: {type: string, enum: [note, adr]}
`

func searchPaths(t *testing.T, m *Workspaces, name, q string) ([]string, error) {
	t.Helper()
	w, ok := m.Get(name)
	if !ok {
		t.Fatalf("%s is not served", name)
	}
	r, err := w.Index.Search(context.Background(), q, index.QueryOpts{})
	var out []string
	for _, h := range r.Hits {
		out = append(out, h.Path)
	}
	return out, err
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !cond(); time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// The schema's lifecycle in a running daemon: set, edited to invalid (the last valid one stays and
// the error names its line), fixed with a new enum (the notes revalidate), and removed.
func TestSchemaLifecycle(t *testing.T) {
	m, db := open(t)
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.md", "---\ntype: adr\n---\nalpha\n")
	write("b.md", "---\ntype: memo\n---\nalpha\n")
	write(".autodoc/schema.yaml", daemonSchema)
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root, Include: []string{"**/*.md"}}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 2)
	// with no schema, type:adr is a word, not a filter: no note has it
	if got, err := searchPaths(t, m, "kb", "alpha type:adr"); err != nil || len(got) != 0 {
		t.Fatalf("before any schema: %v, %v; want no hits and no error", got, err)
	}

	st, err := m.SetSchema(context.Background(), "kb", ".autodoc/schema.yaml")
	if err != nil || !st.Active || st.Fields != 1 || st.Err != "" {
		t.Fatalf("SetSchema = %+v, %v", st, err)
	}
	eventually(t, "type:adr to find a.md", func() bool {
		got, err := searchPaths(t, m, "kb", "alpha type:adr")
		return err == nil && reflect.DeepEqual(got, []string{"a.md"})
	})
	if p, _ := db.SchemaPath(context.Background(), mustID(t, m, "kb")); p != ".autodoc/schema.yaml" {
		t.Fatalf("stored schema path = %q", p)
	}

	// an invalid edit: reported with its line, and the last valid schema keeps answering
	write(".autodoc/schema.yaml", "version: 1\nfrontmatter:\n  type: {type: text}\n")
	eventually(t, "the invalid edit to be reported", func() bool {
		w, _ := m.Get("kb")
		_, st := w.FrontmatterSchema()
		return st.Err != "" && st.Line == 3 && st.Active
	})
	if got, err := searchPaths(t, m, "kb", "alpha type:adr"); err != nil || !reflect.DeepEqual(got, []string{"a.md"}) {
		t.Fatalf("after an invalid edit: %v, %v; want the last valid schema", got, err)
	}

	// fixed, with memo admitted: b.md revalidates without its file changing
	write(".autodoc/schema.yaml", strings.Replace(daemonSchema, "[note, adr]", "[note, adr, memo]", 1))
	eventually(t, "type:memo to find b.md", func() bool {
		got, err := searchPaths(t, m, "kb", "alpha type:memo")
		return err == nil && reflect.DeepEqual(got, []string{"b.md"})
	})

	// a restart (a pattern edit) keeps the schema
	if err := m.SetPatterns(context.Background(), "kb", []string{"**/*.md"}, nil); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 2)
	if got, err := searchPaths(t, m, "kb", "alpha type:memo"); err != nil || !reflect.DeepEqual(got, []string{"b.md"}) {
		t.Fatalf("after a restart: %v, %v", got, err)
	}

	st, err = m.SetSchema(context.Background(), "kb", "")
	if err != nil || st.Active || st.Path != "" {
		t.Fatalf("removing the schema = %+v, %v", st, err)
	}
	if got, err := searchPaths(t, m, "kb", "alpha type:adr"); err != nil || len(got) != 0 {
		t.Fatalf("after the schema was removed: %v, %v; want type:adr back to a word", got, err)
	}
}

// A path with no file yet is stored and reported; the schema activates when the file appears.
func TestSchemaPathBeforeItsFile(t *testing.T) {
	m, _ := open(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("---\ntype: adr\n---\nalpha\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 1)
	outside := filepath.Join(t.TempDir(), "schema.yaml") // a schema need not live under the root
	st, err := m.SetSchema(context.Background(), "kb", outside)
	if err != nil || st.Active || !strings.Contains(st.Err, "no schema file") {
		t.Fatalf("SetSchema before the file = %+v, %v", st, err)
	}
	if err := os.WriteFile(outside, []byte(daemonSchema), 0o600); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the schema to activate from its new file", func() bool {
		got, err := searchPaths(t, m, "kb", "alpha type:adr")
		return err == nil && reflect.DeepEqual(got, []string{"a.md"})
	})
}

func mustID(t *testing.T, m *Workspaces, name string) int64 {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.served[name]
	if !ok {
		t.Fatalf("%s is not served", name)
	}
	return s.id
}
