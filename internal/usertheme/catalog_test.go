package usertheme

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

func TestCatalogAndExportMapping(t *testing.T) {
	dir := t.TempDir()
	for _, base := range themes.Names() {
		if _, err := Create(dir, "copy_"+base, base); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "dark.qml"), []byte(`Theme {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := Open(dir)
	if len(c.Problems) != 1 || !strings.Contains(c.Problems[0].Error(), "shadowed") {
		t.Fatalf("problems: %v", c.Problems)
	}
	if len(c.Names()) != 10 || !slices.Contains(c.Labels(), "copy_sepia (user)") {
		t.Fatalf("names: %v labels: %v", c.Names(), c.Labels())
	}
	for _, base := range themes.Names() {
		want := base
		v, _ := themes.Values(base)
		_, bg := rgb(v["document.base"])
		_, fg := rgb(v["document.text"])
		if !bg || !fg {
			want = "mono"
		}
		if got := c.ExportStyle("copy_" + base); got != want {
			t.Errorf("copy %s maps to %s, want %s", base, got, want)
		}
	}
	if _, err := fs.ReadFile(c.Files(), "dark.qml"); err == nil {
		t.Fatal("shadowing file reached module loader")
	}
	file := filepath.Join(dir, "copy_sepia.qml")
	if err := os.WriteFile(file, []byte(`Theme { app.window: "invalid" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile(c.Files(), "copy_sepia.qml"); err == nil {
		t.Fatal("unchecked bytes reached module loader")
	}
}
