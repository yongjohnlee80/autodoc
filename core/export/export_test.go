package export_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/export/mermaid"
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

// A ```mermaid block becomes a pre.mermaid holding its source, escaped, and the page carries the
// vendored mermaid and the script that starts it, allowed by their digests and nothing else; a
// page without a diagram has no script.
func TestMermaidIsDrawnByTheVendoredScript(t *testing.T) {
	source := []byte("```mermaid\nflowchart TD\nA[\"</pre><script>alert(1)</script>\"] -->|yes| B[Done]\n```\n")
	content, err := export.Render(source, export.HTML, "light")
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	for _, expected := range []string{`<pre class="mermaid">flowchart TD`, `&lt;/pre&gt;&lt;script&gt;alert(1)&lt;/script&gt;`, `data-mermaid="default"`,
		"script-src 'sha256-" + digest(mermaid.Script()), `globalThis["mermaid"]`, "mermaid.run("} {
		if !strings.Contains(page, expected) {
			t.Errorf("the page lacks %q", expected)
		}
	}
	if strings.Contains(page, "language-mermaid") || strings.Contains(page, "<script>alert(1)") {
		t.Error("the page kept the fence, or let the diagram's text out as markup")
	}
	if n := strings.Count(page, "<script>"); n != 2 {
		t.Errorf("the page has %d scripts, want mermaid and its start", n)
	}
	plain, err := export.Render([]byte("# No diagram\n"), export.HTML, "light")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plain), "<script") || strings.Contains(string(plain), "script-src") {
		t.Error("a page without a diagram carries a script")
	}
}

func digest(s string) string {
	sum := sha256.Sum256([]byte(s))
	return base64.StdEncoding.EncodeToString(sum[:])
}

// In a browser, offline and under the page's own policy, mermaid draws what the hand-written
// renderer could not: a flowchart with edge text and line breaks in its labels, and a state
// diagram. The page as the browser leaves it holds each one's SVG, in the page's theme, and no
// error.
func TestMermaidDrawsInABrowser(t *testing.T) {
	browser := ""
	for _, name := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if p, err := exec.LookPath(name); err == nil {
			browser = p
			break
		}
	}
	if browser == "" {
		t.Skip("no headless Chromium or Chrome")
	}
	source := []byte("# Diagrams\n\n```mermaid\nflowchart TD\n  A[\"event arrives<br/>at node N\"] --> B{\"pointer<br/>disabled?\"}\n  B -- yes --> P[\"skip N\"]\n  B -- no --> C[resolve]\n```\n\n" +
		"```mermaid\nstateDiagram-v2\n  [*] --> Idle\n  Idle --> Armed: press<br/>MenuArm\n  Armed --> Idle: release\n```\n")
	content, err := export.Render(source, export.HTML, "dark")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "page.html")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, browser, "--headless=new", "--disable-gpu", "--no-first-run", "--disable-extensions",
		"--proxy-server=127.0.0.1:9", "--host-resolver-rules=MAP * ~NOTFOUND", "--user-data-dir="+filepath.Join(dir, "profile"),
		"--virtual-time-budget=10000", "--dump-dom", "file://"+path).Output()
	if err != nil {
		if strings.Contains(err.Error(), "sandbox") {
			t.Skip("the installed browser has no usable sandbox here")
		}
		t.Fatal(err)
	}
	// the page as drawn, without its scripts, whose own text names mermaid's errors
	dom := regexp.MustCompile(`(?s)<script>.*?</script>`).ReplaceAllString(string(out), "")
	// fill:#ccc is mermaid's dark theme, which only the page's start script asks for: mermaid left
	// to start itself draws in its default theme
	for _, want := range []string{`aria-roledescription="flowchart-v2"`, `aria-roledescription="stateDiagram"`, "event arrives", "pointer", "MenuArm", "fill:#ccc"} {
		if !strings.Contains(dom, want) {
			t.Errorf("the drawn page lacks %q", want)
		}
	}
	if strings.Contains(dom, "Syntax error") || strings.Contains(dom, `aria-roledescription="error"`) {
		t.Error("mermaid drew an error")
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
