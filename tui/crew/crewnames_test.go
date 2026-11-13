package crew

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"

	gpaths "github.com/yongjohnlee80/golib/paths"

	"github.com/yongjohnlee80/autodoc/internal/localfs"
	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// earlier is the element the build before this one wrote for scope: golib paths.Encode's %XX form,
// every escape a %, the form a program that percent-decodes its working directory cannot find.
func earlier(scope string) string { return gpaths.Encode(scope) }

// earlierBuild writes, under base, what an earlier build left for a scope as the element old: its
// claim, its read todo stores and its agents' folder under workspace kb. An old element holding a
// colon (the scope's own spelling, older still) skips on Windows, which cannot hold one.
func earlierBuild(t *testing.T, base, old string) {
	t.Helper()
	if strings.Contains(old, ":") && runtime.GOOS == "windows" {
		t.Skip("a name holding a colon, which Windows cannot hold: nothing to rename there")
	}
	for _, f := range []struct{ path, body string }{
		{filepath.Join(base, ".crew-scopes", old), ""},
		{filepath.Join(base, ".crew-todos", old), `{"stores":["S"]}`},
		{filepath.Join(base, "kb", old, "zen", "AGENTS.md"), "zen's guide"},
	} {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.path, []byte(f.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// renamedTo checks base holds scope's files by CrewFileName, each with the body earlierBuild wrote
// as old, and none by old.
func renamedTo(t *testing.T, base, scope, old string) {
	t.Helper()
	enc := agents.CrewFileName(scope)
	for _, f := range []struct{ path, body string }{
		{filepath.Join(base, ".crew-scopes", enc), ""},
		{filepath.Join(base, ".crew-todos", enc), `{"stores":["S"]}`},
		{filepath.Join(base, "kb", enc, "zen", "AGENTS.md"), "zen's guide"},
	} {
		if b, err := os.ReadFile(f.path); err != nil || string(b) != f.body {
			t.Errorf("%s: %q, %v; want %q", f.path, b, err, f.body)
		}
	}
	for _, p := range []string{filepath.Join(base, ".crew-scopes", old), filepath.Join(base, ".crew-todos", old), filepath.Join(base, "kb", old)} {
		if _, err := os.Lstat(p); err == nil {
			t.Errorf("the old name is still there: %s", p)
		}
	}
}

// A crew identity's file name is legal on every OS and holds no '%': a scope's colon is kept where
// the OS can hold one and escaped on Windows, a codename (lower case, digits, hyphens) is itself, and
// two identities never share a name. The mutant, named here: CrewFileName back to golib
// paths.Encode (the scope's folder is autodoc%3A…, the one opencode could not find).
func TestACrewIdentityIsAFileNameOnEveryOS(t *testing.T) {
	scope := agents.CrewFileName("autodoc:0123456789abcdef")
	want := "autodoc:0123456789abcdef"
	if runtime.GOOS == "windows" {
		want = "autodoc@3A0123456789abcdef"
	}
	if scope != want || strings.ContainsAny(scope, `%/\`) {
		t.Errorf("CrewFileName(scope) = %q, want %q", scope, want)
	}
	if got := agents.CrewFileName("zen-2"); got != "zen-2" {
		t.Errorf("CrewFileName(codename) = %q, want it unchanged", got)
	}
	if agents.CrewFileName("autovim:plugins") == agents.CrewFileName("autovim_plugins") {
		t.Error("two scopes share a file name")
	}
	if id, err := agents.ParseCrewFileName(scope); err != nil || id != "autodoc:0123456789abcdef" {
		t.Errorf("ParseCrewFileName(%q) = %q, %v", scope, id, err)
	}
	// the folder is made where it is named, on this OS
	if err := os.MkdirAll(filepath.Join(t.TempDir(), "kb", scope, agents.CrewFileName("zen")), 0o700); err != nil {
		t.Fatalf("an agent's folder by CrewFileName: %v", err)
	}
}

// An earlier build's names (every escape a %) are renamed to CrewFileName's, their content kept,
// and a second run changes nothing. What is not a scope's (a workspace's own files) is left alone.
func TestAnEarlierBuildsCrewNamesAreRenamed(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000aa"
	old := earlier(scope)
	earlierBuild(t, base, old)
	if err := os.WriteFile(filepath.Join(base, "kb", "AGENTS.md"), []byte("the workspace's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if kept, err := migrateCrewNames(base); err != nil || len(kept) != 0 {
		t.Fatalf("migrateCrewNames: kept %v, %v", kept, err)
	}
	renamedTo(t, base, scope, old)
	if b, err := os.ReadFile(filepath.Join(base, "kb", "AGENTS.md")); err != nil || string(b) != "the workspace's" {
		t.Errorf("a workspace's own file: %q, %v", b, err)
	}
	if kept, err := migrateCrewNames(base); err != nil || len(kept) != 0 {
		t.Fatalf("a second run: kept %v, %v", kept, err)
	}
	renamedTo(t, base, scope, old)
}

// A workspace whose name starts with a dot (.kb is a valid one) has its agents' folders renamed
// too, their content kept; the agents' folder's own entries (.crew-fallbacks here) are not a
// workspace's, and what is in them stays as it is.
func TestADotNamedWorkspacesCrewNamesAreRenamed(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000ee"
	old := earlier(scope)
	earlierBuild(t, base, old)
	hidden := filepath.Join(base, ".kb", old, "zen", "AGENTS.md")
	own := filepath.Join(base, ".crew-fallbacks", old)
	for _, dir := range []string{filepath.Dir(hidden), own} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(hidden, []byte("zen's guide in .kb"), 0o600); err != nil {
		t.Fatal(err)
	}
	if kept, err := migrateCrewNames(base); err != nil || len(kept) != 0 {
		t.Fatalf("migrateCrewNames: kept %v, %v", kept, err)
	}
	renamedTo(t, base, scope, old)
	moved := filepath.Join(base, ".kb", agents.CrewFileName(scope), "zen", "AGENTS.md")
	if b, err := os.ReadFile(moved); err != nil || string(b) != "zen's guide in .kb" {
		t.Errorf("the .kb workspace's agent folder: %q, %v; want it renamed, its guide kept", b, err)
	}
	if _, err := os.Lstat(filepath.Join(base, ".kb", old)); err == nil {
		t.Error("the .kb workspace's old name is still there")
	}
	if _, err := os.Stat(own); err != nil {
		t.Errorf("the agents' own .crew-fallbacks entry was moved: %v", err)
	}
}

// A run cut short leaves some names old and some new; the next run renames the rest.
func TestACrewRenameCutShortIsFinishedNextTime(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000bb"
	old := earlier(scope)
	earlierBuild(t, base, old)
	// the claim was renamed before the run ended; the rest was not
	if err := os.Rename(filepath.Join(base, ".crew-scopes", old), filepath.Join(base, ".crew-scopes", agents.CrewFileName(scope))); err != nil {
		t.Fatal(err)
	}
	if kept, err := migrateCrewNames(base); err != nil || len(kept) != 0 {
		t.Fatalf("migrateCrewNames: kept %v, %v", kept, err)
	}
	renamedTo(t, base, scope, old)
}

// An old name whose new one is already there is never written over: both stay as they were, and
// the old one is reported. Both kinds collide: an agents' folder, which the OS would refuse to rename
// over a full one anyway, and a regular file (a todo feed's record), which a rename would replace.
// Each one's content is checked before the error, so a lost record is what a failure shows.
func TestAnOldCrewNameWhoseNewOneIsThereIsKept(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000cc"
	old := earlier(scope)
	earlierBuild(t, base, old)
	newer := filepath.Join(base, "kb", agents.CrewFileName(scope), "zen", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(newer), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("the newer guide"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldTodos, newerTodos := filepath.Join(base, ".crew-todos", old), filepath.Join(base, ".crew-todos", agents.CrewFileName(scope))
	if err := os.WriteFile(newerTodos, []byte(`{"stores":["newer"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := migrateCrewNames(base)
	oldDir := filepath.Join(base, "kb", old)
	for _, f := range []struct{ path, body, what string }{
		{newerTodos, `{"stores":["newer"]}`, "the new todo record was written over"},
		{oldTodos, `{"stores":["S"]}`, "the old todo record"},
		{newer, "the newer guide", "the new folder's guide was written over"},
		{filepath.Join(oldDir, "zen", "AGENTS.md"), "zen's guide", "the old folder's guide"},
	} {
		if b, rerr := os.ReadFile(f.path); rerr != nil || string(b) != f.body {
			t.Errorf("%s: %q, %v; want %q", f.what, b, rerr, f.body)
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{oldTodos, oldDir}; !slices.Equal(kept, want) {
		t.Errorf("kept %v, want %v", kept, want)
	}
}

// Two windows starting together rename once between them, and both say nothing went wrong.
func TestTwoWindowsRenameTheCrewNamesOnce(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000dd"
	old := earlier(scope)
	earlierBuild(t, base, old)
	var wg sync.WaitGroup
	errs := make([]error, 2)
	kept := make([][]string, 2)
	for i := range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); kept[i], errs[i] = migrateCrewNames(base) }()
	}
	wg.Wait()
	for i := range 2 {
		if errs[i] != nil || len(kept[i]) != 0 {
			t.Errorf("window %d: kept %v, %v", i, kept[i], errs[i])
		}
	}
	renamedTo(t, base, scope, old)
}

// A crew folder that is a file is an error the rename says, on every OS: on Windows, reading a
// file as a folder gives an error errors.Is takes for fs.ErrNotExist, which once passed for
// "nothing to rename" (localfs.ReadDirIfAny).
func TestACrewFolderThatIsAFileIsSaid(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, ".crew-scopes"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := migrateCrewNames(base); !errors.Is(err, localfs.ErrNotAFolder) {
		t.Fatalf("a .crew-scopes file: %v; want it said that it is not a folder", err)
	}
}

// A name from an older build still, a scope's own spelling, is renamed where CrewFileName writes it
// otherwise (an upper-case letter is escaped), and left as it is where it is already CrewFileName's
// (a lower-case scope, on Linux and macOS, keeps its colon). It skips on Windows, which cannot hold
// the colon.
func TestAScopesOwnSpellingIsRenamedOnlyWhereItDiffers(t *testing.T) {
	base := t.TempDir()
	const scope = "nvim:Proj"
	earlierBuild(t, base, scope)
	same := filepath.Join(base, "kb", "autodoc:00000000000000ff", "zen")
	if err := os.MkdirAll(same, 0o700); err != nil {
		t.Fatal(err)
	}
	if kept, err := migrateCrewNames(base); err != nil || len(kept) != 0 {
		t.Fatalf("migrateCrewNames: kept %v, %v", kept, err)
	}
	renamedTo(t, base, scope, scope)
	if _, err := os.Stat(same); err != nil {
		t.Errorf("a name already CrewFileName's was moved: %v", err)
	}
}
