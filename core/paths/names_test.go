package paths

import (
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// names are names from data as the crew has them: scopes, codenames, and the bytes Name escapes.
var names = []string{
	"autodoc:0123456789abcdef", "autovim:plugins", "nvim:Proj", "zen-2", "scout-7", "Gold", "a_b.c",
	"scout@autovim:plugins", "100%", "a b", "con", "x.", "", "a/b", `a\b`, "日本", strings.Repeat("Long:", 80),
}

// A name is one element by its OS's rule: a scope's ':' as it is on Linux and macOS, "@3A" on
// Windows; every other escape '@' and two hex digits, the hashed long form "@@", the empty name
// "@_". The mutant, named here: the '%' mark brought back.
func TestANameIsWrittenByItsOSsRule(t *testing.T) {
	for _, c := range []struct{ name, unix, windows string }{
		{"autodoc:0123456789abcdef", "autodoc:0123456789abcdef", "autodoc@3A0123456789abcdef"},
		{"autovim:plugins", "autovim:plugins", "autovim@3Aplugins"},
		{"zen-2", "zen-2", "zen-2"},
		{"Gold", "@47old", "@47old"},
		{"scout@autovim:plugins", "scout@40autovim:plugins", "scout@40autovim@3Aplugins"},
		{"100%", "100@25", "100@25"},
		{"", "@_", "@_"},
		{"con", "@63on", "@63on"},
	} {
		if got := NameFor(c.name, false); got != c.unix {
			t.Errorf("NameFor(%q) on Linux and macOS = %q, want %q", c.name, got, c.unix)
		}
		if got := NameFor(c.name, true); got != c.windows {
			t.Errorf("NameFor(%q) on Windows = %q, want %q", c.name, got, c.windows)
		}
	}
	long := strings.Repeat("Long:", 80)
	for _, windows := range []bool{false, true} {
		e := NameFor(long, windows)
		if !strings.Contains(e, nameHash) || len(e) > 240 {
			t.Errorf("a long name on windows=%v: %q, want it cut and hashed", windows, e)
		}
		if _, err := ParseNameFor(e, windows); !errors.Is(err, ErrNotName) {
			t.Errorf("a long name's hashed element parsed: %v", err)
		}
	}
}

// No name holds a '%', so a program handed it as a path finds it as it is: a percent-decode, a
// query decode and a file URL's round trip each leave it unchanged, by both OSes' rules. The
// mutant, named here: the '%' mark brought back (a decode makes "autodoc%3A…" "autodoc:…", the
// folder opencode could not find).
func TestANameSurvivesEveryDecodeAProgramMayRunOnIt(t *testing.T) {
	for _, windows := range []bool{false, true} {
		for _, n := range names {
			e := NameFor(n, windows)
			if strings.Contains(e, "%") {
				t.Errorf("NameFor(%q, windows=%v) = %q holds a %%", n, windows, e)
			}
			if d, err := url.PathUnescape(e); err != nil || d != e {
				t.Errorf("a percent-decode of %q: %q, %v", e, d, err)
			}
			if d, err := url.QueryUnescape(e); err != nil || d != e {
				t.Errorf("a query decode of %q: %q, %v", e, d, err)
			}
			p := "/home/u/agents/kb/" + e + "/zen"
			if u, err := url.Parse("file://" + p); err != nil || u.Path != p {
				t.Errorf("file://%s parsed: %+v, %v; want its path back", p, u, err)
			}
			u := url.URL{Scheme: "file", Path: p}
			if back, err := url.Parse(u.String()); err != nil || back.Path != p {
				t.Errorf("file URL %s of %q read back: %+v, %v", u.String(), p, back, err)
			}
		}
	}
}

// ParseName reads back exactly what Name writes on its OS, and nothing else, so two names never
// share an element: every name round-trips and stays apart from every other, a colon is refused on
// Windows and "@3A" on Linux and macOS, and a '%', a lowercase escape or a bare mark is refused on
// both.
func TestANameParsesBackOnlyFromItsOwnForm(t *testing.T) {
	for _, windows := range []bool{false, true} {
		seen := map[string]string{}
		for _, n := range names[:len(names)-1] { // the long one is hashed: not reversible
			e := NameFor(n, windows)
			if got, err := ParseNameFor(e, windows); err != nil || got != n {
				t.Errorf("ParseNameFor(%q, windows=%v) = %q, %v; want %q", e, windows, got, err, n)
			}
			if other, ok := seen[e]; ok {
				t.Errorf("%q and %q share the element %q", other, n, e)
			}
			seen[e] = n
		}
		refused := []string{"autodoc%3A01", "a@3a", "a@", "a@4", "a@G0", "Abc"}
		if windows {
			refused = append(refused, "autodoc:01")
		} else {
			refused = append(refused, "autodoc@3A01")
		}
		for _, e := range refused {
			if got, err := ParseNameFor(e, windows); !errors.Is(err, ErrNotName) {
				t.Errorf("ParseNameFor(%q, windows=%v) = %q, %v; want ErrNotName", e, windows, got, err)
			}
		}
	}
}

// An earlier build's element, golib paths.Encode's own %XX form, is read for its rename once; a
// name in this build's form, or any other, is not one.
func TestAnEarlierBuildsNameIsRead(t *testing.T) {
	if got, ok := ParseOldName("autodoc%3A0123456789abcdef"); !ok || got != "autodoc:0123456789abcdef" {
		t.Errorf("the earlier form: %q, %v", got, ok)
	}
	for _, e := range []string{"autodoc:0123456789abcdef", "autodoc@3A0123456789abcdef", "zen", "a%zz"} {
		if got, ok := ParseOldName(e); ok {
			t.Errorf("ParseOldName(%q) = %q, want it no earlier form", e, got)
		}
	}
}

// An agent's folder, made by Name in a real directory, is found again from its path as a program
// that percent-decodes its working directory (opencode) has it, on this OS.
func TestAFolderByNameIsFoundFromAPercentDecodedPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "kb", Name("autodoc:0123456789abcdef"), Name("scout-7"))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("an agent's folder by Name: %v", err)
	}
	decoded, err := url.PathUnescape(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(decoded); err != nil || !fi.IsDir() {
		t.Errorf("the folder from its percent-decoded path %s: %v", decoded, err)
	}
}
