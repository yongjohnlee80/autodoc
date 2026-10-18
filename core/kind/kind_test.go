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
		"flow.mmd": Text, "flow.MMD": Text, "rows.csv": Text, // diagrams and tables are plain text everywhere
		"c.yaml": YAML, "c.YML": YAML,
		"r.pdf": Derived, "r.DOCX": Derived, "r.doc": Derived, "r.odt": Derived,
	} {
		if got := Of(p, text); got != want {
			t.Errorf("Of(%s) = %v, want %v", p, got, want)
		}
	}
	if Of("app.log", nil) != Markdown {
		t.Error("an undeclared extension is Markdown, the default")
	}
	// a build's chunker cannot claim them either: the built-in kind would win
	if !IsBuiltIn(".mmd") || !IsBuiltIn(".csv") || !IsBuiltIn(".tsv") {
		t.Error(".mmd, .csv and .tsv are not built-in kinds' extensions")
	}
}

func TestTextExtensions(t *testing.T) {
	got, err := TextExtensions([]string{"log", " .RST ", ".log", "", "c++"})
	if err != nil || !reflect.DeepEqual(got, []string{".log", ".rst", ".c++"}) {
		t.Fatalf("TextExtensions = %v, %v", got, err)
	}
	// .mmd and .csv were a workspace's to declare before they were built in: a saved list holding them
	// still saves, without them
	got, err = TextExtensions([]string{".mmd", "log", "CSV"})
	if err != nil || !reflect.DeepEqual(got, []string{".log"}) {
		t.Fatalf("TextExtensions with the now built-in .mmd and .csv = %v, %v", got, err)
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
		{"a.pdf", nil, Derived},
		{"a.log", []string{".log"}, Text},
		{"a.py", nil, Text}, // a source language's: code, not Markdown
	} {
		if got := r.Of(c.path, c.text); got != c.want {
			t.Errorf("%s with text %v: %v, want %v", c.path, c.text, got, c.want)
		}
	}
	if Of("main.go", nil) != Text {
		t.Error("a build with no registrations reads .go as Markdown: a source language claims it")
	}
	if !Registered.IsText() || Registered.String() != "registered" {
		t.Error("a registered file is UTF-8 text")
	}
}

// TestADerivedFormatIsReadable: a format the build derives stays kind Derived, and is readable; one it
// does not derive is not.
func TestADerivedFormatIsReadable(t *testing.T) {
	r := Registrations{Derived: []string{".pdf"}}
	if r.Of("manual.PDF", nil) != Derived || !r.Readable("manual.PDF") {
		t.Fatal("a derived PDF is not a readable derived document")
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

// Text is valid UTF-8 with no NUL: what every reader of a text kind checks, and the editor too.
func TestTextual(t *testing.T) {
	for in, want := range map[string]bool{
		"": true, "plain": true, "héllo ✓": true,
		"nul\x00inside": false, "\xff\xfe": false, "cut \xe2\x9c": false,
	} {
		if got := Textual([]byte(in)); got != want {
			t.Errorf("Textual(%q) = %v, want %v", in, got, want)
		}
	}
}

// TestASourceLanguagesFileIsCode: a file a source language claims (golib's catalog, the one the
// editor highlights with) is plain text, never Markdown, whatever its case: a .sql page has no
// rendered view and no Markdown table keys. The decisions before it still win, and a name no
// language claims stays Markdown.
func TestASourceLanguagesFileIsCode(t *testing.T) {
	for p, want := range map[string]Kind{
		"q.sql": Text, "db/Q.SQL": Text, "a.mysql.sql": Text, "a.sqlite.sql": Text, "a.pgsql": Text,
		"main.go": Text, "lib.rs": Text, "x.py": Text, "x.ts": Text, "run.sh": Text, "init.lua": Text,
		"c.yaml": YAML, "notes.md": Markdown, "x.adoc": Markdown, "README": Markdown, "page.html": Derived,
	} {
		if got := Of(p, nil); got != want {
			t.Errorf("Of(%s) = %v, want %v", p, got, want)
		}
	}
	if !IsSource("schema.sql") || IsSource("notes.md") || IsSource("x.adoc") {
		t.Error("IsSource: .sql is code, .md and .adoc are not")
	}
	if (Registrations{Chunked: []string{".sql"}}).Of("q.sql", nil) != Registered {
		t.Error("a registered chunker's extension is no longer Registered")
	}
	if Of("q.sql", []string{".sql"}) != Text {
		t.Error("a workspace's text extension that a language claims is not text")
	}
}
