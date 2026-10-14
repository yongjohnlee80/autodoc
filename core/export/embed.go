package export

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"errors"
	"fmt"
	stdhtml "html"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// EMBEDS — a page's ![alt](path) of a file beside its document is drawn in the page, as xmd draws
// it: a Mermaid diagram (.mmd) as a diagram, a table (.csv) as a table, its first row the header,
// and an image as the image. The path is looked for beside the document, then in its resources
// folder (the document's name without .x.md or .md: plan.x.md's is plan/), and nowhere else: a
// path that climbs out of the document's folder, an absolute path and a URL are not looked for,
// and os.Root refuses a symlink that points out. An image is written into the page as a data: URI,
// so the page stays whole (its policy admits data: images and nothing from the network). A file
// that is missing, refused, too large or of another kind is drawn as its alt text, as before.
//
// A diagram or a table is a block: it is drawn when the embed is its paragraph's only content, and
// takes the paragraph's place. An image is drawn wherever it is.

// The bounds: a diagram or table file's size, an image's, every image of a page together, and what
// a table shows: its rows, its columns, and its cells, the header's included. A row is padded to the
// table's width, so the columns and cells bound the page a sparse file makes (a wide header over
// short rows): what is drawn grows with the file, never with rows times the widest row.
const (
	maxEmbedText = 1 << 20
	// an image's, so its data: URI stays under the 8 MiB golib's native view decodes
	maxEmbedImage   = 6 << 20
	maxEmbedImages  = 32 << 20
	maxTableRows    = 1000
	maxTableColumns = 64
	maxTableCells   = 20000
)

// imageTypes are the image files an embed draws, by extension, with their media types.
var imageTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif",
	".svg": "image/svg+xml", ".webp": "image/webp", ".avif": "image/avif", ".bmp": "image/bmp",
}

// embedder draws a document's embeds: its folder, opened as a root, and its resources folder's name.
type embedder struct {
	root      *os.Root
	resources string
	fit       tableFit
	images    int64 // the images' bytes so far, against maxEmbedImages
}

// newEmbedder is the embedder for the document at doc; nil when there is none ("") or its folder
// cannot be opened.
func newEmbedder(doc string, fit tableFit) *embedder {
	if doc == "" {
		return nil
	}
	abs, err := filepath.Abs(doc)
	if err != nil {
		return nil
	}
	root, err := os.OpenRoot(filepath.Dir(abs))
	if err != nil {
		return nil
	}
	name := filepath.Base(abs)
	lower := strings.ToLower(name)
	for _, ext := range []string{".x.md", ".md"} {
		if strings.HasSuffix(lower, ext) {
			name = name[:len(name)-len(ext)]
			break
		}
	}
	return &embedder{root: root, resources: name, fit: fit}
}

func (e *embedder) close() { e.root.Close() }

// embed is the HTML that draws dest, with alt as an image's alt text; block says the embed is its
// paragraph's only content, which a diagram and a table need. diagram says it is a Mermaid diagram,
// for the page's script. ok is false when it is drawn as its alt text.
func (e *embedder) embed(dest []byte, alt string, block bool) (html string, isBlock, diagram, ok bool) {
	p, ok := embedPath(string(dest))
	if !ok {
		return "", false, false, false
	}
	ext := strings.ToLower(path.Ext(p))
	mime, image := imageTypes[ext]
	switch {
	case image:
		data, ok := e.read(p, min(maxEmbedImage, maxEmbedImages-e.images))
		if !ok {
			return "", false, false, false
		}
		e.images += int64(len(data))
		return `<img src="data:` + mime + `;base64,` + base64.StdEncoding.EncodeToString(data) + `" alt="` + stdhtml.EscapeString(alt) + `" />`, false, false, true
	case !block:
		return "", false, false, false
	case ext == ".mmd":
		data, ok := e.read(p, maxEmbedText)
		if !ok {
			return "", false, false, false
		}
		return `<pre class="mermaid">` + stdhtml.EscapeString(string(data)) + `</pre>`, true, true, true
	case ext == ".csv":
		data, ok := e.read(p, maxEmbedText)
		if !ok {
			return "", false, false, false
		}
		table, ok := csvTable(data, e.fit)
		return table, true, false, ok
	}
	return "", false, false, false
}

// embedPath is an embed's destination as a path in the document's folder: decoded, without its
// query or fragment. A URL, an absolute path and one that climbs out are not.
func embedPath(dest string) (string, bool) {
	dest = strings.TrimSpace(dest)
	if dest == "" || strings.HasPrefix(dest, "//") || hasScheme(dest) {
		return "", false
	}
	if i := strings.IndexAny(dest, "?#"); i >= 0 {
		dest = dest[:i]
	}
	if d, err := url.PathUnescape(dest); err == nil {
		dest = d
	}
	if strings.HasPrefix(dest, "/") || filepath.IsAbs(dest) {
		return "", false
	}
	p := path.Clean(dest)
	if p == "." || !fs.ValidPath(p) {
		return "", false
	}
	return p, true
}

