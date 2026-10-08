package export

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	stdhtml "html"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/yongjohnlee80/golib/parse/markdown"
	markdownhtml "github.com/yongjohnlee80/golib/parse/markdown/html"

	"github.com/yongjohnlee80/autodoc/core/export/mermaid"
)

type Format string

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
	return page(colors, `<pre class="mermaid">`+stdhtml.EscapeString(source)+`</pre>`, true, true), nil
}

var mermaidBlock = regexp.MustCompile(`(?s)<pre><code class="language-mermaid">(.*?)</code></pre>`)

// Options are what a render reads besides the source, its format and its theme.
type Options struct {
	// Base is the directory the source's relative links are read from: each becomes the file: URL
	// of the file it names there, so the page's links reach their files wherever it is written. ""
	// writes every link as the source has it.
	Base string
}

// Render is RenderWith without options: every link as the source has it.
func Render(source []byte, format Format, theme string) ([]byte, error) {
	return RenderWith(source, format, theme, Options{})
}

func RenderWith(source []byte, format Format, theme string, options Options) ([]byte, error) {
	document := markdown.Parse(source, markdown.GFM(), markdown.Obsidian())
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
		placeholder := fmt.Sprintf("autodoc-link-%x-", sha256.Sum256(source))
		var resolved []string
		forEach(document.Root, func(node *markdown.Node) {
			switch node.Kind {
			case markdown.KindImage:
				node.Kind = markdown.KindEmph
			case markdown.KindLink:
				if base == "" {
					return
				}
				if target, ok := fileURL(base, node.Dest); ok {
					id := placeholder + strconv.Itoa(len(resolved)/2)
					node.Dest = []byte(id)
					resolved = append(resolved, `href="`+id+`"`, `href="`+stdhtml.EscapeString(target)+`"`)
				}
			}
		})
		var body bytes.Buffer
		if err := markdownhtml.Render(&body, document); err != nil {
			return nil, err
		}
		diagrams := false
		rendered := strings.NewReplacer(resolved...).Replace(body.String())
		rendered = mermaidBlock.ReplaceAllStringFunc(rendered, func(block string) string {
			diagrams = true
			// the source stays escaped: mermaid reads the element's text
			return `<pre class="mermaid">` + mermaidBlock.FindStringSubmatch(block)[1] + `</pre>`
		})
		return page(colors, rendered, diagrams, false), nil
	case Text:
		var output bytes.Buffer
		writeText(&output, document.Root, document.Source)
		return []byte(strings.TrimSpace(output.String()) + "\n"), nil
	default:
		return nil, fmt.Errorf("export: format %q must be html or text", format)
	}
}

// page is a whole HTML document of body in colors, with the diagrams' script when it has any; a
// diagram page has the diagram alone, at its own size.
func page(colors palette, body string, diagrams, diagramPage bool) []byte {
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
	fmt.Fprintf(&output, "<style>:root{color-scheme:%s;--background:%s;--foreground:%s;--accent:%s;--muted:%s;--surface:%s;--border:%s}",
		colors.scheme, colors.background, colors.foreground, colors.accent, colors.muted, colors.surface, colors.border)
	if diagramPage {
		output.WriteString("html,body{margin:0;background:var(--background)}body{padding:1rem;width:max-content}pre.mermaid{margin:0}")
	} else {
		output.WriteString("body{max-width:76ch;margin:3rem auto;padding:0 1.5rem;background:var(--background);color:var(--foreground);font:1rem/1.6 system-ui,sans-serif}")
		output.WriteString("a{color:var(--accent)}pre,code{background:var(--surface)}pre{padding:1rem;overflow:auto;border:1px solid var(--border)}")
		output.WriteString("blockquote{border-left:.2rem solid var(--accent);padding-left:1rem;color:var(--muted)}table{border-collapse:collapse}th,td{border:1px solid var(--border);padding:.3rem .6rem}")
	}
	output.WriteString("pre.mermaid{background:none;border:0;text-align:center;color:var(--foreground)}")
	output.WriteString("</style></head><body>\n")
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

func WriteFile(destination string, content []byte) error {
	if destination == "" {
		return errors.New("export: choose a destination")
	}
	directory := filepath.Dir(destination)
	temporary, err := os.CreateTemp(directory, ".autodoc-export-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err = temporary.Write(content); err == nil {
		err = temporary.Sync()
	}
	if closeErr := temporary.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(temporaryPath, destination)
}
