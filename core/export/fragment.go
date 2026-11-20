package export

import (
	"bytes"
	"errors"
	"fmt"
	stdhtml "html"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/parse/html"
	"github.com/yongjohnlee80/golib/parse/markdown"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/outline"
)

// A FRAGMENT is a Markdown document's body alone, for a caller that lays out its own page: no
// <html>, no <head>, no stylesheet, no script, and no style attribute. It is RenderWith's body,
// drawn by the same path, with what a page around it must add said in FragmentInfo: a Mermaid
// diagram is a <pre class="mermaid"> of its escaped source, which the page's own copies of the
// vendored script (mermaid.Script, mermaid.Version) and of MermaidStart draw.
//
// What the body cannot hold, whatever the source says:
//   - raw HTML: it is escaped, as RenderWith escapes it; there is no option to let it through.
//   - a file from outside Embeds: an embed is read in that directory and nowhere else (a path that
//     climbs out, an absolute path, a URL, and a symlink that points out are not read, and the
//     embed is drawn as its alt text). An image there is linked by its path in Embeds, never
//     inlined.
//   - a URL the caller did not pass: Link is applied last, to every URL attribute of the finished
//     body (links, embeds, generated links), and an attribute it refuses is dropped.

// FragmentOptions are what Fragment reads besides the source.
type FragmentOptions struct {
	// Theme is the palette a chart is drawn in, one of Themes, as RenderWith's theme.
	Theme string
	// Chart draws a Vega-Lite spec (a vega-lite fence, a .chart.json embed) as inline SVG, as
	// Options.Chart does. nil keeps each spec as a <pre class="vega-lite"> of its escaped source.
	Chart func(spec []byte, theme string) (svg string, err error)
	// Embeds is the one directory embeds (![alt](path)) are read from: a Mermaid diagram (.mmd), a
	// chart (.chart.json), a table (.csv), an image. "" reads none: each is drawn as its alt text.
	Embeds string
	// Link is the caller's rule for every URL the body holds (an href, a src, a poster), applied
	// after everything else: it is handed the URL as the body would hold it, and answers the one
	// written, or false to drop the attribute (a link keeps its text, an image its alt). nil keeps
	// every URL as it is.
	Link func(href string) (string, bool)
	// MaxDiagrams is how many diagrams and charts a document may ask for: its mermaid and vega-lite
	// fences, and its .mmd and .chart.json embeds drawn as blocks (Embeds set). Past it the render
	// is refused with ErrTooManyDiagrams, before any is drawn. 0: no limit, as RenderWith has none.
	MaxDiagrams int
}

// The bounds a render can need, and whose they are:
//   - diagrams and charts in a document: FragmentOptions.MaxDiagrams, which only the render sees;
//   - a table (an embedded .csv or a csv fence): fixed, 1000 rows, 64 columns and 20,000 cells
//     shown of a file of at most 1 MiB (maxTableRows, embed.go). A table that is cut says so in a
//     last row, <tr class="table-cut">, with how many rows and columns are not shown; a file over
//     1 MiB is drawn as its alt text;
//   - nested embeds: none can nest. An embed draws a diagram, a chart, a table or an image, never
//     a document's Markdown, so its depth is one;
//   - a chart spec's size: the caller's, in its Chart, which is handed every spec;
//   - the rendered body's size: the caller's, on what Fragment returns.

// ErrTooManyDiagrams is a document that asks for more diagrams and charts than
// FragmentOptions.MaxDiagrams.
var ErrTooManyDiagrams = errors.New("export: too many diagrams and charts")

// FragmentInfo is what a page around a fragment needs to know of it.
type FragmentInfo struct {
	// Title is the frontmatter's title when it has one, else the first heading's text; "" when
	// there is neither.
	Title string
	// Headings are the document's headings in order, their IDs the ones the body's headings carry.
	Headings []Heading
	// HasMermaid says the body holds a <pre class="mermaid">: the page adds the script that draws
	// it, and sets data-mermaid="<MermaidTheme>" on its <html> for MermaidStart to read.
	HasMermaid bool
	// MermaidTheme is Mermaid's theme for the fragment's Theme ("default" or "dark"), as a page's
	// data-mermaid attribute.
	MermaidTheme string
	// Classes are the class names the body uses, sorted: what the page's stylesheet styles.
	Classes []string
}

