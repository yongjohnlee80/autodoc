package ingest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"text/template"
	"text/template/parse"
	"time"

	"github.com/yongjohnlee80/golib/search/chunk"
	"github.com/yongjohnlee80/golib/view"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// Args are the arguments every source view declares, in this order: the cursor a page starts
// after, (after_ts, after_id), and the pass's upper bound, upto_ts. The view returns the rows whose
// (updated_at, id) is greater than the cursor and whose updated_at is at most upto_ts, ordered by
// (updated_at, id). On a tenant's first page after_ts is the zero time and after_id is NULL.
var Args = []string{"after_ts", "after_id", "upto_ts"}

// The fields every row's JSON carries, beside the source's access fields.
const (
	FieldID        = "id"
	FieldUpdatedAt = "updated_at"
	FieldTitle     = "title" // optional: the document's title
	FieldTags      = "tags"  // optional: a string or a list of strings
)

// CutVersion is how this package turns a rendered row into chunks: the chunker's own version is
// beside it in Source.Indexer. A change that would cut the same row differently changes it.
const CutVersion = "1"

// ViewError is a .view file a source cannot be indexed from: the view's field it is about and why.
type ViewError struct {
	Path   string
	Field  string // the view's frontmatter field ("args", "export", "process", "source"), or "" for the file
	Reason string
}

func (e *ViewError) Error() string {
	if e.Field == "" {
		return e.Path + ": " + e.Reason
	}
	return e.Path + ": " + e.Field + ": " + e.Reason
}

// Source is a .view file loaded and checked for indexing: its query takes the cursor's arguments
// and its export names a document by the row's id alone, so a row whose body fails to render still
// has a known path. A Source is immutable and safe for concurrent use.
type Source struct {
	path     string
	v        *view.View
	digest   string // the file's SHA-256, its first 12 hex digits
	export   *template.Template
	access   []string
	markdown bool
}

// placeholder is a query parameter: $1, $2, …
var placeholder = regexp.MustCompile(`\$([0-9]+)`)

// Load reads the .view file at path and checks it can be indexed by an ingester with access as the
// tenant's access fields. A file golib/view refuses, or one that breaks a rule below, is a
// *ViewError:
//
//   - source is postgres;
//   - args are exactly [after_ts, after_id, upto_ts], and the query uses $1, $2 and $3 and no other;
//   - export is set, and prints the row's id and nothing else of it: literal text and {{.id}}
//     actions only, so a document's path is known from its id before its body renders.
//
// The order of the rows can only be checked against a database: CheckOrder does it.
func Load(path string, access []string) (*Source, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, &ViewError{Path: path, Reason: err.Error()}
	}
	v, err := view.New(src)
	if err != nil {
		return nil, &ViewError{Path: path, Reason: strings.TrimPrefix(err.Error(), "view: ")}
	}
	refuse := func(field, format string, args ...any) error {
		return &ViewError{Path: path, Field: field, Reason: fmt.Sprintf(format, args...)}
	}
	if v.Source() != "postgres" {
		return nil, refuse("source", "%q: an enterprise source is read from postgres", v.Source())
	}
	if !slices.Equal(v.Args(), Args) {
		return nil, refuse("args", "%v: want exactly [after_ts, after_id, upto_ts], the cursor and the pass's upper bound", v.Args())
	}
	used := map[string]bool{}
	for _, m := range placeholder.FindAllStringSubmatch(v.Process(), -1) {
		used[m[1]] = true
	}
	for _, n := range []string{"1", "2", "3"} {
		if !used[n] {
			return nil, refuse("process", "$%s (%s) is never used: the rows must be bounded by the cursor and upto_ts", n, Args[int(n[0]-'1')])
		}
		delete(used, n)
	}
	if len(used) > 0 {
		return nil, refuse("process", "uses $%s: only $1, $2 and $3 are bound", strings.Join(sortedKeys(used), ", $"))
	}
	if v.Export() == "" {
		return nil, refuse("export", "required: the document's path, from the row's id ({{.id}})")
	}
	export, err := idOnly(v.Export())
	if err != nil {
		return nil, refuse("export", "%v", err)
	}
	sum := sha256.Sum256(src)
	return &Source{path: path, v: v, digest: hex.EncodeToString(sum[:])[:12], export: export,
		access: slices.Clone(access), markdown: v.Format() == view.FormatMarkdown}, nil
}

