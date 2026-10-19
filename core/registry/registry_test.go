package registry

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

const (
	c1 = "1111111111111111111111111111111111111111"
	c2 = "2222222222222222222222222222222222222222"
)

// The fixture is the registry's own output: written by its BuildIndex (autodoc-plugin-registry
// 390fa74), so the two sides of the format agree on its bytes, not only on its types.
func TestTheRegistrysOwnIndexDecodes(t *testing.T) {
	b, err := os.ReadFile("testdata/index.json")
	if err != nil {
		t.Fatal(err)
	}
	ix, err := Decode(b)
	if err != nil {
		t.Fatal(err)
	}
	e := ix.Entry(KindPlugin, "doc-stats")
	if e == nil || e.Declares.Commit != c2 || len(e.Audits) != 2 || e.Audits[0].Commit != c2 {
		t.Fatalf("doc-stats: %+v", e)
	}
	if a := e.AuditAt(c1); a == nil || a.Date != "2026-10-01" {
		t.Errorf("the older audit: %+v", a)
	}
	if w := ix.ByRepo(KindLua, "https://codeberg.org/example/wordcount/"); w == nil || w.Name != "wordcount" {
		t.Errorf("by repo, whatever its trailing .git or /: %+v", w)
	}
	if ix.ByRepo(KindPlugin, "https://codeberg.org/example/wordcount") != nil {
		t.Error("an entry was found under another kind")
	}
}

// AuditAt compares commits in full: a prefix, even 39 characters of one, is never the audited commit.
func TestAnAuditIsOfAFullCommitNeverAPrefix(t *testing.T) {
	const cx = "0123456789abcdef0123456789abcdef01234567"
	e := &IndexEntry{Audits: []IndexAudit{{Commit: cx, Model: "m", Date: "2026-10-01"}}}
	for _, c := range []string{cx[:7], cx[:12], cx[:39], cx + "8", strings.ToUpper(cx), ""} {
		if e.AuditAt(c) != nil {
			t.Errorf("%q matched the audit of %s", c, cx)
		}
	}
	if e.AuditAt(cx) == nil {
		t.Error("the audited commit itself did not match")
	}
	if (*IndexEntry)(nil).AuditAt(c1) != nil {
		t.Error("no entry has an audit")
	}
}

// Validate refuses each bad index with its own reason.
func TestValidateRefusesEachRule(t *testing.T) {
	good := func() Index {
		return Index{Format: Format, Generated: "2026-10-19T00:00:00Z", Entries: []IndexEntry{{
			Kind: KindPlugin, Name: "p", Repo: "https://github.com/o/p",
			Audits: []IndexAudit{{Commit: c1, Model: "m", Date: "2026-10-01", Report: PagesURL + ReportPath(KindPlugin, "p", c1)}},
		}}}
	}
	if err := Validate(good()); err != nil {
		t.Fatalf("the good index: %v", err)
	}
	for _, c := range []struct {
		name string
		edit func(*Index)
		want string
	}{
		{"format", func(ix *Index) { ix.Format = 2 }, "format 2"},
		{"generated", func(ix *Index) { ix.Generated = "yesterday" }, "RFC 3339"},
		{"kind", func(ix *Index) { ix.Entries[0].Kind = "theme" }, `kind "theme"`},
		{"name", func(ix *Index) { ix.Entries[0].Name = "a/b" }, `name "a/b"`},
		{"empty name", func(ix *Index) { ix.Entries[0].Name = "" }, `name ""`},
		{"twice", func(ix *Index) { ix.Entries = append(ix.Entries, ix.Entries[0]) }, "listed twice"},
		{"declares", func(ix *Index) { ix.Entries[0].Declares.Commit = c1[:7] }, "declares at"},
		{"http", func(ix *Index) { ix.Entries[0].Repo = "http://github.com/o/p" }, "https://"},
		{"host", func(ix *Index) { ix.Entries[0].Repo = "https://evil.example/o/p" }, `host "evil.example"`},
		{"path", func(ix *Index) { ix.Entries[0].Repo = "https://github.com/o/p/q" }, "<owner>/<repo>"},
		{"short audit", func(ix *Index) { ix.Entries[0].Audits[0].Commit = c1[:7] }, "full 40-character SHA"},
		{"report", func(ix *Index) { ix.Entries[0].Audits[0].Report = "https://evil.example/r.md" }, "report"},
		{"model", func(ix *Index) { ix.Entries[0].Audits[0].Model = " " }, "model and date"},
	} {
		ix := good()
		c.edit(&ix)
		if err := Validate(ix); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: %v, want %q", c.name, err, c.want)
		}
	}
}

// Decode refuses what is not an index at all, and ignores the fields it does not know.
func TestDecodeRefusesMalformedAndIgnoresNewFields(t *testing.T) {
	b, _ := os.ReadFile("testdata/index.json")
	for _, bad := range [][]byte{nil, []byte("{"), b[:len(b)/2], []byte(`[]`)} {
		if _, err := Decode(bad); err == nil {
			t.Errorf("%.20q decoded", bad)
		}
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	m["mirrors"] = []string{"x"}
	nb, _ := json.Marshal(m)
	if _, err := Decode(nb); err != nil {
		t.Errorf("a field it does not know: %v", err)
	}
}

func TestShortAndSameRepo(t *testing.T) {
	if Short(c1) != "1111111" || Short("ab") != "ab" {
		t.Error("Short")
	}
	if !SameRepo("https://GitHub.com/o/p.git", "https://github.com/o/p/") || SameRepo("", "") || SameRepo("https://github.com/o/p", "https://github.com/o/q") {
		t.Error("SameRepo")
	}
}
