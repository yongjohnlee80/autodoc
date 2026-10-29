package export_test

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// fixture is a docs folder: a site.toml and the files, by their paths under it.
func fixture(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, text := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// build is Site over docs, its pages collected by their paths.
func build(t *testing.T, docs string, opts export.SiteOptions) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := export.Site(docs, opts, func(rel string, page []byte) error {
		if _, dup := out[rel]; dup {
			t.Errorf("%s written twice", rel)
		}
		out[rel] = string(page)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

const siteMap = `title = "Docs"
source = "https://example.com/blob/main/docs/"
[[section]]
path = "README.md"
url = ""
title = "Introduction"
[[section]]
path = "tutorials"
url = "tutorials/"
title = "Tutorials"
[[section]]
path = "reference"
url = "reference/"
title = "Reference"
[[section]]
path = "lua"
url = "reference/lua/"
title = "Lua API"
[[link]]
title = "Plugins"
href = "https://example.com/registry"
`

func siteDocs(t *testing.T) string {
	return fixture(t, map[string]string{
		"site.toml":                   siteMap,
		"README.md":                   "# Welcome\n\nStart with the [tutorials](tutorials/README.md).\n",
		"tutorials/README.md":         "# Tutorials\n\nThe index.\n",
		"tutorials/01-first.md":       "# First\n\n[b](../reference/02-b.md#x) [ops](../ops/x.md) [web](https://example.com/p) [ref](../reference/) [top](#top) [q](../reference/02-b.md?v=1#y) [sp](../ops/my%20notes.md) [pr](//example.com/x)\n",
		"tutorials/02-second.md":      "# Second\n\n```vega-lite\n{\"mark\":\"bar\"}\n```\n",
		"reference/README.md":         "---\ntitle: x\n---\n# The Reference\n",
		"reference/02-b.md":           "# B\n\n![flow](d.mmd)\n\n![rows](t.csv)\n\n![sales](c.chart.json)\n",
		"reference/02-b/d.mmd":        "flowchart LR\n  A --> B\n",
		"reference/02-b/t.csv":        "h,v\na,1\n",
		"reference/02-b/c.chart.json": `{"mark":"bar","data":{"values":[{"a":"x","b":1}]},"encoding":{"x":{"field":"a","type":"nominal"},"y":{"field":"b","type":"quantitative"}}}`,
		"lua/README.md":               "# Lua\n",
		"lua/reference.md":            "# The Lua API\n",
		"ops/x.md":                    "# Not published\n",
	})
}

// The site writes each section's pages at their paths, a folder's README.md as its index.html,
// the file section at the root, .nojekyll last, and nothing site.toml does not name.
func TestASiteWritesItsSectionsPages(t *testing.T) {
	out := build(t, siteDocs(t), export.SiteOptions{})
	var got []string
	for k := range out {
		got = append(got, k)
	}
	slices.Sort(got)
	want := []string{".nojekyll", "index.html", "reference/02-b.html", "reference/index.html", "reference/lua/index.html",
		"reference/lua/reference.html", "tutorials/01-first.html", "tutorials/02-second.html", "tutorials/index.html"}
	if !slices.Equal(got, want) {
		t.Fatalf("pages %q, want %q", got, want)
	}
	if strings.Contains(out["reference/index.html"], "title: x") {
		t.Error("the frontmatter is drawn")
	}
}

// A link to a published page reaches it relative to this one, its #fragment and ?query kept; a
// folder's link reaches its index; another file under docs goes to source, escaped once; anything
// else (a scheme, a protocol-relative URL, a fragment) stays.
func TestASitesLinksReachItsPages(t *testing.T) {
	page := build(t, siteDocs(t), export.SiteOptions{})["tutorials/01-first.html"]
	for _, want := range []string{
		`href="../reference/02-b.html#x"`,
		`href="https://example.com/blob/main/docs/ops/x.md"`,
		`href="https://example.com/p"`,
		`href="../reference/index.html"`,
		`href="#top"`,
		`href="../reference/02-b.html?v=1#y"`,
		`href="https://example.com/blob/main/docs/ops/my%20notes.md"`,
		`href="//example.com/x"`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %s in\n%s", want, page)
		}
	}
	if root := build(t, siteDocs(t), export.SiteOptions{})["index.html"]; !strings.Contains(root, `href="tutorials/index.html"`) {
		t.Errorf("the root's link to the tutorials does not reach their index:\n%s", root)
	}
}

// A site page draws its embeds as an export does: the diagram, the table, and the chart through
// Chart, or as its spec without one; a vega-lite fence the same.
func TestASitesEmbedsAreDrawn(t *testing.T) {
	docs := siteDocs(t)
	plain := build(t, docs, export.SiteOptions{})
	b := plain["reference/02-b.html"]
	if !strings.Contains(b, `<pre class="mermaid">`) || !strings.Contains(b, "<table>") || !strings.Contains(b, `<pre class="vega-lite">`) {
		t.Fatalf("the embeds are not drawn:\n%s", b)
	}
	var themes []string
	charted := build(t, docs, export.SiteOptions{Theme: "light", Chart: func(spec []byte, theme string) (string, error) {
		themes = append(themes, theme)
		return `<svg data-chart="` + string(rune('0'+len(spec)%10)) + `"></svg>`, nil
	}})
	for _, p := range []string{"reference/02-b.html", "tutorials/02-second.html"} {
		if s := charted[p]; !strings.Contains(s, `<div class="embed chart"`) || !strings.Contains(s, "<svg data-chart") || strings.Contains(s, `class="vega-lite"`) {
			t.Errorf("%s: the chart is not drawn by Chart:\n%s", p, s)
		}
	}
	if len(themes) != 2 || themes[0] != "light" {
		t.Errorf("Chart was asked in %q, want the site's theme twice", themes)
	}
	failing := build(t, docs, export.SiteOptions{Chart: func([]byte, string) (string, error) {
		return "", errors.New("a bar of temporal x and quantitative y")
	}})
	for _, p := range []string{"reference/02-b.html", "tutorials/02-second.html"} {
		if s := failing[p]; !strings.Contains(s, `<p class="chart-error">chart not drawn: a bar of temporal x and quantitative y</p><pre class="vega-lite"`) {
			t.Errorf("%s: a chart Chart cannot draw is not captioned with why:\n%s", p, s)
		}
	}
}

// Each page's frame: the sidebar in site.toml's order, the current section opened and the page
// marked, previous and next within the section, every link relative to the page, and its title.
func TestASitesChrome(t *testing.T) {
	out := build(t, siteDocs(t), export.SiteOptions{})
	first := out["tutorials/01-first.html"]
	nav := regexp.MustCompile(`<a href="([^"]*)"( aria-current="page")?>([^<]*)</a>`).FindAllStringSubmatch(first, -1)
	var titles []string
	for _, m := range nav {
		titles = append(titles, m[3])
	}
	if want := []string{"Introduction", "Tutorials", "Tutorials", "First", "Second", "Reference", "Lua API", "Plugins"}; !slices.Equal(titles[:len(want)], want) {
		t.Errorf("the sidebar is %q, want %q", titles, want)
	}
	for _, want := range []string{
		`<a class="site-title" href="../index.html">Docs</a>`,
		`<a href="01-first.html" aria-current="page">First</a>`,
		`<a href="../reference/index.html">Reference</a>`,
		`<a rel="prev" href="index.html">← Tutorials</a>`,
		`<a rel="next" href="02-second.html">Second →</a>`,
		`<title>First · Docs</title>`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("missing %s in\n%s", want, first)
		}
	}
	if lua := out["reference/lua/index.html"]; !strings.Contains(lua, `<a class="site-title" href="../../index.html">`) || !strings.Contains(lua, `<a href="../../tutorials/index.html">Tutorials</a>`) {
		t.Errorf("a page two folders deep does not reach the root:\n%s", lua)
	}
	if root := out["index.html"]; strings.Contains(root, `rel="prev"`) || strings.Contains(root, `rel="next"`) {
		t.Error("a section of one page has previous or next")
	}
}

// site.toml is refused, saying why, before a page is written: missing, a key it does not know, a
// section outside the folder or not there, a url that is not "" or a clean relative path ending in /,
// two sections at one url, two pages at one path (a folder's README.md and index.md), an unknown
// theme.
func TestASiteMapIsChecked(t *testing.T) {
	for _, c := range []struct{ name, toml, want string }{
		{"missing", "", "site.toml"},
		{"unknown key", "[[section]]\npath = \"README.md\"\nurl = \"\"\ntitel = \"x\"\n", "section.titel"},
		{"outside", "[[section]]\npath = \"../x\"\nurl = \"\"\n", "not inside"},
		{"absent", "[[section]]\npath = \"nope\"\nurl = \"\"\n", "nope"},
		{"url", "[[section]]\npath = \"README.md\"\nurl = \"/abs/\"\n", "relative path"},
		{"url by another name", "[[section]]\npath = \"a\"\nurl = \"a/./\"\n", "clean relative path"},
		{"twice", "[[section]]\npath = \"README.md\"\nurl = \"\"\n[[section]]\npath = \"a\"\nurl = \"\"\n", "two sections"},
		{"one path", "[[section]]\npath = \"a\"\nurl = \"\"\n", "both published at index.html"},
		{"theme", "theme = \"neon\"\n[[section]]\npath = \"README.md\"\nurl = \"\"\n", "no theme"},
	} {
		t.Run(c.name, func(t *testing.T) {
			files := map[string]string{"README.md": "# R\n", "a/x.md": "# X\n", "a/README.md": "# A\n", "a/index.md": "# I\n"}
			if c.toml != "" {
				files["site.toml"] = c.toml
			}
			var written []string
			err := export.Site(fixture(t, files), export.SiteOptions{}, func(rel string, _ []byte) error {
				written = append(written, rel)
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("err %v, want one saying %q", err, c.want)
			}
			if len(written) > 0 {
				t.Errorf("wrote %q before refusing", written)
			}
		})
	}
}

// A section, or a page in one, that is a symlink out of the folder is refused before a page is
// written: the site publishes what the folder holds.
func TestASitePublishesOnlyItsFolder(t *testing.T) {
	outside := fixture(t, map[string]string{"x.md": "# Outside\n", "d/y.md": "# Y\n"})
	for name, link := range map[string]struct{ path, target string }{
		"a file":   {"x.md", "x.md"},
		"a folder": {"d", "d"},
	} {
		t.Run(name, func(t *testing.T) {
			docs := fixture(t, map[string]string{
				"README.md": "# R\n",
				"site.toml": "[[section]]\npath = \"README.md\"\nurl = \"\"\n[[section]]\npath = \"" + link.path + "\"\nurl = \"out/\"\n",
			})
			if err := os.Symlink(filepath.Join(outside, link.target), filepath.Join(docs, link.path)); err != nil {
				t.Fatal(err)
			}
			var written []string
			err := export.Site(docs, export.SiteOptions{}, func(rel string, _ []byte) error {
				written = append(written, rel)
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "link out of") || len(written) > 0 {
				t.Errorf("err %v, wrote %q; want a refusal saying \"link out of\", nothing written", err, written)
			}
		})
	}
}

// AutoDoc's own docs/site.toml builds: every section is there, every tutorial a page, and each
// page's links reach published pages or GitHub, never a .md the site does not have.
func TestTheDocsSiteBuilds(t *testing.T) {
	out := build(t, "../../docs", export.SiteOptions{})
	tutorials, _ := filepath.Glob("../../docs/tutorials/[0-9]*.md")
	for _, p := range tutorials {
		if _, ok := out["tutorials/"+strings.TrimSuffix(filepath.Base(p), ".md")+".html"]; !ok {
			t.Errorf("tutorial %s is not a page", filepath.Base(p))
		}
	}
	for _, want := range []string{"index.html", "tutorials/index.html", "reference/index.html", "reference/lua/index.html", "reference/plugins/index.html"} {
		if _, ok := out[want]; !ok {
			t.Errorf("%s is not built", want)
		}
	}
	local := regexp.MustCompile(`href="([^"#?:]+\.md)[#?"]`)
	for rel, page := range out {
		for _, m := range local.FindAllStringSubmatch(page, -1) {
			t.Errorf("%s links to %s, a .md the site does not publish", rel, m[1])
		}
	}
}

// mediaMap is a site of one front page and one tutorial, each drawing media.
const mediaMap = `[[section]]
path = "README.md"
url = ""
[[section]]
path = "tutorials"
url = "tutorials/"
`

// A site publishes the media its pages draw as files, at their paths under docs, and links them:
// an image is no data: URI there, and a video alone in its paragraph is a <video> with its
// attributes, its poster a .jpg of the same name when there is one. A file two pages draw is
// written once. A video in a sentence is its alt text, and the page's policy loads its own media.
func TestASitePublishesItsMedia(t *testing.T) {
	docs := fixture(t, map[string]string{
		"site.toml": mediaMap,
		"README.md": "# Front\n\n![hero](media/hero.gif)\n\n![the clip](media/clip.mp4)\n\n![bare](media/bare.webm)\n\n" +
			"A sentence with ![inline](media/clip.mp4) in it.\n\n![again](media/hero.gif)\n",
		"media/hero.gif":         "GIF89a-hero",
		"media/clip.mp4":         "mp4-bytes",
		"media/clip.jpg":         "jpg-poster",
		"media/bare.webm":        "webm-bytes",
		"tutorials/README.md":    "# T\n",
		"tutorials/01-one.md":    "# One\n\n![shot](one/shot.png)\n",
		"tutorials/one/shot.png": "png-bytes",
	})
	site := build(t, docs, export.SiteOptions{})
	for rel, want := range map[string]string{
		"media/hero.gif": "GIF89a-hero", "media/clip.mp4": "mp4-bytes", "media/clip.jpg": "jpg-poster",
		"media/bare.webm": "webm-bytes", "tutorials/one/shot.png": "png-bytes",
	} {
		if site[rel] != want {
			t.Errorf("%s: %q, want the file's bytes %q", rel, site[rel], want)
		}
	}
	front := site["index.html"]
	for _, want := range []string{
		`<img src="media/hero.gif" alt="hero" />`,
		`<img src="media/hero.gif" alt="again" />`,
		`<video controls muted playsinline preload="metadata" src="media/clip.mp4" poster="media/clip.jpg">the clip</video>`,
		`<video controls muted playsinline preload="metadata" src="media/bare.webm">bare</video>`,
		`<em>inline</em>`,
		`content="default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; media-src 'self'"`,
		`video{display:block;max-width:100%;height:auto;margin:1rem 0}`,
	} {
		if !strings.Contains(front, want) {
			t.Errorf("the front page lacks %s:\n%s", want, front)
		}
	}
	if strings.Contains(front, "data:image") {
		t.Errorf("a site's image is inlined:\n%s", front)
	}
	if one := site["tutorials/01-one.html"]; !strings.Contains(one, `<img src="one/shot.png" alt="shot" />`) {
		t.Errorf("a tutorial's image is not linked relative to its page:\n%s", one)
	}
}

// A file a page draws is published only from inside docs, and at most maxSiteMedia bytes: a
// symlink out of docs and a file one byte over are their alt text, and nothing is written for
// them; a file of exactly maxSiteMedia bytes is published.
func TestASitesMediaStayInItsFolderAndUnderTheCap(t *testing.T) {
	outside := fixture(t, map[string]string{"secret.gif": "GIF89a-secret"})
	docs := fixture(t, map[string]string{
		"site.toml":           mediaMap,
		"README.md":           "# Front\n\n![out](media/out.gif)\n\n![big](media/big.mp4)\n\n![full](media/full.mp4)\n",
		"tutorials/README.md": "# T\n",
	})
	if err := os.MkdirAll(filepath.Join(docs, "media"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.gif"), filepath.Join(docs, "media", "out.gif")); err != nil {
		t.Fatal(err)
	}
	for name, size := range map[string]int64{"big.mp4": export.MaxSiteMedia + 1, "full.mp4": export.MaxSiteMedia} {
		f, err := os.Create(filepath.Join(docs, "media", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(size); err != nil { // sparse: no disk for the bytes
			t.Fatal(err)
		}
		f.Close()
	}
	sizes := map[string]int{}
	var front string
	if err := export.Site(docs, export.SiteOptions{}, func(rel string, page []byte) error {
		sizes[rel] = len(page)
		if rel == "index.html" {
			front = string(page)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := sizes["media/out.gif"]; ok || strings.Contains(front, "media/out.gif") || !strings.Contains(front, "<em>out</em>") {
		t.Errorf("a symlink out of docs was published (%v):\n%s", sizes, front)
	}
	if _, ok := sizes["media/big.mp4"]; ok || !strings.Contains(front, "<em>big</em>") {
		t.Errorf("a video over the cap was published (%v):\n%s", sizes, front)
	}
	if sizes["media/full.mp4"] != int(export.MaxSiteMedia) || !strings.Contains(front, `src="media/full.mp4"`) {
		t.Errorf("a video of exactly the cap was not published (%v):\n%s", sizes, front)
	}
}

// A page on its own, with no Media, is as it was: its image a data: URI, a video its alt text, and
// the policy loading nothing but data: images.
func TestAPageOnItsOwnKeepsItsMediaInline(t *testing.T) {
	dir := fixture(t, map[string]string{"note.md": "# N\n\n![pic](pic.gif)\n\n![clip](clip.mp4)\n", "pic.gif": "GIF89a", "clip.mp4": "mp4"})
	html, err := export.RenderWith([]byte("# N\n\n![pic](pic.gif)\n\n![clip](clip.mp4)\n"), export.HTML, "dark",
		export.Options{Document: filepath.Join(dir, "note.md")})
	if err != nil {
		t.Fatal(err)
	}
	page := string(html)
	for _, want := range []string{`<img src="data:image/gif;base64,`, `<em>clip</em>`,
		`content="default-src 'none'; style-src 'unsafe-inline'; img-src data:"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page lacks %s:\n%s", want, page)
		}
	}
	if strings.Contains(page, "<video") || strings.Contains(page, "media-src") || strings.Contains(page, "video{") {
		t.Errorf("a page on its own has a video or a media policy:\n%s", page)
	}
}

// A media write that fails ends the build with its error, and nothing is written after it: not the
// page's next image, nor the page.
func TestASitesFailedMediaWriteEndsTheBuild(t *testing.T) {
	docs := fixture(t, map[string]string{
		"site.toml":   mediaMap,
		"README.md":   "# Front\n\n![a](media/a.gif) and ![b](media/b.gif)\n",
		"media/a.gif": "a", "media/b.gif": "b",
		"tutorials/README.md": "# T\n",
	})
	full := errors.New("disk full")
	var calls []string
	err := export.Site(docs, export.SiteOptions{}, func(rel string, _ []byte) error {
		calls = append(calls, rel)
		if rel == "media/a.gif" {
			return full
		}
		return nil
	})
	if !errors.Is(err, full) || !slices.Equal(calls, []string{"media/a.gif"}) {
		t.Errorf("err %v, writes %v; want the write's error, and nothing written after it", err, calls)
	}
}

// A link to another page's section, written as it works on GitHub, reaches that section on the
// site: the link keeps its fragment and the heading carries the id.
func TestASitesSectionLinksLandOnTheirSection(t *testing.T) {
	docs := fixture(t, map[string]string{
		"site.toml":       "title = \"Docs\"\n[[section]]\npath = \"guide\"\nurl = \"guide/\"\ntitle = \"Guide\"\n",
		"guide/README.md": "# Guide\n\nSee [the second part](b.md#second-part-the-sequel) and [install](#install).\n\n## Install\n",
		"guide/b.md":      "# B\n\n## Second part: the sequel\n",
	})
	pages := build(t, docs, export.SiteOptions{})
	index, b := pages["guide/index.html"], pages["guide/b.html"]
	for page, want := range map[string]string{
		index: `href="b.html#second-part-the-sequel"`,
		b:     `<h2 id="second-part-the-sequel">Second part: the sequel</h2>`,
	} {
		if !strings.Contains(page, want) {
			t.Errorf("missing %s in\n%s", want, page)
		}
	}
	if !strings.Contains(index, `href="#install"`) || !strings.Contains(index, `<h2 id="install">Install</h2>`) {
		t.Errorf("a link to a section of the same page doesn't reach it:\n%s", index)
	}
}
