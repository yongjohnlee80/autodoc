package export

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	stdhtml "html"
	"net/url"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/yongjohnlee80/golib/parse/markdown"
	markdownhtml "github.com/yongjohnlee80/golib/parse/markdown/html"

	"github.com/yongjohnlee80/autodoc/core/export/mermaid"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/lab"
)

// Format is what a Markdown source is rendered as.
type Format string

// The formats: a standalone HTML page in a theme, or plain text.
const (
	HTML Format = "html"
	Text Format = "text"
)

type palette struct {
	background, foreground, accent, muted, surface, border string
	scheme                                                 string // the color-scheme it declares: light or dark
}

// palettes are the shipped themes' document colours (golib's tui/decl/themes), so an export and a
// preview look as the page does; light and dark are required, the others are their own.
var palettes = map[string]palette{
	"dark":  {"#1c1c1c", "#d0d0d0", "#87afd7", "#8a8a8a", "#303030", "#585858", "dark"},
	"light": {"#f6f2e7", "#2e2a24", "#2f5f8f", "#7a7163", "#ebe6d9", "#b3aa96", "light"},
	"sepia": {"#e6d9b9", "#5b4636", "#8a5a2b", "#8c7656", "#dacca9", "#b5a380", "light"},
	"retro": {"#000070", "#ffff55", "#ffffff", "#aaaaaa", "#00005a", "#555555", "dark"},
	"mono":  {"#ffffff", "#000000", "#000000", "#555555", "#eeeeee", "#888888", "light"},
}

// Background is a theme's page colour; dark's for a theme it does not have.
func Background(theme string) string { return palettes[ThemeOf(theme)].background }

