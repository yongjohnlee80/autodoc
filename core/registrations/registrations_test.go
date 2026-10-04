package registrations

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/kind"
)

// chunker is a registration whose version may change after it is frozen.
type chunker struct{ v *string }

func (c chunker) Version() string                        { return *c.v }
func (chunker) Chunk(search.Doc) ([]search.Chunk, error) { return nil, nil }

func ver(v string) chunker { return chunker{&v} }

// TestMainRefusesARegistrationNamingIt: every refusal names the offender and why.
func TestMainRefusesARegistrationNamingIt(t *testing.T) {
	for _, c := range []struct {
		ext  string
		c    search.Chunker
		says string
	}{
		{"go", ver("1"), "a dot and 1 to 16"},
		{".GO", ver("1"), "a dot and 1 to 16"},
		{".a-very-long-extension", ver("1"), "a dot and 1 to 16"},
		{".md", ver("1"), "built-in"},
		{".yml", ver("1"), "built-in"},
		{".pdf", ver("1"), "Pro document format"},
		{".go", nil, "no chunker"},
		{".go", ver(""), "version"},
		{".go", ver("code@1"), "version"},
		{".go", ver("code:1"), "version"},
		{".go", ver("code 1"), "version"},
		{".go", ver("code/1"), "version"},
		{".go", ver(strings.Repeat("v", 33)), "version"},
	} {
		_, err := New(map[string]search.Chunker{c.ext: c.c}, nil)
		var re *Error
		if !errors.As(err, &re) || re.Name != c.ext || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%q: %v; want a refusal naming it, saying %q", c.ext, err, c.says)
		}
	}
}

// TestRegistrationsAreFrozenAtEntry: a version is read once, so a chunker's later answer never
// reaches a document; lookups lower-case the path's extension; no chunkers is the nil Table, which
// answers as none.
func TestRegistrationsAreFrozenAtEntry(t *testing.T) {
	c := ver("code-go-1+text-5")
	tab, err := New(map[string]search.Chunker{".go": c, ".rs": ver("code-rs-1")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	*c.v = "code-go-2"
	if _, v, ok := tab.Chunker("pkg/Main.GO"); !ok || v != "code-go-1+text-5" {
		t.Fatalf("Main.GO: %q, %v; want the frozen version", v, ok)
	}
	if _, _, ok := tab.Chunker("notes.md"); ok {
		t.Fatal("a built-in file has a registered chunker")
	}
	if got := tab.Extensions(); !slices.Equal(got, []string{".go", ".rs"}) {
		t.Fatalf("extensions %q", got)
	}
	if got := tab.Collisions([]string{".log", ".rs", ".go"}); !slices.Equal(got, []string{".rs", ".go"}) {
		t.Fatalf("collisions %q", got)
	}
	vs := tab.Versions()
	vs[".go"] = "changed"
	if _, v, _ := tab.Chunker("a.go"); v != "code-go-1+text-5" {
		t.Fatal("Versions is not a copy")
	}
	none, err := New(nil, nil)
	if err != nil || none != nil {
		t.Fatalf("no chunkers: %v, %v; want the nil Table", none, err)
	}
	if _, _, ok := none.Chunker("a.go"); ok || none.Extensions() != nil || len(none.Versions()) != 0 || none.Collisions([]string{".go"}) != nil {
		t.Fatal("the nil Table answers as if it had registrations")
	}
	if none.Kinds().Of("a.go", nil) != kind.Markdown || tab.Kinds().Of("a.go", nil) != kind.Registered {
		t.Fatal("the kinds do not follow the registrations")
	}
}

// TestTheIdentityCharsets: versions keep the narrow charset; deriver ids add '/', so autorag's
// real ids are kept exactly; '@', ':' and whitespace never occur in either.
func TestTheIdentityCharsets(t *testing.T) {
	for _, id := range []string{"autorag/pdf", "autorag/docx", "x", strings.Repeat("a", 64)} {
		if !ValidDeriverID(id) {
			t.Errorf("deriver id %q refused", id)
		}
	}
	for _, id := range []string{"", "a@b", "a:b", "a b", "a\tb", strings.Repeat("a", 65)} {
		if ValidDeriverID(id) {
			t.Errorf("deriver id %q admitted", id)
		}
	}
	for _, v := range []string{"1", "1+a", "code-go-1+text-5", "v1.2_3"} {
		if !ValidVersion(v) {
			t.Errorf("version %q refused", v)
		}
	}
	for _, v := range []string{"autorag/pdf", "1@2", "1:2", " 1"} {
		if ValidVersion(v) {
			t.Errorf("version %q admitted", v)
		}
	}
}
