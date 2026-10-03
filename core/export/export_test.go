package export_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/export"
)

func TestHTMLIsOfflineSafeAndThemed(t *testing.T) {
	source := []byte("# Hello\n\n<script>alert(1)</script>\n\n![remote](https://example.com/image.png)\n\n```go\nfmt.Println(1)\n```\n")
	for _, theme := range []string{"light", "dark"} {
		content, err := export.Render(source, export.HTML, theme)
		if err != nil {
			t.Fatal(err)
		}
		for _, expected := range []string{"<!doctype html>", "color-scheme:" + theme, "Content-Security-Policy", "&lt;script&gt;", "<h1>Hello</h1>", "fmt.Println(1)"} {
			if !bytes.Contains(content, []byte(expected)) {
				t.Errorf("%s HTML lacks %q", theme, expected)
			}
		}
		if bytes.Contains(content, []byte("<script>")) || bytes.Contains(content, []byte("<img ")) || bytes.Contains(content, []byte("https://example.com/image.png")) {
			t.Errorf("%s HTML retained executable markup or a remote image", theme)
		}
	}
}

func TestTextStripsMarkdownStructure(t *testing.T) {
	content, err := export.Render([]byte("# Title\n\nThis is **important** and [linked](https://example.com).\n"), export.Text, "dark")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "Title\n") || !strings.Contains(string(content), "This is important and linked.") || strings.Contains(string(content), "**") {
		t.Fatalf("plain text = %q", content)
	}
}

func TestMermaidRendersAsOfflineSVG(t *testing.T) {
	source := []byte("```mermaid\nflowchart TD\nA[Start] -->|yes| B[Done]\n```\n")
	content, err := export.Render(source, export.HTML, "light")
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{`<svg class="diagram"`, "Start", "Done", "yes", `fill="#2e2a24"`} {
		if !bytes.Contains(content, []byte(expected)) {
			t.Errorf("SVG lacks %q", expected)
		}
	}
	if bytes.Contains(content, []byte("language-mermaid")) || bytes.Contains(content, []byte("<script")) {
		t.Errorf("Mermaid export retained source fence or executable script")
	}
}

func TestWriteFileReplacesAtomically(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "page.html")
	if err := export.WriteFile(destination, []byte("old")); err != nil {
		t.Fatal(err)
	}
	if err := export.WriteFile(destination, []byte("new")); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil || string(content) != "new" {
		t.Fatalf("written = %q, %v", content, err)
	}
}
