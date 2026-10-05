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

	"github.com/yongjohnlee80/golib/tui/widget"

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
	browser, ok := widget.HTMLRasterizer()
	if !ok {
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
	out := dumpDOM(t, browser, dir, path)
	// the page as drawn, without its scripts, whose own text names mermaid's errors
	dom := regexp.MustCompile(`(?s)<script>.*?</script>`).ReplaceAllString(out, "")
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

// dumpDOM is the page at path as the browser leaves it once its scripts have settled, offline: every
// host resolves nowhere and every request goes to a proxy that is not there. dir holds the
// browser's profile; flags are added to the browser's own.
func dumpDOM(t *testing.T, browser, dir, path string, flags ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	args := append([]string{"--headless=new", "--disable-gpu", "--no-first-run", "--disable-extensions",
		"--proxy-server=127.0.0.1:9", "--host-resolver-rules=MAP * ~NOTFOUND", "--user-data-dir=" + filepath.Join(dir, "profile")}, flags...)
	out, err := exec.CommandContext(ctx, browser, append(args, "--virtual-time-budget=10000", "--dump-dom", "file://"+path)...).Output()
	if err != nil {
		var stderr []byte
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = ee.Stderr
		}
		// the browser's own sandbox must run here: a runner that forbids user namespaces refuses
		// it, and says so. That is read from this run, not a probe before it: the rasterizer's
		// probe had its 15-second budget, which a cold browser on a loaded runner outlasted, and
		// was killed for it (CI, 2026-10-04 and -05)
		if strings.Contains(string(stderr), "No usable sandbox") {
			t.Skip("the installed browser has no usable sandbox here")
		}
		t.Fatalf("the browser: %v: %s", err, stderr)
	}
	return string(out)
}

// fileURL is the file: URL of an absolute path whose every byte is safe in a URL, written by hand.
func fileURL(path string) string { return "file://" + filepath.ToSlash(path) }

// With a base, a relative link is the file: URL of the file it names there: its escapes decoded, the
// path encoded again (a space, a '#', a byte past ASCII), its query and fragment kept; an absolute
// path is its own file: URL. A URL (another host's among them), a #fragment, a wikilink and an
// image are as they were without one, and without one every link is as written.
func TestRelativeLinksResolveAgainstTheBase(t *testing.T) {
	parent := t.TempDir()
	base := filepath.Join(parent, "my docs")
	source := []byte("[a](notes/a.md#intro) [up](../x.md#sec) [space](<sub dir/b c.md>) [korean](노트.md#제목)\n" +
		"[hash](a%23b.md) [percent](100%.md) [query](c.md?x=1&y=2#f) [abs](/etc/hosts) [ref][r]\n\n" +
		"[web](https://example.com/x#y) [host](//example.com/z) [frag](#local) [mail](mailto:a@example.com) [[Other note]] ![pic](pic.png)\n\n" +
		"[r]: ./ref.md\n")
	content, err := export.RenderWith(source, export.HTML, "light", export.Options{Base: base})
	if err != nil {
		t.Fatal(err)
	}
	page := string(content)
	docs := fileURL(parent) + "/my%20docs"
	for _, want := range []string{
		`href="` + docs + `/notes/a.md#intro"`,
		`href="` + fileURL(parent) + `/x.md#sec"`,
		`href="` + docs + `/sub%20dir/b%20c.md"`,
		`href="` + docs + `/%EB%85%B8%ED%8A%B8.md#%EC%A0%9C%EB%AA%A9"`,
		`href="` + docs + `/a%23b.md"`,
		`href="` + docs + `/100%25.md"`,
		`href="` + docs + `/c.md?x=1&amp;y=2#f"`,
		`href="file:///etc/hosts"`,
		`href="` + docs + `/ref.md"`,
		`href="https://example.com/x#y"`,
		`href="//example.com/z"`,
		`href="#local"`,
		`href="mailto:a@example.com"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
	if strings.Contains(page, "autodoc-link-") || strings.Contains(page, "<img") || strings.Contains(page, "pic.png") {
		t.Error("the page kept a placeholder or an image")
	}
	plain, err := export.Render(source, export.HTML, "light")
	if err != nil {
		t.Fatal(err)
	}
	wikilink := regexp.MustCompile(`<a class="wikilink"[^>]*>`)
	if got, want := wikilink.FindString(page), wikilink.FindString(string(plain)); got == "" || got != want {
		t.Errorf("the wikilink is %q with a base, %q without", got, want)
	}
	for _, want := range []string{`href="notes/a.md#intro"`, `href="../x.md#sec"`, `href="sub%20dir/b%20c.md"`} {
		if !strings.Contains(string(plain), want) {
			t.Errorf("without a base, the page lacks %s", want)
		}
	}
}

// A relative base is read from the working directory.
func TestARelativeBaseIsReadFromTheWorkingDirectory(t *testing.T) {
	content, err := export.RenderWith([]byte("[a](a.md)\n"), export.HTML, "light", export.Options{Base: "."})
	if err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := `href="` + fileURL(wd) + `/a.md"`; !strings.Contains(string(content), want) {
		t.Errorf("the page lacks %s", want)
	}
}

// In a browser, a page written away from its source follows a resolved link to the file it names,
// fragment and all, though the page's policy loads nothing: a link is navigation, not a load. The
// test's own page frames the export, clicks its link and writes down where the frame went and what
// it shows.
func TestAResolvedLinkIsFollowedInABrowser(t *testing.T) {
	browser, ok := widget.HTMLRasterizer()
	if !ok {
		t.Skip("no headless Chromium or Chrome")
	}
	dir := t.TempDir()
	sources := filepath.Join(dir, "source docs")
	if err := os.MkdirAll(filepath.Join(sources, "sub dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sources, "sub dir", "노트 #1.md"), []byte("# Target\n\nthe target file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	content, err := export.RenderWith([]byte("[the target](<sub dir/노트 %231.md#sec>)\n"), export.HTML, "dark", export.Options{Base: sources})
	if err != nil {
		t.Fatal(err)
	}
	pages := filepath.Join(dir, "pages")
	if err := os.Mkdir(pages, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pages, "page.html"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	frame := `<!doctype html><html><body><iframe id="f" src="page.html"></iframe><pre id="r"></pre><script>` +
		`var f=document.getElementById("f"),r=document.getElementById("r"),loads=0;` +
		`f.onload=function(){try{var d=f.contentDocument;if(++loads==1){d.querySelector("a").click();return}` +
		`r.textContent="at "+f.contentWindow.location.href+" shows "+d.body.innerText}catch(e){r.textContent="failed: "+e}}` +
		`</script></body></html>`
	if err := os.WriteFile(filepath.Join(pages, "frame.html"), []byte(frame), 0o600); err != nil {
		t.Fatal(err)
	}
	// the frame reads the export's document, which a file: page may only with this flag
	out := dumpDOM(t, browser, dir, filepath.Join(pages, "frame.html"), "--allow-file-access-from-files")
	want := "at " + fileURL(dir) + "/source%20docs/sub%20dir/%EB%85%B8%ED%8A%B8%20%231.md#sec shows # Target\n\nthe target file"
	if !strings.Contains(out, want) {
		t.Fatalf("the browser did not follow the link to the target: %s", out)
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
