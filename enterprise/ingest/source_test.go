package ingest

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
)

// trackView is the sample source view: tracks joined to their artists, whose updated_at covers both
// tables, ordered by the cursor's key, explicitly, page by page of limit rows.
const trackView = `---
version: 1
name: track
source: postgres
args: [after_ts, after_id, upto_ts]
process: |
  SELECT jsonb_build_object(
    'id', t.id, 'label_id', t.label_id, 'title', t.title, 'artist', a.name, 'lyric', t.lyric,
    'updated_at', GREATEST(t.updated_at, a.updated_at))
  FROM track t JOIN artist a ON a.id = t.artist_id
  WHERE (GREATEST(t.updated_at, a.updated_at), t.id) > ($1, $2)
    AND GREATEST(t.updated_at, a.updated_at) <= $3
  ORDER BY GREATEST(t.updated_at, a.updated_at), t.id
  LIMIT 3
export: track/{{.id}}.md
---
# {{.title}}
by {{.artist}}

{{if .lyric}}{{.lyric}}{{else}}*(Instrumental)*{{end}}
`

// writeView writes src as a .view file in a test directory and returns its path.
func writeView(t testing.TB, src string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "source.view")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A view an ingester cannot page or name documents with is refused when it loads, naming the
// view's field: the arguments must be the cursor's, each used and no other; the source postgres;
// the export set, printing the id and nothing else of the row, and at least once. The mutants,
// named here: Load without the args check (a view with [since] loads), without the placeholder
// check (a view never bounded by upto_ts loads), and idOnly admitting any field (an export by
// title loads).
func TestAViewThatCannotBePagedIsRefusedAtLoad(t *testing.T) {
	for _, c := range []struct {
		name, from, to, field, says string
	}{
		{"since", "args: [after_ts, after_id, upto_ts]", "args: [since]", "args", "want exactly [after_ts, after_id, upto_ts]"},
		{"reordered", "args: [after_ts, after_id, upto_ts]", "args: [after_id, after_ts, upto_ts]", "args", "want exactly"},
		{"sqlite", "source: postgres", "source: sqlite", "source", "read from postgres"},
		{"no upto", "AND GREATEST(t.updated_at, a.updated_at) <= $3", "", "process", "$3 (upto_ts) is never used"},
		{"a fourth", "<= $3", "<= $3 AND $4 IS NULL", "process", "uses $4"},
		{"no export", "export: track/{{.id}}.md\n", "", "export", "required"},
		{"by title", "export: track/{{.id}}.md", "export: track/{{.title}}.md", "export", "may print only {{.id}}"},
		{"no id", "export: track/{{.id}}.md", "export: track/all.md", "export", "prints no {{.id}}"},
		{"a function", "export: track/{{.id}}.md", "export: track/{{lower .id}}.md", "export", "does not parse"},
		{"a range", "export: track/{{.id}}.md", "export: track/{{range .tags}}{{.}}{{end}}.md", "export", "may print only {{.id}}"},
		{"two ids", "export: track/{{.id}}.md", "export: track/{{.id}}/{{ .id }}.md", "", ""},
		{"no version", "version: 1\n", "", "", "version: want 1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			src := strings.Replace(trackView, c.from, c.to, 1)
			if src == trackView {
				t.Fatalf("the case changes nothing: %q is not in the view", c.from)
			}
			s, err := Load(writeView(t, src), []string{"label_id"})
			if c.says == "" {
				if err != nil {
					t.Fatalf("Load: %v, want it loaded", err)
				}
				if p, _ := s.Path("7"); p != "track/7/7.md" {
					t.Errorf("Path(7) = %q", p)
				}
				return
			}
			var ve *ViewError
			if !errors.As(err, &ve) || ve.Field != c.field || !strings.Contains(err.Error(), c.says) {
				t.Fatalf("Load: %v, want a ViewError on %q saying %q", err, c.field, c.says)
			}
		})
	}
	if _, err := Load(filepath.Join(t.TempDir(), "missing.view"), nil); err == nil {
		t.Error("a missing file loads")
	}
}