// Heading is one heading of a fragment: its level (1 to 6), its text, and its ID, the body's id
// attribute on it ("" when it has none).
type Heading struct {
	Level int
	Text  string
	ID    string
}

// MermaidStart is the script that draws a page's Mermaid diagrams, after the vendored mermaid.js
// (mermaid.Script) has run: Mermaid in strict security, in the theme the page's data-mermaid
// attribute names. A page serves it as a file of its own, beside mermaid.js.
func MermaidStart() string { return mermaidStart }

// urlAttrs are the attributes that hold a URL a browser loads or follows. The body's renderer and
// embeds write href, src, poster and data-href; the rest are named so that none, wherever it came
// from (a chart's SVG), reaches the page without Link.
var urlAttrs = map[string]bool{
	"href": true, "xlink:href": true, "src": true, "poster": true, "data-href": true, "srcset": true,
	"action": true, "formaction": true, "cite": true, "background": true, "longdesc": true,
	"ping": true, "data": true, "codebase": true, "usemap": true, "manifest": true, "profile": true,
}

// rawText are the elements whose content an HTML parser reads as text, not markup, but a browser
// parses as markup inside an <svg>: a URL in one would pass linkLast unread. The renderer writes
// none of them (raw HTML is escaped), so one in a body came from Chart, and the body is refused.
var rawText = map[string]bool{
	"script": true, "style": true, "title": true, "textarea": true, "xmp": true, "iframe": true,
	"noembed": true, "noframes": true, "noscript": true, "plaintext": true,
}

// fragmentLimits bound the tokenizer that reads the finished body for Link: as large as a body
// can be (a chart's SVG holds long attribute values), so a body is never refused for its size.
var fragmentLimits = html.Limits{MaxSource: 1 << 30, MaxAttrs: 1 << 12, MaxAttr: 1 << 30}

// Fragment renders a Markdown document's body: no page, no inline script, no stylesheet. See
// FragmentOptions for what it reads and FragmentInfo for what it says of the body.
func Fragment(source []byte, opts FragmentOptions) ([]byte, FragmentInfo, error) {
	colors, ok := palettes[opts.Theme]
	if !ok {
		return nil, FragmentInfo{}, fmt.Errorf("export: no theme %q (the themes are %s)", opts.Theme, strings.Join(Themes(), ", "))
	}
	var embedder *embedder
	if opts.Embeds != "" {
		var err error
		if embedder, err = newEmbedderIn(opts.Embeds, tableFit{marked: true}); err != nil {
			return nil, FragmentInfo{}, fmt.Errorf("export: the embeds' directory: %w", err)
		}
		defer embedder.close()
		embedder.media = embedder.relative
	}
	document := kind.ParseMarkdown(source)
	if n := diagramsAsked(document, embedder != nil); opts.MaxDiagrams > 0 && n > opts.MaxDiagrams {
		return nil, FragmentInfo{}, fmt.Errorf("%w: the document asks for %d, at most %d", ErrTooManyDiagrams, n, opts.MaxDiagrams)
	}
	info := FragmentInfo{Title: frontmatterTitle(document), MermaidTheme: mermaidTheme(colors)}
	for _, h := range outline.Read(source, kind.Markdown, "").Headings() {
		info.Headings = append(info.Headings, Heading{Level: h.Level, Text: h.Text, ID: h.ID})
	}
	if info.Title == "" && len(info.Headings) > 0 {
		info.Title = info.Headings[0].Text
	}
	b, err := renderBody(document, source, opts.Theme, Options{Chart: opts.Chart, markCuts: true}, embedder)
	if err != nil {
		return nil, FragmentInfo{}, err
	}
	out, classes, err := linkLast([]byte(b.html), opts.Link)
	if err != nil {
		return nil, FragmentInfo{}, err
	}
	info.HasMermaid, info.Classes = b.diagrams, classes
	return out, info, nil
}

