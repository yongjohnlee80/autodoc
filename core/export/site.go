package export

import (
	"bufio"
	"cmp"
	"fmt"
	stdhtml "html"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"
)

// THE SITE (ADR 1792395460) — a folder of Markdown published as a website by this package's own
// page: the sections docs/site.toml names, each folder's *.md a page in file-name order (its
// README.md the folder's index), every page rendered by RenderWith with Links (a link to another
// page reaches it, relative to this one, so the site works under any path), Chrome (a sidebar of
// the sections and the current section's pages, and its previous and next pages) and Chart. The
// pages are handed to the caller's write: the package still reads only.

// SiteOptions are a site build's inputs besides its folder.
type SiteOptions struct {
	Theme string                                         // one of Themes; "" is site.toml's, else dark
	Chart func(spec []byte, theme string) (string, bool) // Options.Chart for every page; nil: specs
}

// siteMap is docs/site.toml.
type siteMap struct {
	Title   string        `toml:"title"`
	Theme   string        `toml:"theme"`
	Source  string        `toml:"source"` // the URL an unpublished file under the folder is linked at, + its path
	Section []siteSection `toml:"section"`
	Link    []siteLink    `toml:"link"`
}

// siteSection is a file or a folder published at url ("" the site's root, else a path ending in
// /), titled in the sidebar.
type siteSection struct {
	Path  string `toml:"path"`
	URL   string `toml:"url"`
	Title string `toml:"title"`
}

// siteLink is a sidebar link out.
type siteLink struct {
	Title string `toml:"title"`
	Href  string `toml:"href"`
}

// sitePage is a page the site publishes.
type sitePage struct {
	source  string // the Markdown file, absolute
	rel     string // where it is written in the site: "index.html", "tutorials/01-x.html"
	title   string
	section int
}

// Site builds the site docs/site.toml describes, handing each page and its path in the site to
// write, and ".nojekyll" last, so GitHub Pages serves the site as it is. It writes nothing itself.
func Site(docs string, opts SiteOptions, write func(rel string, page []byte) error) error {
	docs, err := filepath.Abs(docs)
	if err != nil {
		return err
	}
	cfg, err := readSiteMap(docs)
	if err != nil {
		return err
	}
	theme := cmp.Or(opts.Theme, cfg.Theme, "dark")
	if _, ok := palettes[theme]; !ok {
		return fmt.Errorf("site: no theme %q (the themes are %s)", theme, strings.Join(Themes(), ", "))
	}
	pages, err := listPages(docs, cfg)
	if err != nil {
		return err
	}
	real, err := filepath.EvalSymlinks(docs)
	if err != nil {
		return err
	}
	byFile := make(map[string]string, len(pages))
	byRel := make(map[string]string, len(pages))
	for _, pg := range pages { // all checked before the first write, so a bad site writes nothing
		// where the page really is: a symlink out of docs would publish what docs does not hold
		if r, err := filepath.EvalSymlinks(pg.source); err != nil {
			return err
		} else if rel, err := filepath.Rel(real, r); err != nil || !filepath.IsLocal(rel) {
			return fmt.Errorf("site: %s is a link out of %s", pg.source, docs)
		}
		if other, ok := byRel[pg.rel]; ok {
			return fmt.Errorf("site: %s and %s are both published at %s", other, pg.source, pg.rel)
		}
		byRel[pg.rel] = pg.source
		byFile[pg.source] = pg.rel
	}
	for i, pg := range pages {
		src, err := os.ReadFile(pg.source)
		if err != nil {
			return err
		}
		html, err := RenderWith(src, HTML, theme, Options{
			Document: pg.source,
			Links:    linksFrom(pg, byFile, docs, cfg.Source),
			Chrome:   chromeFor(cfg, pages, i),
			Chart:    opts.Chart,
		})
		if err != nil {
			return fmt.Errorf("site: %s: %w", pg.source, err)
		}
		if err := write(pg.rel, html); err != nil {
			return err
		}
	}
	return write(".nojekyll", nil)
}

