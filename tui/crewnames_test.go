package tui

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// earlierBuild writes, under base, what a build before crewFileName left for scope: its claim, its
// read todo stores and its agents' folder under workspace kb, each by the scope's own spelling. It
// skips on Windows, where a name cannot hold the colon every scope has: nothing there to rename.
func earlierBuild(t *testing.T, base, scope string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("an earlier build's names hold a colon, which Windows cannot hold: nothing to rename there")
	}
	for _, f := range []struct{ path, body string }{
		{filepath.Join(base, ".crew-scopes", scope), ""},
		{filepath.Join(base, ".crew-todos", scope), `{"stores":["S"]}`},
		{filepath.Join(base, "kb", scope, "zen", "AGENTS.md"), "zen's guide"},
	} {
		if err := os.MkdirAll(filepath.Dir(f.path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(f.path, []byte(f.body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// renamedTo checks base holds scope's files by crewFileName, each with the body earlierBuild wrote,
// and none by the old spelling.
func renamedTo(t *testing.T, base, scope string) {
	t.Helper()
	enc := crewFileName(scope)
	for _, f := range []struct{ path, body string }{
		{filepath.Join(base, ".crew-scopes", enc), ""},
		{filepath.Join(base, ".crew-todos", enc), `{"stores":["S"]}`},
		{filepath.Join(base, "kb", enc, "zen", "AGENTS.md"), "zen's guide"},
	} {
		if b, err := os.ReadFile(f.path); err != nil || string(b) != f.body {
			t.Errorf("%s: %q, %v; want %q", f.path, b, err, f.body)
		}
	}
	for _, old := range []string{filepath.Join(base, ".crew-scopes", scope), filepath.Join(base, ".crew-todos", scope), filepath.Join(base, "kb", scope)} {
		if _, err := os.Lstat(old); err == nil {
			t.Errorf("the old name is still there: %s", old)
		}
	}
}

// A crew identity's file name is legal on every OS: a scope's colon is escaped, a codename (lower
// case, digits, hyphens) is itself, and two identities never share a name.
func TestACrewIdentityIsAFileNameOnEveryOS(t *testing.T) {
	scope := crewFileName("autodoc:0123456789abcdef")
	if strings.ContainsAny(scope, `:/\`) {
		t.Errorf("crewFileName(scope) = %q, holds a character a file name can't", scope)
	}
	if got := crewFileName("zen-2"); got != "zen-2" {
		t.Errorf("crewFileName(codename) = %q, want it unchanged", got)
	}
	if crewFileName("autovim:plugins") == crewFileName("autovim_plugins") {
		t.Error("two scopes share a file name")
	}
	// the folder is made where it is named, on this OS
	if err := os.MkdirAll(filepath.Join(t.TempDir(), "kb", scope, crewFileName("zen")), 0o700); err != nil {
		t.Fatalf("an agent's folder by crewFileName: %v", err)
	}
}

// An earlier build's names are renamed to crewFileName's, their content kept, and a second run
// changes nothing. What is not a scope's (a workspace's own files) is left alone.
func TestAnEarlierBuildsCrewNamesAreRenamed(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000aa"
	earlierBuild(t, base, scope)
	if err := os.WriteFile(filepath.Join(base, "kb", "AGENTS.md"), []byte("the workspace's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if kept, err := migrateCrewNames(base); err != nil || len(kept) != 0 {
		t.Fatalf("migrateCrewNames: kept %v, %v", kept, err)
	}
	renamedTo(t, base, scope)
	if b, err := os.ReadFile(filepath.Join(base, "kb", "AGENTS.md")); err != nil || string(b) != "the workspace's" {
		t.Errorf("a workspace's own file: %q, %v", b, err)
	}
	if kept, err := migrateCrewNames(base); err != nil || len(kept) != 0 {
		t.Fatalf("a second run: kept %v, %v", kept, err)
	}
	renamedTo(t, base, scope)
}

// A workspace whose name starts with a dot (.kb is a valid one) has its agents' folders renamed
// too, their content kept; the agents' folder's own entries (.crew-fallbacks here) are not a
// workspace's, and what is in them stays as it is.
func TestADotNamedWorkspacesCrewNamesAreRenamed(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000ee"
	earlierBuild(t, base, scope)
	hidden := filepath.Join(base, ".kb", scope, "zen", "AGENTS.md")
	own := filepath.Join(base, ".crew-fallbacks", scope)
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
	renamedTo(t, base, scope)
	moved := filepath.Join(base, ".kb", crewFileName(scope), "zen", "AGENTS.md")
	if b, err := os.ReadFile(moved); err != nil || string(b) != "zen's guide in .kb" {
		t.Errorf("the .kb workspace's agent folder: %q, %v; want it renamed, its guide kept", b, err)
	}
	if _, err := os.Lstat(filepath.Join(base, ".kb", scope)); err == nil {
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
	earlierBuild(t, base, scope)
	// the claim was renamed before the run ended; the rest was not
	if err := os.Rename(filepath.Join(base, ".crew-scopes", scope), filepath.Join(base, ".crew-scopes", crewFileName(scope))); err != nil {
		t.Fatal(err)
	}
	if kept, err := migrateCrewNames(base); err != nil || len(kept) != 0 {
		t.Fatalf("migrateCrewNames: kept %v, %v", kept, err)
	}
	renamedTo(t, base, scope)
}

// An old name whose new one is already there is never written over: both stay as they were, and
// the old one is reported.
func TestAnOldCrewNameWhoseNewOneIsThereIsKept(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000cc"
	earlierBuild(t, base, scope)
	newer := filepath.Join(base, "kb", crewFileName(scope), "zen", "AGENTS.md")
	if err := os.MkdirAll(filepath.Dir(newer), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newer, []byte("the newer guide"), 0o600); err != nil {
		t.Fatal(err)
	}
	kept, err := migrateCrewNames(base)
	if err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(base, "kb", scope)
	if len(kept) != 1 || kept[0] != old {
		t.Errorf("kept %v, want the old folder %s", kept, old)
	}
	if b, err := os.ReadFile(filepath.Join(old, "zen", "AGENTS.md")); err != nil || string(b) != "zen's guide" {
		t.Errorf("the old folder's guide: %q, %v", b, err)
	}
	if b, err := os.ReadFile(newer); err != nil || string(b) != "the newer guide" {
		t.Errorf("the new folder's guide was written over: %q, %v", b, err)
	}
}

// Two windows starting together rename once between them, and both say nothing went wrong.
func TestTwoWindowsRenameTheCrewNamesOnce(t *testing.T) {
	base := t.TempDir()
	const scope = "autodoc:00000000000000dd"
	earlierBuild(t, base, scope)
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
	renamedTo(t, base, scope)
}
