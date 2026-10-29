package usertheme

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/themes"
)

// A theme's name is a QML module segment, [a-z][a-z0-9_]*, and never a built-in's: anything else
// is refused, saying which rule it broke.
func TestCheckNameRefusesWhatCannotBeAModule(t *testing.T) {
	for _, ok := range []string{"a", "my_theme", "t2", "x_9_y"} {
		if err := CheckName(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "My", "2nd", "_lead", "with-dash", "dot.ted", "../up", "a/b", "spa ce", "ünï"} {
		if err := CheckName(bad); err == nil || !strings.Contains(err.Error(), "must match") {
			t.Errorf("%q: %v, want the pattern refusal", bad, err)
		}
	}
	for _, builtin := range themes.Names() {
		if err := CheckName(builtin); err == nil || !strings.Contains(err.Error(), "cannot be shadowed") {
			t.Errorf("built-in %q: %v, want the shadowing refusal", builtin, err)
		}
	}
}

// Create refuses a bad name and an unknown built-in before touching the disk, makes the folder it
// needs, writes the built-in's bytes as they are, and never overwrites: a second Create of the
// name fails and leaves the first file as it was.
func TestCreateRefusesAndNeverOverwrites(t *testing.T) {
	base := themes.Names()[0]
	dir := filepath.Join(t.TempDir(), "not", "yet")
	if _, err := Create(dir, "Bad", base); err == nil {
		t.Error("a bad name was created")
	}
	if _, err := Create(dir, "fine", "no_such_theme"); err == nil || !strings.Contains(err.Error(), `no built-in theme "no_such_theme"`) {
		t.Errorf("an unknown built-in: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("a refused Create made the folder: %v", err)
	}
	file, err := Create(dir, "fine", base)
	if err != nil {
		t.Fatal(err)
	}
	want, err := fs.ReadFile(themes.FS(), base+".qml")
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(file); string(got) != string(want) || file != filepath.Join(dir, "fine.qml") {
		t.Fatalf("created %s with %d bytes; want the built-in's %d at fine.qml", file, len(got), len(want))
	}
	if err := os.WriteFile(file, []byte("// mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Create(dir, "fine", base); !errors.Is(err, fs.ErrExist) {
		t.Errorf("a second Create: %v, want ErrExist", err)
	}
	if got, _ := os.ReadFile(file); string(got) != "// mine" {
		t.Errorf("a second Create overwrote the file: %q", got)
	}
}

// Open of a folder that is not there has no problems and no user themes. Of one that is, it skips
// folders and files that are not .qml, says which file has a bad name or does not validate (and
// keeps the latter registered, for repair), and lists the good ones as the user's.
func TestOpenSaysWhichFilesAreWrong(t *testing.T) {
	if c := Open(filepath.Join(t.TempDir(), "missing")); len(c.Problems) != 0 || len(c.Names()) != len(themes.Names()) {
		t.Errorf("a missing folder: problems %v, names %v", c.Problems, c.Names())
	}
	if c := Open(""); len(c.Problems) != 0 {
		t.Errorf("no folder: %v", c.Problems)
	}
	dir := t.TempDir()
	if _, err := Create(dir, "good", themes.Names()[0]); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"Bad-Name.qml": "Theme {}",
		"broken.qml":   `Theme { app.window: "invalid" }`,
		"notes.txt":    "not a theme",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub.qml"), 0o755); err != nil {
		t.Fatal(err)
	}
	c := Open(dir)
	joined := errors.Join(c.Problems...).Error()
	if len(c.Problems) != 2 || !strings.Contains(joined, filepath.Join(dir, "Bad-Name.qml")+":1:1: name") || !strings.Contains(joined, "broken.qml") {
		t.Fatalf("problems: %v; want the bad name and the invalid theme, each by its file", c.Problems)
	}
	users := c.Names()[len(themes.Names()):]
	if !slices.Equal(users, []string{"broken", "good"}) || !slices.Contains(c.Labels(), "good (user)") {
		t.Errorf("user themes %v, labels %v; want broken (kept for repair) and good", users, c.Labels())
	}
	if _, err := c.Values("good"); err != nil {
		t.Errorf("the good theme's values: %v", err)
	}
	if _, err := c.Values("nope"); err == nil || !strings.Contains(err.Error(), `no theme "nope"`) {
		t.Errorf("an unknown theme: %v", err)
	}
	if v, err := c.Values(themes.Names()[0]); err != nil || len(v) == 0 {
		t.Errorf("a built-in's values: %d, %v", len(v), err)
	}
	if got := c.ExportStyle("nope"); got != "light" {
		t.Errorf("an unknown theme's export style %q, want light", got)
	}
}

// The module loader's filesystem opens only a registered user theme's .qml that validates: an
// invalid path is ErrInvalid, anything else ErrNotExist, and only "." lists.
func TestTheLoadersFilesystemRefusesWhatIsNotAThemesFile(t *testing.T) {
	dir := t.TempDir()
	if _, err := Create(dir, "good", themes.Names()[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "stray.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := Open(dir).Files()
	for _, c := range []struct {
		name string
		want error
	}{
		{"../good.qml", fs.ErrInvalid},
		{"/good.qml", fs.ErrInvalid},
		{"good", fs.ErrNotExist},
		{"stray.txt", fs.ErrNotExist},
		{"other.qml", fs.ErrNotExist},
	} {
		if _, err := files.Open(c.name); !errors.Is(err, c.want) {
			t.Errorf("Open(%q): %v, want %v", c.name, err, c.want)
		}
		if _, err := fs.ReadFile(files, c.name); !errors.Is(err, c.want) {
			t.Errorf("ReadFile(%q): %v, want %v", c.name, err, c.want)
		}
	}
	rd := files.(fs.ReadDirFS)
	if _, err := rd.ReadDir("../x"); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadDir of an invalid path: %v", err)
	}
	if _, err := rd.ReadDir("sub"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadDir of a sub-folder: %v", err)
	}
	if es, err := rd.ReadDir("."); err != nil || len(es) != 1 || es[0].Name() != "good.qml" {
		t.Errorf("ReadDir(.): %v, %v; want good.qml alone", es, err)
	}
	if err := os.Remove(filepath.Join(dir, "good.qml")); err != nil {
		t.Fatal(err)
	}
	if _, err := files.Open("good.qml"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a registered theme deleted since: %v, want ErrNotExist", err)
	}
}

// Directory is themes/ beside the config file named, or beside the default config.
func TestDirectoryIsBesideTheConfig(t *testing.T) {
	got, err := Directory(filepath.Join("/etc", "autodoc", "config.toml"))
	if err != nil || got != filepath.Join("/etc", "autodoc", "themes") {
		t.Errorf("Directory of a named config: %q, %v", got, err)
	}
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "cfg"))
	got, err = Directory("")
	if err != nil || filepath.Base(got) != "themes" || !strings.HasPrefix(got, os.Getenv("XDG_CONFIG_HOME")) {
		t.Errorf("Directory of the default config: %q, %v", got, err)
	}
}
