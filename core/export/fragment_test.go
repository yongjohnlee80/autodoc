package export_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/html"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// A FRAGMENT's cells: the body alone, raw HTML escaped, embeds read in one directory, the caller's
// URL rule applied last, the same body RenderWith's page holds, and no GUI or daemon underneath.

// fragmentSource has a frontmatter title, headings, code, a table, a Mermaid fence, a Vega-Lite
// fence, a CSV fence and links of several kinds.
const fragmentSource = "---\ntitle: \"The handbook\"\ndate: 2026-11-20\n---\n\n# Getting started\n\n" +
	"Read [the guide](guide.md), [the site](https://example.com/a?b=1#c), [[Other note]] and #tagged.\n\n" +
	"## Code\n\n```go\nfmt.Println(\"a<b\")\n```\n\n| a | b |\n|:--|--:|\n| 1 | 2 |\n\n" +
	"## Code\n\n```mermaid\ngraph TD\n  a --> b\n```\n\n```vega-lite\n{\"mark\": \"bar\"}\n```\n\n```csv\nh,v\nx,1\n```\n"

func fragment(t *testing.T, source string, opts export.FragmentOptions) (string, export.FragmentInfo) {
	t.Helper()
	if opts.Theme == "" {
		opts.Theme = "light"
	}
	body, info, err := export.Fragment([]byte(source), opts)
	if err != nil {
		t.Fatal(err)
	}
	return string(body), info
}

