package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExportMainWritesChosenDestination(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "note.md")
	destination := filepath.Join(directory, "note.html")
	if err := os.WriteFile(sourcePath, []byte("# Heading\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := exportMain(sourcePath, "html", destination, "dark", ""); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || !strings.Contains(string(content), `<h1 id="heading">Heading</h1>`) {
		t.Fatalf("HTML export = %q, %v", content, err)
	}
	if err := exportMain(sourcePath, "text", sourcePath, "light", ""); err == nil {
		t.Fatal("export overwrote its source")
	}
}

// autodoc --export through main: a written file and exit 0; a failed export says why and exits 1.
func TestExportThroughTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "note.md")
	if err := os.WriteFile(src, []byte("# Heading\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "note.html")
	if code, msg := runMain(t, nil, "--export", "html", "--output", out, "--theme", "sepia", src); code != 0 {
		t.Fatalf("exit %d: %s", code, msg)
	}
	if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), "#e6d9b9") {
		t.Fatalf("export = %.80q, %v", b, err)
	}
	if code, msg := runMain(t, nil, "--export", "html", "--output", out, "--theme", "neon", src); code != 1 || !strings.Contains(msg, "autodoc:") {
		t.Fatalf("a bad theme: exit %d, %q", code, msg)
	}
}

// A page exported away from its source reaches the files its relative links name: read from the
// source's directory by default, from --base when it is given; a --base that is not a directory is
// refused.
func TestExportReadsRelativeLinksFromTheSource(t *testing.T) {
	sources, pages, base := t.TempDir(), t.TempDir(), t.TempDir()
	src := filepath.Join(sources, "note.md")
	if err := os.WriteFile(src, []byte("[other](other.md#s)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(pages, "note.html")
	for _, c := range []struct {
		args []string
		dir  string
	}{
		{[]string{"--export", "html", "--output", out, src}, sources},
		{[]string{"--export", "html", "--output", out, "--base", base, src}, base},
	} {
		if code, msg := runMain(t, nil, c.args...); code != 0 {
			t.Fatalf("%v: exit %d: %s", c.args, code, msg)
		}
		want := `href="file://` + filepath.ToSlash(c.dir) + `/other.md#s"`
		if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), want) {
			t.Errorf("%v: the page lacks %s: %v", c.args, want, err)
		}
	}
	if code, msg := runMain(t, nil, "--export", "html", "--output", out, "--base", src, src); code != 1 || !strings.Contains(msg, "is not a directory") {
		t.Errorf("a file as --base: exit %d, %q", code, msg)
	}
}

// autodoc --export=site writes the site docs/site.toml describes, site.toml's theme unless
// --theme is given, and .nojekyll; it refuses --base, an --output inside the docs folder (through a
// symlink too), and an --output with files, an earlier site's too.
func TestExportASiteThroughTheCommandLine(t *testing.T) {
	dir := t.TempDir()
	docs := filepath.Join(dir, "docs")
	for name, text := range map[string]string{
		"site.toml":     "theme = \"sepia\"\n[[section]]\npath = \"README.md\"\nurl = \"\"\n[[section]]\npath = \"guide\"\nurl = \"guide/\"\n",
		"README.md":     "# Hello\n\nSee the [guide](guide/01-a.md).\n",
		"guide/01-a.md": "# A\n",
	} {
		p := filepath.Join(docs, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(dir, "_site")
	if code, msg := runMain(t, nil, "--export", "site", "--output", out, docs); code != 0 {
		t.Fatalf("exit %d: %s", code, msg)
	}
	index, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil || !strings.Contains(string(index), "#e6d9b9") || !strings.Contains(string(index), `href="guide/01-a.html"`) {
		t.Fatalf("index.html = %.200q, %v", index, err)
	}
	for _, f := range []string{"guide/01-a.html", ".nojekyll"} {
		if _, err := os.Stat(filepath.Join(out, filepath.FromSlash(f))); err != nil {
			t.Errorf("%s: %v", f, err)
		}
	}
	dark := filepath.Join(dir, "dark")
	if code, msg := runMain(t, nil, "--export", "site", "--output", dark, "--theme", "dark", docs); code != 0 {
		t.Fatalf("--theme dark: exit %d: %s", code, msg)
	}
	if b, _ := os.ReadFile(filepath.Join(dark, "index.html")); !strings.Contains(string(b), "#1c1c1c") {
		t.Error("--theme dark did not win over site.toml's")
	}
	other := filepath.Join(dir, "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "keep.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(dir, "alias") // docs by another name
	if err := os.Symlink(docs, alias); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(dir, "generated") // an empty folder in docs by another name
	if err := os.Mkdir(filepath.Join(docs, "generated"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(docs, "generated"), generated); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"base", []string{"--export", "site", "--output", out, "--base", dir, docs}, "--base"},
		{"inside", []string{"--export", "site", "--output", filepath.Join(docs, "_site"), docs}, "inside the docs folder"},
		{"through a link", []string{"--export", "site", "--output", filepath.Join(alias, "_site"), docs}, "inside the docs folder"},
		{"a link into docs", []string{"--export", "site", "--output", generated, docs}, "inside the docs folder"},
		{"docs through a link", []string{"--export", "site", "--output", filepath.Join(docs, "_site"), alias}, "inside the docs folder"},
		{"files", []string{"--export", "site", "--output", other, docs}, "not empty"},
		{"an earlier site", []string{"--export", "site", "--output", out, docs}, "not empty"},
	} {
		if code, msg := runMain(t, nil, c.args...); code != 1 || !strings.Contains(msg, c.want) {
			t.Errorf("%s: exit %d, %q; want 1 saying %q", c.name, code, msg, c.want)
		}
	}
}