// idOnly compiles an export template that prints the row's id and nothing else of the row: literal
// text and {{.id}} actions. A path that depended on any other field could not be named for a row
// whose other fields are missing, and a refresh could then not tell that row from one gone.
func idOnly(export string) (*template.Template, error) {
	trees, err := parse.Parse("export", export, "", "", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("does not parse: %v", err)
	}
	ids := 0
	for _, n := range trees["export"].Root.Nodes {
		switch n := n.(type) {
		case *parse.TextNode:
		case *parse.ActionNode:
			if !printsID(n.Pipe) {
				return nil, fmt.Errorf("%s: an export may print only {{.id}}: a document's path is known from its id alone", n)
			}
			ids++
		default:
			return nil, fmt.Errorf("%s: an export may print only {{.id}}: a document's path is known from its id alone", n)
		}
	}
	if ids == 0 {
		return nil, errors.New("prints no {{.id}}: every row needs a path of its own")
	}
	return template.New("export").Option("missingkey=error").Parse(export)
}

func printsID(p *parse.PipeNode) bool {
	if p == nil || len(p.Decl) > 0 || len(p.Cmds) != 1 || len(p.Cmds[0].Args) != 1 {
		return false
	}
	f, ok := p.Cmds[0].Args[0].(*parse.FieldNode)
	return ok && len(f.Ident) == 1 && f.Ident[0] == FieldID
}

// Name is the view's name.
func (s *Source) Name() string { return s.v.Name() }

// File is the .view file's path.
func (s *Source) File() string { return s.path }

// Access are the tenant's access fields, which every row must carry.
func (s *Source) Access() []string { return slices.Clone(s.access) }

// Indexer is the identity every document of the source is cut under: the view's name, its file's
// digest, and how its rows are chunked. An edit of the view changes it, so the next refresh cuts
// every row again.
func (s *Source) Indexer() string {
	return fmt.Sprintf("view:%s@%s+c%s.i%s", s.v.Name(), s.digest, chunk.Version, CutVersion)
}

// Path is the document path of the row whose id is id: the view's export, rendered with id alone.
func (s *Source) Path(id string) (string, error) {
	var b strings.Builder
	if err := s.export.Execute(&b, map[string]string{FieldID: id}); err != nil {
		return "", err
	}
	p := strings.TrimSpace(b.String())
	if p == "" {
		return "", errors.New("the export renders an empty path")
	}
	return p, nil
}

// row is one source row as a page reads it: its JSON, and its key when it has one.
type row struct {
	raw []byte
	id  string    // "" when the row has no usable id
	at  time.Time // zero when the row has no usable updated_at
	// bad is why the row's id or updated_at is missing or unusable
	bad error
}

// keyed reports whether the row has both parts of its cursor key.
func (r row) keyed() bool { return r.bad == nil }

func (r row) key() pgstore.Cursor { return pgstore.Cursor{UpdatedAt: r.at, ID: r.id} }

// fields decodes a row's JSON object, keeping numbers as their digits.
func fields(raw []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil || m == nil {
		return nil, errors.New("the row is not a JSON object")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("the row holds more than one JSON value")
	}
	return m, nil
}

// decode reads a row's key: its id (a string or a number, kept as its digits) and its updated_at
// (an RFC 3339 timestamp, as PostgreSQL writes a timestamptz into JSON; one without a zone is
// taken as UTC).
func decode(raw []byte) row {
	r := row{raw: raw}
	m, err := fields(raw)
	if err != nil {
		r.bad = err
		return r
	}
	switch id := m[FieldID].(type) {
	case string:
		r.id = id
	case json.Number:
		r.id = id.String()
	}
	if r.id == "" {
		r.bad = errors.New("the row has no id (a string or a number)")
		return r
	}
	s, _ := m[FieldUpdatedAt].(string)
	if r.at, err = parseTime(s); err != nil {
		r.bad = fmt.Errorf("the row's updated_at %w", err)
	}
	return r
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, errors.New("is missing")
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not an RFC 3339 timestamp", s)
}