// A source's identity names its view and changes with the file: an edit of the view cuts every
// row again.
func TestTheIndexerChangesWithTheViewFile(t *testing.T) {
	a, err := Load(writeView(t, trackView), []string{"label_id"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Load(writeView(t, strings.Replace(trackView, "by {{.artist}}", "performed by {{.artist}}", 1)), []string{"label_id"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(a.Indexer(), "view:track@") || !strings.HasSuffix(a.Indexer(), "+c5.i"+CutVersion) {
		t.Errorf("Indexer = %q", a.Indexer())
	}
	if a.Indexer() == b.Indexer() {
		t.Errorf("an edited view keeps the indexer %q", a.Indexer())
	}
	if a.Name() != "track" || !slices.Equal(a.Access(), []string{"label_id"}) || a.File() == "" {
		t.Errorf("Name %q, Access %v, File %q", a.Name(), a.Access(), a.File())
	}
}

// A row's key is its id (a string, or a number kept as its digits) and its updated_at (RFC 3339,
// or a timestamp with no zone, taken as UTC); a row missing either is not keyed, and says why.
func TestARowsKey(t *testing.T) {
	at := time.Date(2026, 10, 12, 10, 0, 0, 123456000, time.UTC)
	for _, c := range []struct {
		json  string
		id    string
		keyed bool
	}{
		{`{"id": 12345678901234567890, "updated_at": "2026-10-12T10:00:00.123456+00:00"}`, "12345678901234567890", true},
		{`{"id": "a-1", "updated_at": "2026-10-12T03:00:00.123456-07:00"}`, "a-1", true},
		{`{"id": "a-1", "updated_at": "2026-10-12T10:00:00.123456"}`, "a-1", true},
		{`{"id": "a-1"}`, "a-1", false},
		{`{"id": "a-1", "updated_at": "yesterday"}`, "a-1", false},
		{`{"id": "", "updated_at": "2026-10-12T10:00:00Z"}`, "", false},
		{`{"id": {"x": 1}, "updated_at": "2026-10-12T10:00:00Z"}`, "", false},
		{`[1, 2]`, "", false},
		{`{"id": 1} {"id": 2}`, "", false},
	} {
		r := decode([]byte(c.json))
		if r.id != c.id || r.keyed() != c.keyed || (r.keyed() && !r.at.Equal(at)) || (!r.keyed() && r.bad == nil) {
			t.Errorf("%s: id %q at %v keyed %v (%v); want id %q keyed %v", c.json, r.id, r.at, r.keyed(), r.bad, c.id, c.keyed)
		}
	}
}

// A row becomes its document: the access fields as facets (a scalar or a list), the title and tags,
// the rendered body chunked as Markdown with an abstract chunk from its frontmatter, every chunk's
// kind, and a Version of updated_at and a hash of everything the document keeps. A row missing an
// access field, with a malformed title or tags, or whose body does not render, is an error: the
// row is quarantined. The mutant, named here: a hash over the body alone, so a changed access
// field leaves the Version as it was.
func TestARowBecomesItsDocument(t *testing.T) {
	src := strings.Replace(trackView, "---\n# {{.title}}", "---\n---\nabstract: \"{{.title}}, a song\"\ntags: [Rock]\n---\n# {{.title}}", 1)
	s, err := Load(writeView(t, src), []string{"label_id", "region"})
	if err != nil {
		t.Fatal(err)
	}
	doc := func(js string) (pgstore.Document, error) {
		t.Helper()
		r := decode([]byte(js))
		p, err := s.Path(r.id)
		if err != nil {
			t.Fatal(err)
		}
		return s.document(r, p)
	}
	base := `"id": 7, "updated_at": "2026-10-12T10:00:00Z", "title": "Blue", "artist": "Ann", "lyric": "la la"`
	d, err := doc(`{` + base + `, "label_id": 42, "region": ["ca", "us"], "tags": ["Live", "live"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if d.Path != "track/7.md" || d.Title != "Blue" || d.Indexer != s.Indexer() {
		t.Errorf("Path %q Title %q Indexer %q", d.Path, d.Title, d.Indexer)
	}
	if !slices.Equal(d.Facets["label_id"], []string{"42"}) || !slices.Equal(d.Facets["region"], []string{"ca", "us"}) {
		t.Errorf("Facets %v", d.Facets)
	}
	if !slices.Equal(d.Tags, []string{"live", "rock"}) {
		t.Errorf("Tags %v, want the row's and the body's, lowercased, once each", d.Tags)
	}
	if len(d.Kinds) != len(d.Chunks) || d.Kinds[len(d.Kinds)-1] != pgstore.ChunkAbstract || d.Kinds[0] != pgstore.ChunkSection ||
		!strings.Contains(d.Chunks[len(d.Chunks)-1].Body, "Blue, a song") || !strings.Contains(d.Chunks[0].Body, "la la") {
		t.Errorf("Chunks %+v Kinds %v", d.Chunks, d.Kinds)
	}
	if !strings.HasPrefix(d.Version, "2026-10-12T10:00:00Z|") || len(d.Version) != len("2026-10-12T10:00:00Z|")+12 {
		t.Errorf("Version %q", d.Version)
	}
	if d.SourceID != "7" {
		t.Errorf("SourceID %q, want the row's id", d.SourceID)
	}
	other, err := doc(`{` + strings.Replace(base, `"id": 7`, `"id": 8`, 1) + `, "label_id": 42, "region": ["ca", "us"], "tags": ["Live", "live"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if other.Version == d.Version {
		t.Errorf("rows differing only by id share the Version %q: a document stored without its source id would never be written again with it", d.Version)
	}
	moved, err := doc(`{` + base + `, "label_id": 43, "region": ["ca", "us"], "tags": ["Live"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if moved.Version == d.Version {
		t.Errorf("a row moved to another label keeps the Version %q: a refresh would not write its new access facet", d.Version)
	}
	for js, says := range map[string]string{
		`{` + base + `, "region": "ca"}`:                                                                 "the access field label_id is missing",
		`{` + base + `, "label_id": null, "region": "ca"}`:                                               "the access field label_id is missing",
		`{` + base + `, "label_id": [], "region": "ca"}`:                                                 "the access field label_id is missing",
		`{` + base + `, "label_id": "", "region": "ca"}`:                                                 "the access field label_id has an empty value",
		`{` + base + `, "label_id": {"a": 1}, "region": "ca"}`:                                           "the access field label_id is not a string",
		`{"id": 7, "updated_at": "2026-10-12T10:00:00Z", "title": ["x"], "label_id": 1, "region": "ca"}`: "the title is not a string",
		`{` + base + `, "label_id": 1, "region": "ca", "tags": [{"a": 1}]}`:                              "the tags is not a string",
		`{"id": 7, "label_id": 1, "region": "ca"}`:                                                       "updated_at is missing",
	} {
		if _, err := doc(js); err == nil || !strings.Contains(err.Error(), says) {
			t.Errorf("%s: %v, want %q", js, err, says)
		}
	}
	// a body that does not render: the template calls a value as a function
	bad, err := Load(writeView(t, strings.Replace(trackView, "by {{.artist}}", "by {{index .artist 3}}", 1)), []string{"label_id"})
	if err != nil {
		t.Fatal(err)
	}
	r := decode([]byte(`{` + base + `, "label_id": 1}`))
	if _, err := bad.document(r, "track/7.md"); err == nil || !strings.Contains(err.Error(), "render") {
		t.Errorf("a body that fails to render: %v", err)
	}
	// a text export is chunked as text, titled by the row's id when it has no title
	txt, err := Load(writeView(t, strings.Replace(trackView, "export: track/{{.id}}.md", "export: track/{{.id}}.txt", 1)), []string{"label_id"})
	if err != nil {
		t.Fatal(err)
	}
	r = decode([]byte(`{"id": 7, "updated_at": "2026-10-12T10:00:00Z", "artist": "Ann", "label_id": true}`))
	td, err := txt.document(r, "track/7.txt")
	if err != nil {
		t.Fatal(err)
	}
	if td.Title != "7" || len(td.Chunks) == 0 || td.Kinds[0] != pgstore.ChunkSection || !slices.Equal(td.Facets["label_id"], []string{"true"}) {
		t.Errorf("a text document: %+v", td)
	}
}