// Themes are the themes an export takes, sorted.
func Themes() []string {
	out := make([]string, 0, len(palettes))
	for name := range palettes {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ThemeOf is the export theme for a TUI theme: its own where it has one, else dark.
func ThemeOf(tuiTheme string) string {
	if _, ok := palettes[tuiTheme]; ok {
		return tuiTheme
	}
	return "dark"
}

// MERMAID — a ```mermaid block is drawn by the vendored mermaid.js (core/export/mermaid), in the
// page, offline: the page holds the script, and its policy allows that script and the fixed one
// that starts it, by their digests, and no other. A page without a diagram has no script at all.
// A diagram mermaid cannot draw shows mermaid's own error in its place.

// mermaidStart draws every pre.mermaid in the theme the html element names (data-mermaid); on a
// diagram page (data-diagram) each kind at its own size rather than the page's width.
const mermaidStart = `(function(){var d=document.documentElement.dataset,w={useMaxWidth:!d.diagram},c={startOnLoad:false,securityLevel:"strict",theme:d.mermaid||"default"};` +
	`["flowchart","sequence","gantt","journey","timeline","class","state","er","pie","quadrantChart","xyChart","requirement","mindmap","gitGraph","c4","sankey","packet","block","architecture","radar","kanban"].forEach(function(k){c[k]=w});` +
	`mermaid.initialize(c);mermaid.run({querySelector:"pre.mermaid",suppressErrors:true})})()`

var (
	hashesOnce     sync.Once
	scriptPolicies string
)

// scriptPolicy is the script-src of a page with diagrams: the vendored script and mermaidStart.
func scriptPolicy() string {
	hashesOnce.Do(func() { scriptPolicies = mermaid.Hash(mermaid.Script()) + " " + mermaid.Hash(mermaidStart) })
	return scriptPolicies
}

// mermaidTheme is the mermaid theme for a palette.
func mermaidTheme(colors palette) string {
	switch {
	case colors.scheme == "dark":
		return "dark"
	case colors.background == palettes["mono"].background:
		return "neutral"
	}
	return "default"
}

// DiagramPage is a page of one Mermaid diagram, drawn at its own size on the theme's background:
// for a preview, which renders it whole and scrolls it.
func DiagramPage(source, theme string) ([]byte, error) {
	colors, ok := palettes[theme]
	if !ok {
		return nil, fmt.Errorf("export: no theme %q (the themes are %s)", theme, strings.Join(Themes(), ", "))
	}
	return page(colors, `<pre class="mermaid">`+stdhtml.EscapeString(source)+`</pre>`, true, true, false, nil), nil
}

// mermaidBlock is a mermaid fence as the renderer writes it: its pre's attributes (data-src, with
// SourceSpans) and its escaped source.
var mermaidBlock = fenceBlock("mermaid")

// CHARTS AND TABLES IN FENCES — a ```vega-lite fence is a chart, as a .chart.json embed is: a <pre
// class="vega-lite"> of its spec, which the GUI's HTML view draws natively and a browser shows as
// the spec. A ```csv fence is a table, as a .csv embed is (its first row the header, fit as
// Options say); one that is not CSV stays code.
var (
	chartBlock = fenceBlock("vega-lite")
	csvBlock   = fenceBlock("csv")
)

// fenceBlock matches a fence of lang as the renderer writes it: its pre's attributes and its
// escaped source.
func fenceBlock(lang string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<pre((?:\s+[a-zA-Z-]+="[^"]*")*)><code class="language-` + regexp.QuoteMeta(lang) + `">(.*?)</code></pre>`)
}

// Options are what a render reads besides the source, its format and its theme.
type Options struct {
	// Base is the directory the source's relative links are read from: each becomes the file: URL
	// of the file it names there, so the page's links reach their files wherever it is written. ""
	// writes every link as the source has it.
	Base string
	// SourceSpans puts each block element's source bytes on it (data-src="start-end"), for a
	// view that follows the editor: the GUI's native HTML preview scrolls to the cursor's block.
	SourceSpans bool
	// Document is the source's own file on disk. Its embeds (![alt](path)) of a Mermaid diagram
	// (.mmd), a Vega-Lite chart (.chart.json), a table (.csv) or an image are read beside it, or in
	// its resources folder, and drawn in the page (embed.go). "" draws each as its alt text.
	Document string
	// TableRows is how many rows an embedded table, or a csv fence's, shows (0: 1000); TableWidth
	// is the width, in characters, its columns are fit in: those that fit are shown, and the table
	// says how many are not (0: any width, 64 columns at most). For a view narrower than a
	// browser: the GUI's Rendered view of a note.
	TableRows, TableWidth int
	// Links maps a relative link's destination, as written, to the URL the page writes; ok false
	// keeps it as written. Base, when set, is the file: mapping (fileURL) and wins over Links. For a
	// site: a link to another page reaches that page (site.go).
	Links func(dest string) (url string, ok bool)
	// Chrome is the frame around the body: a site's sidebar and previous/next. nil: a bare page.
	Chrome *Chrome
	// Chart draws a Vega-Lite spec as inline SVG in the page's theme; nil, or ok false, keeps the
	// <pre class="vega-lite"> of its spec. It draws a .chart.json embed and a vega-lite fence. The
	// GUI build gives one (golib's gui/vegalite); core/export draws none of its own.
	Chart func(spec []byte, theme string) (svg string, ok bool)
}

// Chrome is a site page's frame, drawn by the page in its palette: the site's title, the
// sidebar, and the previous and next pages of its section. Every Href is relative to the page.
type Chrome struct {
	Site       string    // the site's title, linking to its root
	Page       string    // this page's title: the document's <title> is "Page · Site"
	Root       string    // the path from this page to the site's root: "", "../", "../../"
	Nav        []NavItem // the sidebar: the sections, their pages, the links out
	Prev, Next *NavItem  // within the page's section
}

// NavItem is a sidebar entry: a section with its pages, a page, or a link out.
type NavItem struct {
	Title, Href string
	Current     bool      // this page, or the section it is in
	Children    []NavItem // a section's pages
}

// Colors is a theme's page colours, for what is drawn in its palette (a chart): its background,
// its text, its muted text and its borders, as #rrggbb; dark's for a theme it does not have.
func Colors(theme string) (background, foreground, muted, border string) {
	p := palettes[ThemeOf(theme)]
	return p.background, p.foreground, p.muted, p.border
}

// Render is RenderWith without options: every link as the source has it.
func Render(source []byte, format Format, theme string) ([]byte, error) {
	return RenderWith(source, format, theme, Options{})
}

// RenderWith renders the Markdown source as format. HTML is a whole page in the named theme (one
// of Themes), its Mermaid diagrams drawn by the page's own script; theme is ignored for Text.
func RenderWith(source []byte, format Format, theme string, options Options) ([]byte, error) {
	document := kind.ParseMarkdown(source)
	switch format {
	case HTML:
		colors, ok := palettes[theme]
		if !ok {
			return nil, fmt.Errorf("export: no theme %q (the themes are %s)", theme, strings.Join(Themes(), ", "))
		}
		base := options.Base
		if base != "" {
			var err error
			if base, err = filepath.Abs(base); err != nil {
				return nil, err
			}
		}
		// the renderer writes a file: URL empty (only http, https, mailto, ftp and tel are written,
		// unless every raw HTML block is written too), so a resolved link is rendered as a
		// placeholder and its URL written in its place. The placeholder holds the source's digest,
		// which the source cannot hold, so no text of its own is taken for one.
		digest := sha256.Sum256(source)
		placeholder := fmt.Sprintf("autodoc-link-%x-", digest)
		var resolved []string
		charts := false // a chart drawn as SVG, whose sizing the page then carries
		chart := chartIn(options.Chart, theme, &charts)
		embedder := newEmbedder(options.Document, tableFit{rows: options.TableRows, width: options.TableWidth})
		if embedder != nil {
			embedder.chart = chart
			defer embedder.close()
		}
		mapLink := options.Links
		if base != "" {
			mapLink = func(dest string) (string, bool) { return fileURL(base, []byte(dest)) }
		}
		drawn := embeds{placeholder: fmt.Sprintf("autodoc-embed-%x-", digest)}
		diagrams := false
		forEach(document.Root, func(node *markdown.Node) {
			switch node.Kind {
			case markdown.KindImage:
				if embedder != nil {
					parent := node.Parent
					alone := parent != nil && parent.Kind == markdown.KindParagraph && parent.FirstChild == node && parent.LastChild == node
					var alt bytes.Buffer
					for child := node.FirstChild; child != nil; child = child.Next {
						writeText(&alt, child, document.Source)
					}
					if html, block, diagram, ok := embedder.embed(node.Dest(), alt.String(), alone); ok {
						diagrams = diagrams || diagram
						node.Kind, node.Literal = markdown.KindText, []byte(drawn.add(html, block))
						node.FirstChild, node.LastChild = nil, nil
						return
					}
				}
				node.Kind = markdown.KindEmph
			case markdown.KindCodeBlock:
				// a lab note's cell: its outputs, files beside the note (ADR 1792457255), drawn
				// right after its fence, in name order; a fence no run wrote for draws none
				if embedder == nil || node.Fence() == 0 || !lab.Is(options.Document) {
					return
				}
				dir, key := lab.OutDir(options.Document), lab.CellKey(lab.FenceLang(string(node.Info())), string(node.Literal))
				after := node
				for _, f := range lab.Outputs(embedder.root.FS(), dir, key) {
					if html, ok := embedder.output(f); ok {
						after = insertAfter(after, drawn.add(html, true))
					}
				}
			case markdown.KindLink:
				if mapLink == nil {
					return
				}
				if target, ok := mapLink(string(node.Dest())); ok {
					id := placeholder + strconv.Itoa(len(resolved)/2)
					node.SetDest([]byte(id))
					resolved = append(resolved, `href="`+id+`"`, `href="`+stdhtml.EscapeString(target)+`"`)
				}
			}
		})
		var body bytes.Buffer
		var renderOpts []markdownhtml.Option
		if options.SourceSpans {
			renderOpts = append(renderOpts, markdownhtml.SourceSpans())
		}
		if err := markdownhtml.Render(&body, document, renderOpts...); err != nil {
			return nil, err
		}
		rendered := drawn.place(strings.NewReplacer(resolved...).Replace(body.String()))
		rendered = mermaidBlock.ReplaceAllStringFunc(rendered, func(block string) string {
			diagrams = true
			// the source stays escaped: mermaid reads the element's text. The pre keeps its
			// attributes: a pane that follows the cursor finds the diagram by its data-src
			m := mermaidBlock.FindStringSubmatch(block)
			return `<pre class="mermaid"` + m[1] + `>` + m[2] + `</pre>`
		})
		rendered = chartBlock.ReplaceAllStringFunc(rendered, func(block string) string {
			m := chartBlock.FindStringSubmatch(block)
			if chart != nil {
				if svg, ok := chart([]byte(stdhtml.UnescapeString(m[2]))); ok {
					return `<div class="embed chart"` + m[1] + `>` + svg + `</div>`
				}
			}
			return `<pre class="vega-lite"` + m[1] + `>` + m[2] + `</pre>`
		})
		fit := tableFit{rows: options.TableRows, width: options.TableWidth}
		rendered = csvBlock.ReplaceAllStringFunc(rendered, func(block string) string {
			m := csvBlock.FindStringSubmatch(block)
			table, ok := csvTable([]byte(stdhtml.UnescapeString(m[2])), fit)
			if !ok {
				return block
			}
			return `<div class="embed"` + m[1] + `>` + table + `</div>`
		})
		return page(colors, rendered, diagrams, false, charts, options.Chrome), nil
	case Text:
		var output bytes.Buffer
		writeText(&output, document.Root, document.Source)
		return []byte(strings.TrimSpace(output.String()) + "\n"), nil
	default:
		return nil, fmt.Errorf("export: format %q must be html or text", format)
	}
}

// page is a whole HTML document of body in colors, with the diagrams' script when it has any and
// the charts' sizing when it draws any, so a page with neither is as it always was; a diagram page
// has the diagram alone, at its own size. A chrome puts a site's sidebar beside the
// body and its previous and next pages under it, in the same palette and under the same policy.
func page(colors palette, body string, diagrams, diagramPage, charts bool, chrome *Chrome) []byte {
	var output bytes.Buffer
	output.WriteString("<!doctype html>\n<html lang=\"en\"")
	if diagrams {
		fmt.Fprintf(&output, ` data-mermaid="%s"`, mermaidTheme(colors))
	}
	if diagramPage {
		output.WriteString(` data-diagram="1"`)
	}
	output.WriteString("><head><meta charset=\"utf-8\">\n")
	policy := "default-src 'none'; style-src 'unsafe-inline'; img-src data:"
	if diagrams {
		policy += "; script-src " + scriptPolicy()
	}
	output.WriteString("<meta http-equiv=\"Content-Security-Policy\" content=\"" + policy + "\">\n")
	output.WriteString("<meta name=\"color-scheme\" content=\"" + colors.scheme + "\">\n")
	if chrome != nil {
		output.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
		output.WriteString("<title>" + stdhtml.EscapeString(strings.TrimSuffix(chrome.Page+" · "+chrome.Site, " · ")) + "</title>\n")
	}
	fmt.Fprintf(&output, "<style>:root{color-scheme:%s;--background:%s;--foreground:%s;--accent:%s;--muted:%s;--surface:%s;--border:%s}",
		colors.scheme, colors.background, colors.foreground, colors.accent, colors.muted, colors.surface, colors.border)
	if diagramPage {
		output.WriteString("html,body{margin:0;background:var(--background)}body{padding:1rem;width:max-content}pre.mermaid{margin:0}")
	} else {
		output.WriteString("body{max-width:76ch;margin:3rem auto;padding:0 1.5rem;background:var(--background);color:var(--foreground);font:1rem/1.6 system-ui,sans-serif}")
		output.WriteString("a{color:var(--accent)}pre,code{background:var(--surface)}pre{padding:1rem;overflow:auto;border:1px solid var(--border)}")
		output.WriteString("blockquote{border-left:.2rem solid var(--accent);padding-left:1rem;color:var(--muted)}table{border-collapse:collapse}th,td{border:1px solid var(--border);padding:.3rem .6rem}")
		output.WriteString("img{max-width:100%}div.embed{margin:1rem 0;overflow:auto}")
		if charts {
			output.WriteString("div.chart svg{max-width:100%;height:auto}")
		}
		if chrome != nil {
			output.WriteString(chromeStyle)
		}
	}
	output.WriteString("pre.mermaid{background:none;border:0;text-align:center;color:var(--foreground)}")
	output.WriteString("</style></head><body>\n")
	if chrome != nil && !diagramPage {
		body = chrome.around(body)
	}
	output.WriteString(body)
	if diagrams {
		output.WriteString("\n<script>" + mermaid.Script() + "</script>\n<script>" + mermaidStart + "</script>\n")
	}
	output.WriteString("</body></html>\n")
	return output.Bytes()
}

// fileURL is the file: URL of a link's destination read from base, as a browser beside the source
// would read it: a relative path from base, an absolute one as it is, its escapes decoded, and its
// query and fragment as written. ok is false for a destination that is not a path: a URL with a
// scheme (http:, mailto:, file:, …), one naming another host (//host/…), a bare #fragment or none.
func fileURL(base string, destination []byte) (string, bool) {
	link := string(destination)
	if strings.HasPrefix(link, "//") || hasScheme(link) {
		return "", false
	}
	path, rest := link, ""
	if i := strings.IndexAny(link, "?#"); i >= 0 {
		path, rest = link[:i], link[i:]
	}
	if path == "" {
		return "", false // none, or a bare #fragment or ?query: this page's own
	}
	if decoded, err := url.PathUnescape(path); err == nil {
		path = decoded // an escape that is not one (100%.md) is the file's own name
	}
	path = filepath.FromSlash(path)
	if !filepath.IsAbs(path) {
		path = filepath.Join(base, path)
	}
	path = filepath.ToSlash(filepath.Clean(path))
	if !strings.HasPrefix(path, "/") {
		path = "/" + path // a drive's path: file:///C:/…
	}
	return (&url.URL{Scheme: "file", Path: path}).String() + escapeURL(rest), true
}

// hasScheme reports whether link starts with a scheme, as a URL parser reads one: a letter, then
// letters, digits, '+', '-' or '.', then ':'.
func hasScheme(link string) bool {
	for i := 0; i < len(link); i++ {
		switch c := link[i]; {
		case c == ':':
			return i > 0
		case 'a' <= c|0x20 && c|0x20 <= 'z', i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return false
}

// escapeURL percent-encodes what is not safe in a URL (a space, a quote, any byte past ASCII) and
// keeps the rest, '%' among it, as written.
func escapeURL(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-_.!~*'();/?:@&=+$,#%", c) >= 0 {
			out.WriteByte(c)
		} else {
			fmt.Fprintf(&out, "%%%02X", c)
		}
	}
	return out.String()
}

func forEach(node *markdown.Node, visit func(*markdown.Node)) {
	visit(node)
	for child := node.FirstChild; child != nil; child = child.Next {
		forEach(child, visit)
	}
}

func writeText(output *bytes.Buffer, node *markdown.Node, source []byte) {
	switch node.Kind {
	case markdown.KindFrontmatter, markdown.KindLinkRefDef, markdown.KindHTMLBlock, markdown.KindRawHTML:
		return
	case markdown.KindText, markdown.KindCodeSpan, markdown.KindAutolink:
		output.Write(node.Text(source))
		return
	case markdown.KindCodeBlock:
		output.Write(node.Literal)
		output.WriteByte('\n')
		return
	case markdown.KindSoftBreak, markdown.KindHardBreak:
		output.WriteByte('\n')
		return
	}
	for child := node.FirstChild; child != nil; child = child.Next {
		writeText(output, child, source)
	}
	switch node.Kind {
	case markdown.KindHeading, markdown.KindParagraph, markdown.KindItem, markdown.KindTableRow:
		output.WriteByte('\n')
	case markdown.KindList, markdown.KindBlockQuote, markdown.KindTable:
		output.WriteByte('\n')
	}
}