// document is r as the tenant keeps it. A row missing its updated_at or an access field, one whose
// title or tags are malformed, and one whose body fails to render, is an error: the row is
// quarantined, and its document, if it has one, is kept as it was.
//
// Its SourceID is the row's id, which a search hit links back to its record by.
//
// Its Version is updated_at, then a hash of everything the document keeps from the row (the body,
// the title, the tags, the access facets and the source id), so a refresh writes the document
// again when any of them changed though updated_at did not. A document written before it kept a
// source id hashes differently now, so the first refresh after the upgrade writes it again with
// its id; its chunks' texts are unchanged, so no vector is embedded again.
func (s *Source) document(r row, path string) (pgstore.Document, error) {
	if r.bad != nil {
		return pgstore.Document{}, r.bad
	}
	m, err := fields(r.raw)
	if err != nil {
		return pgstore.Document{}, err
	}
	facets := make(map[string][]string, len(s.access))
	for _, f := range s.access {
		vals, err := values(m[f])
		if err != nil {
			return pgstore.Document{}, fmt.Errorf("the access field %s %w", f, err)
		}
		facets[f] = vals
	}
	title, err := text(m[FieldTitle])
	if err != nil {
		return pgstore.Document{}, fmt.Errorf("the title %w", err)
	}
	var tags []string
	if m[FieldTags] != nil {
		if tags, err = values(m[FieldTags]); err != nil {
			return pgstore.Document{}, fmt.Errorf("the tags %w", err)
		}
	}
	body, err := s.v.Parse(r.raw)
	if err != nil {
		return pgstore.Document{}, fmt.Errorf("render: %w", err)
	}
	d := pgstore.Document{Path: path, Indexer: s.Indexer(), Facets: facets, SourceID: r.id}
	if s.markdown {
		doc := kind.ParseMarkdown([]byte(body))
		meta := chunk.ReadMeta(doc, path)
		if title == "" {
			title = meta.Title
		}
		d.Chunks = chunk.Markdown(doc, title, 0)
		d.Kinds = make([]string, len(d.Chunks), len(d.Chunks)+1)
		for i := range d.Kinds {
			d.Kinds[i] = pgstore.ChunkSection
		}
		if c, ok := chunk.Abstract(meta, title); ok {
			c.Ord = len(d.Chunks) // after the sections, so none of them moves
			d.Chunks = append(d.Chunks, c)
			d.Kinds = append(d.Kinds, pgstore.ChunkAbstract)
		}
		tags = append(tags, meta.Tags...)
	} else {
		if title == "" {
			title = r.id
		}
		d.Chunks = chunk.Text([]byte(body), title, 0)
		d.Kinds = make([]string, len(d.Chunks))
		for i := range d.Kinds {
			d.Kinds[i] = pgstore.ChunkSection
		}
	}
	d.Title, d.Tags = title, normalTags(tags)
	d.Version = r.at.Format(time.RFC3339Nano) + "|" + contentHash(body, d)
	return d, nil
}

// contentHash is the first 12 hex digits of a SHA-256 over what a document keeps from its row,
// each part length-prefixed so no two different documents hash alike by moving a boundary.
func contentHash(body string, d pgstore.Document) string {
	h := sha256.New()
	part := func(s string) { fmt.Fprintf(h, "%d:%s", len(s), s) }
	part(body)
	part(d.Title)
	for _, t := range d.Tags {
		part(t)
	}
	part("")
	for _, f := range sortedKeys(d.Facets) {
		part(f)
		for _, v := range d.Facets[f] {
			part(v)
		}
		part("")
	}
	part(d.SourceID)
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// values reads an access field or the tags: a scalar (string, number or boolean), or a non-empty
// list of them; a value may not be empty.
func values(v any) ([]string, error) {
	list, isList := v.([]any)
	if !isList {
		list = []any{v}
	}
	if v == nil || len(list) == 0 {
		return nil, errors.New("is missing")
	}
	out := make([]string, 0, len(list))
	for _, item := range list {
		s, err := text(item)
		switch {
		case err != nil:
			return nil, err
		case s == "":
			return nil, errors.New("has an empty value")
		}
		out = append(out, s)
	}
	return out, nil
}

// text reads a scalar as text: a missing value is "".
func text(v any) (string, error) {
	switch v := v.(type) {
	case nil:
		return "", nil
	case string:
		return v, nil
	case json.Number:
		return v.String(), nil
	case bool:
		if v {
			return "true", nil
		}
		return "false", nil
	}
	return "", errors.New("is not a string, a number or a boolean")
}

// normalTags are tags lowercased, as the search port's filters compare them, without repeats, in
// order.
func normalTags(tags []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t != "" && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