// readSiteMap reads docs/site.toml and checks it: no key it does not know, a section's path inside
// docs and there, its url "" or a clean relative path ending in /, and one url per section.
func readSiteMap(docs string) (siteMap, error) {
	var cfg siteMap
	file := filepath.Join(docs, "site.toml")
	meta, err := toml.DecodeFile(file, &cfg)
	if err != nil {
		return cfg, fmt.Errorf("site: %w", err)
	}
	if unknown := meta.Undecoded(); len(unknown) > 0 {
		return cfg, fmt.Errorf("site: %s: no key %q", file, unknown[0].String())
	}
	if len(cfg.Section) == 0 {
		return cfg, fmt.Errorf("site: %s names no section", file)
	}
	urls := map[string]bool{}
	for _, s := range cfg.Section {
		p := filepath.Join(docs, filepath.FromSlash(s.Path))
		if r, err := filepath.Rel(docs, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(s.Path) {
			return cfg, fmt.Errorf("site: section %q is not inside %s", s.Path, docs)
		}
		if _, err := os.Stat(p); err != nil {
			return cfg, fmt.Errorf("site: section %q: %w", s.Path, err)
		}
		// one spelling per folder: "guide/./" or "guide//" would be guide/ by another name
		if s.URL != "" && (!strings.HasSuffix(s.URL, "/") || strings.HasPrefix(s.URL, "/") || strings.Contains(s.URL, "..") ||
			path.Clean(s.URL)+"/" != s.URL || strings.Contains(s.URL, "\\")) {
			return cfg, fmt.Errorf("site: section %q's url %q is not \"\" or a clean relative path ending in /", s.Path, s.URL)
		}
		if urls[s.URL] {
			return cfg, fmt.Errorf("site: two sections at url %q", s.URL)
		}
		urls[s.URL] = true
	}
	return cfg, nil
}

// listPages are the site's pages in the sidebar's order: a file section's file at its url's
// index.html, a folder's README.md there too and its other *.md beside it, by file name.
func listPages(docs string, cfg siteMap) ([]sitePage, error) {
	var pages []sitePage
	for i, s := range cfg.Section {
		p := filepath.Join(docs, filepath.FromSlash(s.Path))
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			pages = append(pages, sitePage{source: p, rel: s.URL + "index.html", title: titleOf(p), section: i})
			continue
		}
		names, err := filepath.Glob(filepath.Join(p, "*.md"))
		if err != nil {
			return nil, err
		}
		slices.SortFunc(names, func(a, b string) int { // README.md first, then by name
			ra, rb := filepath.Base(a) == "README.md", filepath.Base(b) == "README.md"
			switch {
			case ra && !rb:
				return -1
			case rb && !ra:
				return 1
			}
			return strings.Compare(a, b)
		})
		for _, n := range names {
			rel := s.URL + strings.TrimSuffix(filepath.Base(n), ".md") + ".html"
			if filepath.Base(n) == "README.md" {
				rel = s.URL + "index.html"
			}
			pages = append(pages, sitePage{source: n, rel: rel, title: titleOf(n), section: i})
		}
	}
	return pages, nil
}

// titleOf is a page's first "# " heading, past its frontmatter; its file name when it has none.
func titleOf(file string) string {
	f, err := os.Open(file)
	if err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		front := false
		for n := 0; sc.Scan(); n++ {
			line := strings.TrimRight(sc.Text(), "\r")
			switch {
			case n == 0 && line == "---":
				front = true
			case front:
				front = line != "---"
			case strings.HasPrefix(line, "# "):
				return strings.TrimSpace(strings.TrimPrefix(line, "# "))
			}
		}
	}
	return strings.TrimSuffix(filepath.Base(file), ".md")
}

// linksFrom maps pg's relative links: one to a published page (a folder's to its README.md's)
// becomes that page's path relative to pg's; one to any other file, source + its path from docs
// (a file outside docs by "../"), when source is set; anything else stays as written. The ?query
// and #fragment are split off first and kept.
func linksFrom(pg sitePage, byFile map[string]string, docs, source string) func(string) (string, bool) {
	return func(dest string) (string, bool) {
		if dest == "" || strings.HasPrefix(dest, "//") || strings.HasPrefix(dest, "#") || strings.HasPrefix(dest, "?") || hasScheme(dest) {
			return "", false
		}
		p, rest := dest, ""
		if i := strings.IndexAny(dest, "?#"); i >= 0 {
			p, rest = dest[:i], dest[i:]
		}
		if decoded, err := url.PathUnescape(p); err == nil {
			p = decoded
		}
		if filepath.IsAbs(p) {
			return "", false
		}
		target := filepath.Clean(filepath.Join(filepath.Dir(pg.source), filepath.FromSlash(p)))
		if rel, ok := byFile[target]; ok {
			return relPath(pg.rel, rel) + rest, true
		}
		if rel, ok := byFile[filepath.Join(target, "README.md")]; ok {
			return relPath(pg.rel, rel) + rest, true
		}
		under, err := filepath.Rel(docs, target)
		if err != nil || source == "" {
			return "", false
		}
		// a file outside docs too (the repository's README): source's "../", which a browser resolves
		return strings.TrimSuffix(source, "/") + "/" + escapePath(filepath.ToSlash(under)) + rest, true
	}
}

// relPath is to, a path in the site, as a link from the page at from.
func relPath(from, to string) string {
	r, err := filepath.Rel(filepath.Dir(filepath.FromSlash(from)), filepath.FromSlash(to))
	if err != nil {
		return to
	}
	return escapePath(filepath.ToSlash(r))
}