// diagramsAsked counts what a render of document would draw as a diagram or a chart: a mermaid or
// vega-lite fence (its info's first word, as the renderer names its language), and, when embeds
// are read, a .mmd or .chart.json embed alone in its paragraph.
func diagramsAsked(document *markdown.Document, embeds bool) int {
	n := 0
	forEach(document.Root, func(node *markdown.Node) {
		switch node.Kind {
		case markdown.KindCodeBlock:
			lang := string(node.Info())
			if i := strings.IndexAny(lang, " \t"); i >= 0 {
				lang = lang[:i]
			}
			if lang == "mermaid" || lang == "vega-lite" {
				n++
			}
		case markdown.KindImage:
			parent := node.Parent
			if !embeds || parent == nil || parent.Kind != markdown.KindParagraph || parent.FirstChild != node || parent.LastChild != node {
				return
			}
			if p, ok := embedPath(string(node.Dest())); ok && (strings.ToLower(filepath.Ext(p)) == ".mmd" || isChart(p)) {
				n++
			}
		}
	})
	return n
}

// relative is the URL a fragment links a file in its embeds' directory at: its path there, each
// segment escaped, and "./" before it when its first segment holds a colon (a folder named
// "https:"), so a browser never reads it as a URL with a scheme. file is under e.dir: the embedder
// found it there.
func (e *embedder) relative(file string) (string, bool) {
	rel, err := filepath.Rel(e.dir, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return (&url.URL{Path: filepath.ToSlash(rel)}).String(), true
}

// linkLast is the finished body with link applied to every URL attribute of every tag, an
// attribute it refuses dropped, and the class names the body uses. A tag with no URL attribute is
// kept byte for byte; one with any is written again, its attributes in their order, each value
// escaped. The body's text is escaped by the renderer, so every tag the tokenizer finds is one the
// body holds; a body with a raw-text element (rawText) is refused, as its content is not read.
func linkLast(body []byte, link func(string) (string, bool)) ([]byte, []string, error) {
	t := html.NewTokenizer(body, fragmentLimits)
	var out bytes.Buffer
	out.Grow(len(body))
	classes := map[string]bool{}
	last := 0
	for {
		tok, ok := t.Next()
		if !ok {
			break
		}
		if tok.Kind != html.StartTag && tok.Kind != html.SelfClosing {
			continue
		}
		if rawText[tok.Name] {
			return nil, nil, fmt.Errorf("export: the fragment holds a <%s> (from a chart), whose content is not read for its links", tok.Name)
		}
		for _, a := range tok.Attrs {
			if a.Name == "class" {
				for _, c := range strings.Fields(a.Value) {
					classes[c] = true
				}
			}
		}
		if link == nil || !slices.ContainsFunc(tok.Attrs, func(a html.Attr) bool { return urlAttrs[a.Name] }) {
			continue
		}
		out.Write(body[last:tok.Span[0]])
		last = tok.Span[1]
		out.WriteString("<" + tok.Name)
		for _, a := range tok.Attrs {
			value := a.Value
			if urlAttrs[a.Name] {
				mapped, keep := link(value)
				if !keep {
					continue
				}
				value = mapped
			}
			out.WriteString(" " + a.Name + `="` + stdhtml.EscapeString(value) + `"`)
		}
		if tok.Kind == html.SelfClosing {
			out.WriteString(" />")
		} else {
			out.WriteString(">")
		}
	}
	if err := t.Err(); err != nil {
		return nil, nil, fmt.Errorf("export: the fragment's links: %w", err)
	}
	out.Write(body[last:])
	names := make([]string, 0, len(classes))
	for c := range classes {
		names = append(names, c)
	}
	slices.Sort(names)
	return out.Bytes(), names, nil
}

// frontmatterTitle is the frontmatter's top-level title: its value, a quoted one unquoted; "" when
// the document has no frontmatter or it names no title.
func frontmatterTitle(document *markdown.Document) string {
	front := document.Root.FirstChild
	if front == nil || front.Kind != markdown.KindFrontmatter {
		return ""
	}
	for _, line := range strings.Split(string(front.Literal), "\n") {
		value, ok := strings.CutPrefix(strings.TrimRight(line, "\r"), "title:")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if n := len(value); n >= 2 && (value[0] == '"' && value[n-1] == '"' || value[0] == '\'' && value[n-1] == '\'') {
			value = value[1 : n-1]
		}
		return value
	}
	return ""
}
