package usertheme

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/yongjohnlee80/golib/tui/decl/themes"

	"github.com/yongjohnlee80/autodoc/core/export"
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
	// golib's themes, and a copy of each
	if len(c.Names()) != 2*len(themes.Names()) || !slices.Contains(c.Labels(), "copy_sepia (user)") {
		t.Fatalf("names: %v labels: %v", c.Names(), c.Labels())
	}
	for _, base := range themes.Names() {
		// a copy is drawn as its base is: the base's export theme, or mono without RGB colours
		want := c.ExportStyle(base)
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
	var paths []string
	for _, base := range themes.Names() {
		paths = append(paths, "copy_"+base+".qml")
	}
	if err := fstest.TestFS(c.Files(), paths...); err != nil {
		t.Fatalf("checked module filesystem: %v", err)
	}
	file := filepath.Join(dir, "copy_sepia.qml")
	if err := os.WriteFile(file, []byte(`Theme { app.window: "invalid" }`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.ReadFile(c.Files(), "copy_sepia.qml"); err == nil {
		t.Fatal("unchecked bytes reached module loader")
	}
}

// Every theme, shipped or a user's, is drawn in an export theme the exporter has: its own when the
// exporter has one, else the nearest. golib ships themes the exporter has no palette for (nord), and
// the HTML preview of one used to be refused.
func TestEveryThemeHasAnExportThemeThatRenders(t *testing.T) {
	c := Open(t.TempDir())
	exportable := export.Themes()
	for _, name := range themes.Names() {
		got := c.ExportStyle(name)
		if !slices.Contains(exportable, got) {
			t.Errorf("%s maps to %q, which the exporter has no palette for (it has %v)", name, got, exportable)
			continue
		}
		if slices.Contains(exportable, name) && got != name {
			t.Errorf("%s has its own export palette but maps to %s", name, got)
		}
		if _, err := export.Render([]byte("# Title\n\ntext\n"), export.HTML, got); err != nil {
			t.Errorf("%s (as %s) doesn't render: %v", name, got, err)
		}
	}
}