// escapePath escapes each of p's segments for a URL.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// chromeFor is page i's frame: the site's title, every section (the current one with its pages)
// and the links out, and the previous and next pages of its section.
func chromeFor(cfg siteMap, pages []sitePage, i int) *Chrome {
	pg := pages[i]
	ch := &Chrome{Site: cmp.Or(cfg.Title, "Documentation"), Page: pg.title, Root: strings.Repeat("../", strings.Count(pg.rel, "/"))}
	for si, s := range cfg.Section {
		var kids []NavItem
		first := -1
		for j, p := range pages {
			if p.section != si {
				continue
			}
			if first < 0 {
				first = j
			}
			kids = append(kids, NavItem{Title: p.title, Href: relPath(pg.rel, p.rel), Current: j == i})
		}
		if first < 0 {
			continue
		}
		item := NavItem{Title: cmp.Or(s.Title, pages[first].title), Href: relPath(pg.rel, pages[first].rel), Current: si == pg.section}
		if item.Current && len(kids) > 1 {
			item.Children = kids
		}
		ch.Nav = append(ch.Nav, item)
		if si != pg.section {
			continue
		}
		var idx []int
		for j, p := range pages {
			if p.section == si {
				idx = append(idx, j)
			}
		}
		at := slices.Index(idx, i)
		if at > 0 {
			ch.Prev = &NavItem{Title: pages[idx[at-1]].title, Href: relPath(pg.rel, pages[idx[at-1]].rel)}
		}
		if at >= 0 && at < len(idx)-1 {
			ch.Next = &NavItem{Title: pages[idx[at+1]].title, Href: relPath(pg.rel, pages[idx[at+1]].rel)}
		}
	}
	for _, l := range cfg.Link {
		ch.Nav = append(ch.Nav, NavItem{Title: l.Title, Href: l.Href})
	}
	return ch
}

// around is body in the frame: the sidebar beside it, the previous and next pages under it.
func (c *Chrome) around(body string) string {
	var b strings.Builder
	b.WriteString(`<div class="site"><nav class="site-nav" aria-label="site">`)
	fmt.Fprintf(&b, `<a class="site-title" href="%sindex.html">%s</a>`, attr(c.Root), stdhtml.EscapeString(c.Site))
	writeNav(&b, c.Nav)
	b.WriteString(`</nav><main>`)
	b.WriteString(body)
	if c.Prev != nil || c.Next != nil {
		b.WriteString(`<nav class="pager" aria-label="pages">`)
		if c.Prev != nil {
			fmt.Fprintf(&b, `<a rel="prev" href="%s">← %s</a>`, attr(c.Prev.Href), stdhtml.EscapeString(c.Prev.Title))
		} else {
			b.WriteString(`<span></span>`)
		}
		if c.Next != nil {
			fmt.Fprintf(&b, `<a rel="next" href="%s">%s →</a>`, attr(c.Next.Href), stdhtml.EscapeString(c.Next.Title))
		}
		b.WriteString(`</nav>`)
	}
	b.WriteString(`</main></div>`)
	return b.String()
}

func writeNav(b *strings.Builder, items []NavItem) {
	b.WriteString("<ul>")
	for _, it := range items {
		b.WriteString("<li>")
		current := ""
		if it.Current && len(it.Children) == 0 {
			current = ` aria-current="page"`
		}
		fmt.Fprintf(b, `<a href="%s"%s>%s</a>`, attr(it.Href), current, stdhtml.EscapeString(it.Title))
		if len(it.Children) > 0 {
			writeNav(b, it.Children)
		}
		b.WriteString("</li>")
	}
	b.WriteString("</ul>")
}

// attr is s escaped for a double-quoted attribute.
func attr(s string) string { return stdhtml.EscapeString(s) }

// chromeStyle lays a site page out: the sidebar beside the body, stacked on a narrow screen.
const chromeStyle = "body{max-width:none;margin:0;padding:0}" +
	".site{display:flex;min-height:100vh}" +
	".site-nav{flex:none;width:16rem;padding:1.5rem 1rem;border-right:1px solid var(--border);background:var(--surface);font-size:.95rem}" +
	".site-nav ul{list-style:none;padding-left:0;margin:.5rem 0}.site-nav ul ul{padding-left:1rem}" +
	".site-nav li{margin:.2rem 0}.site-nav a{text-decoration:none}.site-nav a[aria-current]{font-weight:bold;color:var(--foreground)}" +
	".site-title{display:block;font-weight:bold;font-size:1.15rem;margin-bottom:1rem}" +
	"main{flex:1;min-width:0;max-width:76ch;margin:0 auto;padding:2rem 1.5rem}" +
	".pager{display:flex;justify-content:space-between;gap:1rem;margin-top:3rem;padding-top:1rem;border-top:1px solid var(--border)}" +
	"@media (max-width:48rem){.site{display:block}.site-nav{width:auto;border-right:0;border-bottom:1px solid var(--border)}}"

// chartIn is chart in theme, for the embedder and the fences, setting *drawn once it draws one; nil
// when there is none.
func chartIn(chart func(spec []byte, theme string) (string, bool), theme string, drawn *bool) func([]byte) (string, bool) {
	if chart == nil {
		return nil
	}
	return func(spec []byte) (string, bool) {
		svg, ok := chart(spec, theme)
		*drawn = *drawn || ok
		return svg, ok
	}
}
