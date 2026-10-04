package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/kind"
)

// crumbOf is the breadcrumb of the first hit for q.
func crumbOf(t *testing.T, m *Workspaces, q string) string {
	t.Helper()
	w, _ := m.Get("kb")
	r, err := w.Index.Search(context.Background(), q, index.QueryOpts{Mode: index.ModeLexical})
	if err != nil || len(r.Hits) == 0 {
		return ""
	}
	return r.Hits[0].Breadcrumb
}

// A declared text extension is read as plain text (no Markdown heading from a # line), at once
// and without a restart; removing it reads the file as Markdown again.
func TestTextExtensionsChangeHowFilesAreRead(t *testing.T) {
	m, db := open(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.log"), []byte("# Boot\nkestrel started\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root, Include: []string{"**/*.log"}}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 1)
	if got := crumbOf(t, m, "kestrel"); got != "Boot" {
		t.Fatalf("before: crumb %q, want the Markdown heading Boot", got)
	}

	norm, err := m.SetTextExtensions(context.Background(), "kb", []string{"LOG"})
	if err != nil || !reflect.DeepEqual(norm, []string{".log"}) {
		t.Fatalf("SetTextExtensions = %v, %v", norm, err)
	}
	eventually(t, "app.log read as plain text", func() bool { return crumbOf(t, m, "kestrel") == "app" })
	if got, _ := db.TextExtensions(context.Background(), mustID(t, m, "kb")); !reflect.DeepEqual(got, []string{".log"}) {
		t.Fatalf("stored = %v", got)
	}
	w, _ := m.Get("kb")
	if got := w.TextExtensions(); !reflect.DeepEqual(got, []string{".log"}) {
		t.Fatalf("served = %v", got)
	}
	// the document API reads it as text too: not UTF-8 is refused
	if err := os.WriteFile(filepath.Join(root, "bin.log"), []byte{0xff, 0xfe, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Docs.Read(context.Background(), "bin.log"); err == nil {
		t.Fatal("a .log that is not UTF-8 was read as text")
	}

	if _, err := m.SetTextExtensions(context.Background(), "kb", nil); err != nil {
		t.Fatal(err)
	}
	eventually(t, "app.log read as Markdown again", func() bool { return crumbOf(t, m, "kestrel") == "Boot" })

	for _, bad := range []string{".pdf", ".md", "no spaces"} {
		var e *kind.ErrExtension
		if _, err := m.SetTextExtensions(context.Background(), "kb", []string{bad}); !errors.As(err, &e) {
			t.Errorf("%q: %v, want a refusal", bad, err)
		}
	}
}