// read is p's bytes, beside the document or else in its resources folder, when it is a file of
// at most limit bytes.
func (e *embedder) read(p string, limit int64) ([]byte, bool) {
	for _, candidate := range []string{p, path.Join(e.resources, p)} {
		data, err := e.readAt(candidate, limit)
		if err == nil {
			return data, true
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, false // there, but refused or too large: not looked for elsewhere
		}
	}
	return nil, false
}

var errTooLarge = errors.New("export: an embed is too large")

func (e *embedder) readAt(p string, limit int64) ([]byte, error) {
	f, err := e.root.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("export: %s is not a file", p)
	}
	if info.Size() > limit {
		return nil, errTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errTooLarge
	}
	return data, nil
}

// tableFit is how much of a table a page shows: its rows (0: maxTableRows), and the width, in
// characters, its columns are fit in (0: any width, within maxTableColumns).
type tableFit struct {
	rows, width int
}

// csvTable is a CSV file as a table, its first row the header, as fit allows: the columns that fit
// (one at least, maxTableColumns at most), and fit's rows, as many as maxTableCells allow too. A
// last row says how many rows and columns are not shown. ok is false for a file with no rows, or one
// that is not CSV.
func csvTable(data []byte, fit tableFit) (string, bool) {
	r := csv.NewReader(bytes.NewReader(bytes.TrimPrefix(data, []byte("\xef\xbb\xbf"))))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	rows, err := r.ReadAll()
	if err != nil || len(rows) == 0 {
		return "", false
	}
	columns := 0
	for _, row := range rows {
		columns = max(columns, len(row))
	}
	limit := maxTableRows
	if fit.rows > 0 {
		limit = min(limit, fit.rows)
	}
	// the columns are fitted first, to the rows that may show; the cells then bound the rows at
	// the width drawn, so a table fitted narrower shows as many rows as its cells allow
	width := min(columns, maxTableColumns)
	shown := min(len(rows)-1, limit)
	if fit.width > 0 {
		width = fitting(rows[:1+shown], width, fit.width)
	}
	shown = min(shown, maxTableCells/width-1)
	var b strings.Builder
	cells := func(tag string, row []string) {
		b.WriteString("<tr>")
		for i := range width {
			cell := ""
			if i < len(row) {
				cell = row[i]
			}
			b.WriteString("<" + tag + ">" + stdhtml.EscapeString(cell) + "</" + tag + ">")
		}
		b.WriteString("</tr>\n")
	}
	b.WriteString("<table>\n<thead>\n")
	cells("th", rows[0])
	b.WriteString("</thead>\n")
	var hidden []string
	if more := len(rows) - 1 - shown; more > 0 {
		hidden = append(hidden, strconv.Itoa(more)+" more rows")
	}
	if more := columns - width; more > 0 {
		hidden = append(hidden, strconv.Itoa(more)+" more columns")
	}
	if shown > 0 || len(hidden) > 0 {
		b.WriteString("<tbody>\n")
		for _, row := range rows[1 : 1+shown] {
			cells("td", row)
		}
		if len(hidden) > 0 {
			b.WriteString(`<tr><td colspan="` + strconv.Itoa(width) + `">… ` + strings.Join(hidden, ", ") + "</td></tr>\n")
		}
		b.WriteString("</tbody>\n")
	}
	b.WriteString("</table>")
	return b.String(), true
}

// fitting is how many of a table's first columns fit in width characters as drawn, one at least:
// each column is its widest cell among rows, and three for its padding and border.
func fitting(rows [][]string, columns, width int) int {
	total := 1
	for i := range columns {
		w := 0
		for _, row := range rows {
			if i < len(row) {
				w = max(w, utf8.RuneCountInString(row[i]))
			}
		}
		if total += w + 3; total > width && i > 0 {
			return i
		}
	}
	return columns
}

// embeds are a render's drawn embeds, each written in the page in its placeholder's place.
type embeds struct {
	placeholder string // the source's digest, which the source cannot hold
	html        []string
	block       []bool
}

// add records an embed's HTML and gives the placeholder text that stands for it in the tree.
func (m *embeds) add(html string, block bool) string {
	m.html = append(m.html, html)
	m.block = append(m.block, block)
	return m.placeholder + strconv.Itoa(len(m.html)-1) + "-"
}

// place writes each embed in its placeholder's place: a block in its paragraph's place, keeping
// the paragraph's data-src on the div that holds it; an image where it stands.
func (m *embeds) place(rendered string) string {
	if len(m.html) == 0 {
		return rendered
	}
	token := regexp.QuoteMeta(m.placeholder) + `([0-9]+)-`
	re := regexp.MustCompile(`<p( data-src="[0-9]+-[0-9]+")?>` + token + `</p>|` + token)
	return re.ReplaceAllStringFunc(rendered, func(found string) string {
		sub := re.FindStringSubmatch(found)
		attrs, id := sub[1], sub[2]
		alone := id != ""
		if !alone {
			id = sub[3]
		}
		i, err := strconv.Atoi(id)
		if err != nil || i >= len(m.html) {
			return found
		}
		switch {
		case alone && m.block[i]:
			return `<div class="embed"` + attrs + `>` + m.html[i] + `</div>`
		case alone:
			return `<p` + attrs + `>` + m.html[i] + `</p>`
		}
		return m.html[i]
	})
}
