package registrations

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// deriver is a build's deriver for tests: autorag's ids by default, its answers changeable after
// it is frozen; describes counts Describe's calls.
type deriver struct {
	formats   []string
	ids       map[string][2]string
	describes int
}

func autorag() *deriver {
	return &deriver{formats: []string{".pdf", ".docx"}, ids: map[string][2]string{".pdf": {"autorag/pdf", "1"}, ".docx": {"autorag/docx", "1+a"}}}
}

func (d *deriver) Formats() []string { return d.formats }
func (d *deriver) Describe(f string) (string, string) {
	d.describes++
	return d.ids[f][0], d.ids[f][1]
}
func (d *deriver) Derive(context.Context, string, io.ReaderAt, int64) (Derived, error) {
	return Derived{}, errors.New("unused")
}

// TestADeriverIsFrozenAtEntry: autorag's real ids register, each format described once; a later
// answer never reaches a document; the formats join the tables and their fingerprint; a Derived
// made under another identity is refused.
func TestADeriverIsFrozenAtEntry(t *testing.T) {
	d := autorag()
	tab, err := New(map[string]search.Chunker{".go": ver("1")}, d)
	if err != nil {
		t.Fatal(err)
	}
	if d.describes != 2 {
		t.Fatalf("Describe called %d times, want once a format", d.describes)
	}
	d.ids[".pdf"] = [2]string{"autorag/pdf", "2"}
	if f, ok := tab.Format(".pdf"); !ok || f != (Format{"autorag/pdf", "1"}) {
		t.Fatalf(".pdf: %+v, %v; want the frozen identity", f, ok)
	}
	if got := tab.Formats(); strings.Join(got, " ") != ".docx .pdf" || tab.Deriver() != d {
		t.Fatalf("formats %q", got)
	}
	tabs := tab.Tables()
	if tabs.Formats[".docx"] != (Format{"autorag/docx", "1+a"}) || tabs.Chunkers[".go"] != "1" {
		t.Fatalf("tables %+v", tabs)
	}
	without, _ := New(map[string]search.Chunker{".go": ver("1")}, nil)
	if tabs.Fingerprint() == without.Tables().Fingerprint() {
		t.Fatal("the formats are not in the fingerprint")
	}
	if err := tab.CheckDerived(".pdf", Derived{ID: "autorag/pdf", Version: "1"}); err != nil {
		t.Fatalf("the frozen identity refused: %v", err)
	}
	for _, c := range []struct {
		format string
		d      Derived
	}{
		{".pdf", Derived{ID: "autorag/pdf", Version: "2"}},
		{".pdf", Derived{ID: "autorag/docx", Version: "1"}},
		{".odt", Derived{ID: "autorag/odt", Version: "1"}},
	} {
		if err := tab.CheckDerived(c.format, c.d); err == nil {
			t.Errorf("%s %+v admitted", c.format, c.d)
		}
	}
	if k := tab.Kinds(); !k.Readable("a.pdf") || !k.Readable("b.docx") || k.Readable("c.odt") || k.Of("a.go", nil) != kind.Registered {
		t.Fatalf("kinds %+v: the formats are not readable", k)
	}
	if (*Table)(nil).Deriver() != nil || (*Table)(nil).Formats() != nil {
		t.Fatal("the nil Table has a deriver")
	}
	if _, ok := (*Table)(nil).Format(".pdf"); ok {
		t.Fatal("the nil Table derives")
	}
	// a deriver alone is a Table too
	if alone, err := New(nil, autorag()); err != nil || alone == nil || len(alone.Extensions()) != 0 {
		t.Fatalf("a deriver alone: %v, %v", alone, err)
	}
}

// TestMainRefusesABadDeriver: every refusal names the format and why.
func TestMainRefusesABadDeriver(t *testing.T) {
	for _, c := range []struct {
		formats []string
		ids     map[string][2]string
		name    string
		says    string
	}{
		{nil, nil, "deriver", "no format"},
		{[]string{"pdf"}, nil, "pdf", "a dot and 1 to 16"},
		{[]string{".PDF"}, nil, ".PDF", "a dot and 1 to 16"},
		{[]string{".pptx"}, nil, ".pptx", "not a Pro document format"},
		{[]string{".md"}, nil, ".md", "not a Pro document format"},
		{[]string{".pdf", ".pdf"}, map[string][2]string{".pdf": {"a", "1"}}, ".pdf", "named twice"},
		{[]string{".pdf"}, map[string][2]string{".pdf": {"auto rag", "1"}}, ".pdf", "deriver id"},
		{[]string{".pdf"}, map[string][2]string{".pdf": {"a@b", "1"}}, ".pdf", "deriver id"},
		{[]string{".pdf"}, map[string][2]string{".pdf": {"a:b", "1"}}, ".pdf", "deriver id"},
		{[]string{".pdf"}, map[string][2]string{".pdf": {"autorag/pdf", "1/2"}}, ".pdf", "deriver version"},
		{[]string{".pdf"}, map[string][2]string{".pdf": {"autorag/pdf", ""}}, ".pdf", "deriver version"},
	} {
		_, err := New(nil, &deriver{formats: c.formats, ids: c.ids})
		var re *Error
		if !errors.As(err, &re) || re.Name != c.name || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%v %v: %v; want a refusal naming %s, saying %q", c.formats, c.ids, err, c.name, c.says)
		}
	}
}
