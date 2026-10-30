package export_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/export"
	mermaidparse "github.com/yongjohnlee80/golib/parse/mermaid"
)

// moreLink is a tutorial's "More:" line's link into the Reference Book, one folder deeper from a
// translation's page.
var moreLink = regexp.MustCompile(`\]\((?:\.\./)+reference/([^)]+)\)`)

// TestTheTutorialsAreShortWithDiagramsThatDraw: docs/tutorials/'s pages (README.md, the index, is
// none) each keep the rules of both books (checkBookPage), open with their heading, draw each
// mermaid fence (it parses, and the HTML export draws it), and end with a "More:" line whose links
// reach Reference Book chapters. The index lists every page. Each translation
// (docs/tutorials/<tag>/, docs/decisions/editor.md#six-languages) keeps the same rules, its "More:"
// in its language.
func TestTheTutorialsAreShortWithDiagramsThatDraw(t *testing.T) {
	checkTutorialPages(t, "../../docs/tutorials", "More: ", "../reference/")
	langs, err := os.ReadDir("../../docs/tutorials")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range langs {
		if d.IsDir() {
			t.Run(d.Name(), func(t *testing.T) {
				checkTutorialPages(t, filepath.Join("../../docs/tutorials", d.Name()), "", "../../reference/")
			})
		}
	}
}

// checkTutorialPages is the cell for one language's pages in dir: their last line opens with more
// ("" for any) and links to Reference chapters through ref.
func checkTutorialPages(t *testing.T, dir, more, ref string) {
	t.Helper()
	pages, err := filepath.Glob(filepath.Join(dir, "[0-9]*.md"))
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(pages)
	if len(pages) < 11 {
		t.Fatalf("%d tutorials; want at least 11", len(pages))
	}
	index, err := os.ReadFile("../../docs/tutorials/README.md")
	if err != nil {
		t.Fatal(err)
	}
	verbs := registeredVerbs(t)
	for _, p := range pages {
		base := filepath.Base(p)
		if want := "](./" + base + ")"; !strings.Contains(string(index), want) {
			t.Errorf("the index does not list %s", base) // a translation's name is English's
		}
		t.Run(base, func(t *testing.T) {
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			text := string(src)
			checkBookPage(t, base, text, verbs)
			if !strings.HasPrefix(text, "# ") {
				t.Errorf("%s does not open with its heading", base)
			}
			fences := strings.Split(text, "```mermaid\n")[1:]
			for _, f := range fences {
				diagram, _, ok := strings.Cut(f, "```")
				if !ok {
					t.Fatalf("%s: a mermaid fence is not closed", base)
				}
				if _, err := mermaidparse.Parse(diagram); err != nil {
					t.Errorf("%s: a diagram does not parse: %v\n%s", base, err, diagram)
				}
			}
			html, err := export.RenderWith(src, export.HTML, "dark", export.Options{Document: p})
			if err != nil {
				t.Fatalf("%s: export: %v", base, err)
			}
			if got := strings.Count(string(html), `<pre class="mermaid">`); got != len(fences) {
				t.Errorf("%s: the export drew %d diagrams; the page has %d", base, got, len(fences))
			}
			trimmed := strings.TrimSpace(text)
			last := trimmed[strings.LastIndex(trimmed, "\n")+1:]
			links := moreLink.FindAllStringSubmatch(last, -1)
			if !strings.HasPrefix(last, more) || len(links) == 0 {
				t.Fatalf("%s: its last line %q is not a More: line into the Reference Book", base, last)
			}
			for _, l := range links {
				if !strings.Contains(last, "]("+ref+l[1]+")") {
					t.Errorf("%s: More: reaches %s by the wrong path from this folder (want %s)", base, l[1], ref)
				}
				if _, err := os.Stat(filepath.Join("../../docs/reference", l[1])); err != nil {
					t.Errorf("%s: More: links to %s, which is not a Reference chapter", base, l[1])
				}
			}
		})
	}
}
