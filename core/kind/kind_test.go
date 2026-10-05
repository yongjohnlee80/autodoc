package kind

import (
	"errors"
	"reflect"
	"testing"
)

func TestOf(t *testing.T) {
	text := []string{".log", ".rst"}
	for p, want := range map[string]Kind{
		"a.md": Markdown, "a.MD": Markdown, "README": Markdown, "x.adoc": Markdown,
		"n.txt": Text, "n.TXT": Text, "app.log": Text, "doc.rst": Text,
		"c.yaml": YAML, "c.YML": YAML,
		"r.pdf": Pro, "r.DOCX": Pro, "r.doc": Pro, "r.odt": Pro,
	} {
		if got := Of(p, text); got != want {
			t.Errorf("Of(%s) = %v, want %v", p, got, want)
		}
	}
	if Of("app.log", nil) != Markdown {
		t.Error("an undeclared extension is Markdown, the default")
	}
}

func TestTextExtensions(t *testing.T) {
	got, err := TextExtensions([]string{"log", " .RST ", ".log", "", "c++"})
	if err != nil || !reflect.DeepEqual(got, []string{".log", ".rst", ".c++"}) {
		t.Fatalf("TextExtensions = %v, %v", got, err)
	}
	for _, bad := range []string{".pdf", "DOCX", ".md", "txt", ".yml", "a b", ".", ".toolongextension17", "../x"} {
		var e *ErrExtension
		if _, err := TextExtensions([]string{bad}); !errors.As(err, &e) {
			t.Errorf("%q was accepted (%v)", bad, err)
		}
	}
}

// TestARegistrationReadsItsExtension: a built-in kind wins, then a registered chunker's, then the
// workspace's own text extension; a registered file is text that validates as such.
func TestARegistrationReadsItsExtension(t *testing.T) {
	r := Registrations{Chunked: []string{".go", ".rs"}}
	for _, c := range []struct {
		path string
		text []string
		want Kind
	}{
		{"main.go", nil, Registered},
		{"MAIN.GO", nil, Registered},
		{"lib.rs", []string{".rs"}, Registered},
		{"notes.md", nil, Markdown},
		{"a.txt", nil, Text},
		{"a.yaml", nil, YAML},
		{"a.pdf", nil, Pro},
		{"a.log", []string{".log"}, Text},
		{"a.py", nil, Markdown},
	} {
		if got := r.Of(c.path, c.text); got != c.want {
			t.Errorf("%s with text %v: %v, want %v", c.path, c.text, got, c.want)
		}
	}
	if Of("main.go", nil) != Markdown {
		t.Error("a build with no registrations reads .go as Markdown, as before")
	}
	if !Registered.IsText() || Registered.String() != "registered" {
		t.Error("a registered file is UTF-8 text")
	}
}

// TestADerivedFormatIsReadable: a format the build derives stays kind Pro, and is readable; one it
// does not derive is not.
func TestADerivedFormatIsReadable(t *testing.T) {
	r := Registrations{Derived: []string{".pdf"}}
	if r.Of("manual.PDF", nil) != Pro || !r.Readable("manual.PDF") {
		t.Fatal("a derived PDF is not a readable Pro document")
	}
	if r.Readable("letter.docx") {
		t.Fatal("a format the build does not derive is readable")
	}
	if (Registrations{}).Readable("a.pdf") {
		t.Fatal("the zero value derives")
	}
	if Label("a/Manual.Pdf") != "PDF" || Label("b.docx") != "DOCX" {
		t.Fatalf("labels %q %q", Label("a/Manual.Pdf"), Label("b.docx"))
	}
}