// tags are the body's start and self-closing tags, read as a browser's tokenizer reads them.
func tags(t *testing.T, body string) []html.Token {
	t.Helper()
	tz := html.NewTokenizer([]byte(body), html.Limits{MaxSource: 1 << 30, MaxAttr: 1 << 30})
	var out []html.Token
	for {
		tok, ok := tz.Next()
		if !ok {
			break
		}
		if tok.Kind == html.StartTag || tok.Kind == html.SelfClosing {
			out = append(out, tok)
		}
	}
	if err := tz.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// A fragment is the body alone: no html, head, body, style or script element and no style
// attribute, the Mermaid fence a <pre class="mermaid"> for the page's script to draw. Its info:
// the frontmatter's title (the first heading's without one), every heading with the ID its tag
// carries (a repeated one numbered), the diagram, Mermaid's theme for the palette, and the class
// names the body uses. An unknown theme is refused, as RenderWith refuses it.
func TestAFragmentIsTheBodyAlone(t *testing.T) {
	body, info := fragment(t, fragmentSource, export.FragmentOptions{})
	for _, tok := range tags(t, body) {
		if slices.Contains([]string{"html", "head", "body", "style", "script", "link", "meta"}, tok.Name) {
			t.Errorf("the fragment has a <%s>:\n%s", tok.Name, body)
		}
		for _, a := range tok.Attrs {
			if a.Name == "style" {
				t.Errorf("<%s> has a style attribute:\n%s", tok.Name, body)
			}
		}
	}
	for _, want := range []string{`<h1 id="getting-started">Getting started</h1>`, `<h2 id="code-1">Code</h2>`,
		"<pre class=\"mermaid\">graph TD\n  a --&gt; b\n</pre>", `<code class="language-go">`, `<pre class="vega-lite">`} {
		if !strings.Contains(body, want) {
			t.Errorf("the fragment lacks %q:\n%s", want, body)
		}
	}
	want := export.FragmentInfo{
		Title:        "The handbook",
		Headings:     []export.Heading{{Level: 1, Text: "Getting started", ID: "getting-started"}, {Level: 2, Text: "Code", ID: "code"}, {Level: 2, Text: "Code", ID: "code-1"}},
		HasMermaid:   true,
		MermaidTheme: "default",
	}
	got := info
	got.Classes = nil
	if !reflect.DeepEqual(got, want) {
		t.Errorf("info %+v\nwant %+v", got, want)
	}
	for _, c := range []string{"mermaid", "language-go", "vega-lite", "embed", "wikilink", "tag"} {
		if !slices.Contains(info.Classes, c) {
			t.Errorf("the classes %v lack %q", info.Classes, c)
		}
	}
	if !slices.IsSorted(info.Classes) {
		t.Errorf("the classes are not sorted: %v", info.Classes)
	}
	if _, info := fragment(t, "Some text.\n\n### First\n", export.FragmentOptions{Theme: "dark"}); info.Title != "First" || info.HasMermaid || info.MermaidTheme != "dark" {
		t.Errorf("no frontmatter, no diagram, dark: %+v", info)
	}
	if _, _, err := export.Fragment([]byte("x"), export.FragmentOptions{Theme: "neon"}); err == nil {
		t.Error("an unknown theme was not refused")
	}
}

// Raw HTML in the source, as a block and inline (a script, a style, an iframe, an event handler, a
// javascript: link, a form), is text in the fragment: no such element and no on* attribute reaches
// it. FragmentOptions has no way to let HTML through: its fields are the four this cell names, so
// an option added later is looked at here first. The mutant, named here: the renderer's unsafe
// mode (raw HTML written as it is).
func TestAFragmentKeepsRawHTMLEscaped(t *testing.T) {
	source := "<script>alert(1)</script>\n\n<style>body{display:none}</style>\n\n<iframe src=\"https://evil.example\"></iframe>\n\n" +
		"Inline <img src=x onerror=alert(2)> and <a href=\"javascript:alert(3)\">a link</a> and <b onclick=\"x()\">bold</b>.\n\n" +
		"<form action=\"https://evil.example\"><input name=p></form>\n\n[a link](javascript:alert(4))\n"
	body, _ := fragment(t, source, export.FragmentOptions{})
	for _, tok := range tags(t, body) {
		if slices.Contains([]string{"script", "style", "iframe", "img", "form", "input", "b"}, tok.Name) {
			t.Errorf("raw HTML reached the fragment as a <%s>:\n%s", tok.Name, body)
		}
		for _, a := range tok.Attrs {
			if strings.HasPrefix(a.Name, "on") || strings.Contains(strings.ToLower(a.Value), "javascript:") {
				t.Errorf("<%s %s=%q> reached the fragment:\n%s", tok.Name, a.Name, a.Value, body)
			}
		}
	}
	if !strings.Contains(body, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Errorf("the script is not shown as text:\n%s", body)
	}
	var fields []string
	for typ, i := reflect.TypeFor[export.FragmentOptions](), 0; i < typ.NumField(); i++ {
		fields = append(fields, typ.Field(i).Name)
	}
	if want := []string{"Theme", "Chart", "Embeds", "Link"}; !slices.Equal(fields, want) {
		t.Errorf("FragmentOptions has %v, want %v: an option that lets raw HTML through is not to be one", fields, want)
	}
}

// fragmentTree is a directory of embeds with files outside it beside it, and links that point out:
// root/secret.mmd and root/secret.png outside; root/embeds/ holding a diagram, an image, a table in
// a subfolder, an image whose name needs escaping, and symlinks out (a file, an image, a folder).
func fragmentTree(t *testing.T) (root, embeds string) {
	t.Helper()
	root = t.TempDir()
	embeds = filepath.Join(root, "embeds")
	for name, text := range map[string]string{
		"secret.mmd":           "graph TD\n  secret --> out\n",
		"secret.png":           "\x89PNG secret",
		"embeds/near.mmd":      "graph TD\n  near --> here\n",
		"embeds/pic.png":       "\x89PNG near",
		"embeds/my pic.png":    "\x89PNG spaced",
		"embeds/sub/deep.csv":  "h,v\ndeep,1\n",
		"embeds/sub/chart.png": "\x89PNG chart",
	} {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"link.mmd": "secret.mmd", "link.png": "secret.png", "linkdir": "."} {
		if err := os.Symlink(filepath.Join(root, target), filepath.Join(embeds, link)); err != nil {
			t.Fatal(err)
		}
	}
	return root, embeds
}

// Embeds are read in the Embeds directory and nowhere else: a path that climbs out (written
// plainly or escaped), an absolute path, a URL, and a symlink out (to a file, an image, or a folder
// above) are each drawn as their alt text, and nothing of the files outside is in the fragment. An
// image there is linked by its path in the directory, escaped, never inlined. With no Embeds, every
// embed is its alt text. The mutants, named here: the embeds read in the directory's parent, and an
// image inlined as a data: URI.
func TestAFragmentReadsEmbedsOnlyFromItsDirectory(t *testing.T) {
	root, embeds := fragmentTree(t)
	out := map[string]string{
		"up": "../secret.mmd", "escaped": "%2e%2e/secret.mmd", "slashed": "..%2Fsecret.mmd", "abs": filepath.ToSlash(filepath.Join(root, "secret.mmd")),
		"url": "https://example.com/a.mmd", "link": "link.mmd", "linkpng": "link.png", "linkdir": "linkdir/secret.mmd", "imgup": "../secret.png",
	}
	var source strings.Builder
	for alt, dest := range out {
		source.WriteString("![" + alt + "](" + dest + ")\n\n")
	}
	source.WriteString("![near](near.mmd)\n\n![deep](sub/deep.csv)\n\nSee ![pic](pic.png), ![spaced](<my pic.png>) and ![sub](sub/chart.png).\n")
	body, info := fragment(t, source.String(), export.FragmentOptions{Embeds: embeds})
	for alt := range out {
		if !strings.Contains(body, "<em>"+alt+"</em>") {
			t.Errorf("%s (%s) was not drawn as its alt text:\n%s", alt, out[alt], body)
		}
	}
	if strings.Contains(body, "secret") || strings.Contains(body, "data:") {
		t.Errorf("a file outside the embeds' directory, or a data: URI, is in the fragment:\n%s", body)
	}
	for _, want := range []string{"near --&gt; here", "<td>deep</td>", `<img src="pic.png" alt="pic" />`,
		`<img src="my%20pic.png" alt="spaced" />`, `<img src="sub/chart.png" alt="sub" />`} {
		if !strings.Contains(body, want) {
			t.Errorf("the fragment lacks %q:\n%s", want, body)
		}
	}
	if !info.HasMermaid {
		t.Error("the embedded diagram is not counted")
	}
	if body, _ := fragment(t, "![near](near.mmd)\n\n![pic](pic.png)\n", export.FragmentOptions{}); !strings.Contains(body, "<em>near</em>") || !strings.Contains(body, "<em>pic</em>") {
		t.Errorf("with no Embeds an embed was drawn:\n%s", body)
	}
	if _, _, err := export.Fragment([]byte("x"), export.FragmentOptions{Theme: "light", Embeds: filepath.Join(root, "missing")}); err == nil {
		t.Error("an Embeds directory that does not open was not refused")
	}
}

// urlAttributes are the attributes a URL can be in, for the cells to look at every one.
var urlAttributes = []string{"href", "xlink:href", "src", "srcset", "poster", "data-href", "action", "formaction", "cite", "background", "longdesc", "ping", "data"}

// Link is applied after everything else, to every URL in the finished body: a link as written, an
// absolute one, a wikilink, a tag, an embedded image's generated path, and an href in a chart's
// SVG. Each is handed to Link once, as the body holds it, and every URL attribute of the fragment
// is Link's answer. A Link that refuses drops each attribute: the links keep their text, the image
// its alt, and no URL attribute is left. The mutants, named here: Link not applied, and src left
// out of the attributes it is applied to.
func TestLinkIsAppliedLastToEveryURL(t *testing.T) {
	_, embeds := fragmentTree(t)
	source := "# Top\n\nRead [the guide](guide.md), [the site](https://example.com/a), [top](#top), [[Other note]], #tagged and <https://auto.example>.\n\n" +
		"See ![pic](pic.png).\n\n```vega-lite\n{\"mark\": \"bar\"}\n```\n"
	chart := func([]byte, string) (string, error) {
		return `<svg xmlns="http://www.w3.org/2000/svg"><a xlink:href="#bar"><rect width="1" height="1"/></a></svg>`, nil
	}
	var seen []string
	mapped := func(href string) (string, bool) {
		seen = append(seen, href)
		return "https://site.example/m/" + href, true
	}
	body, _ := fragment(t, source, export.FragmentOptions{Embeds: embeds, Chart: chart, Link: mapped})
	for _, want := range []string{"guide.md", "https://example.com/a", "#top", "pic.png", "#bar"} {
		if !slices.Contains(seen, want) {
			t.Errorf("Link was not handed %q (it was handed %q)", want, seen)
		}
	}
	urls := 0
	for _, tok := range tags(t, body) {
		for _, a := range tok.Attrs {
			if slices.Contains(urlAttributes, a.Name) {
				urls++
				if !strings.HasPrefix(a.Value, "https://site.example/m/") {
					t.Errorf("<%s %s=%q> did not go through Link:\n%s", tok.Name, a.Name, a.Value, body)
				}
			}
		}
	}
	if urls != len(seen) || urls < 8 {
		t.Errorf("%d URL attributes in the fragment, %d handed to Link (want the same, 8 at least): %q", urls, len(seen), seen)
	}
	refused, _ := fragment(t, source, export.FragmentOptions{Embeds: embeds, Chart: chart, Link: func(string) (string, bool) { return "", false }})
	for _, tok := range tags(t, refused) {
		for _, a := range tok.Attrs {
			if slices.Contains(urlAttributes, a.Name) {
				t.Errorf("a refused URL was kept: <%s %s=%q>", tok.Name, a.Name, a.Value)
			}
		}
	}
	for _, want := range []string{">the guide</a>", ">the site</a>", `<img alt="pic" />`} {
		if !strings.Contains(refused, want) {
			t.Errorf("with every URL refused, the fragment lacks %q:\n%s", want, refused)
		}
	}
}

// Fragment and RenderWith draw one body: a page RenderWith writes holds, between its <body> and
// its diagrams' script, exactly the fragment of the same source (no Link; embeds of diagrams and
// tables, which both draw alike). The mutant, named here: a body path of Fragment's own (its
// headings without their IDs).
func TestAFragmentIsTheBodyRenderWithsPageHolds(t *testing.T) {
	_, embeds := fragmentTree(t)
	source := fragmentSource + "\n![near](near.mmd)\n\n![deep](sub/deep.csv)\n"
	for _, theme := range []string{"light", "dark"} {
		body, _ := fragment(t, source, export.FragmentOptions{Theme: theme, Embeds: embeds})
		page, err := export.RenderWith([]byte(source), export.HTML, theme, export.Options{Document: filepath.Join(embeds, "doc.md")})
		if err != nil {
			t.Fatal(err)
		}
		_, after, ok := bytes.Cut(page, []byte("</style></head><body>\n"))
		inner, _, ok2 := bytes.Cut(after, []byte("\n<script>"))
		if !ok || !ok2 {
			t.Fatalf("%s: the page's body is not where it was:\n%s", theme, page)
		}
		if string(inner) != body {
			t.Errorf("%s: the page's body and the fragment differ:\npage\n%s\nfragment\n%s", theme, inner, body)
		}
	}
}

// core/export builds on golib's parsers and its own small packages, and nothing of a GUI, a TUI, a
// daemon or a database: a caller that lays out its own page imports it without them. Every package
// it depends on outside the standard library is one of these.
func TestTheExporterNeedsNoGUIOrDaemon(t *testing.T) {
	const mod = "github.com/yongjohnlee80/autodoc/"
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}", mod+"core/export").Output()
	if err != nil {
		t.Skipf("go list core/export: %v", err)
	}
	own := []string{mod + "core/export", mod + "core/export/mermaid", mod + "core/kind", mod + "core/lab", mod + "core/outline"}
	golib := []string{"parse", "highlight", "indent", "errs", "streamcache", "logger", "vfs"}
	allowed := func(p string) bool {
		if slices.Contains(own, p) {
			return true
		}
		if rest, ok := strings.CutPrefix(p, "github.com/yongjohnlee80/golib/"); ok {
			return slices.ContainsFunc(golib, func(g string) bool { return rest == g || strings.HasPrefix(rest, g+"/") })
		}
		return slices.ContainsFunc([]string{"github.com/BurntSushi/toml", "golang.org/x/text", "golang.org/x/sys"}, func(m string) bool {
			return p == m || strings.HasPrefix(p, m+"/")
		})
	}
	n := 0
	for _, p := range strings.Fields(string(out)) {
		n++
		if !allowed(p) {
			t.Errorf("core/export depends on %s", p)
		}
	}
	if n < len(own) {
		t.Errorf("go list named %d packages, want %d at least: %s", n, len(own), out)
	}
}
