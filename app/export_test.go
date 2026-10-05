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
	if err != nil || !strings.Contains(string(content), "<h1>Heading</h1>") {
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
	if b, err := os.ReadFile(out); err != nil || !strings.Contains(string(b), "#f4ecd8") {
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
