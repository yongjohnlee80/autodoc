package daemon

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// scaffolded is a served workspace named kb over a fresh root that indexes only Markdown, as a
// Zettelkasten type's does, so a type's own _schema/frontmatter.yaml is not admitted.
func scaffolded(t *testing.T) (*Workspaces, string) {
	t.Helper()
	m, _ := open(t)
	root := t.TempDir()
	if _, err := m.Add(context.Background(), workspace.Spec{Name: "kb", Root: root, Include: []string{"**/*.md"}}); err != nil {
		t.Fatal(err)
	}
	return m, root
}

func outcomes(rs []rpc.ScaffoldResult) map[string]string {
	out := map[string]string{}
	for _, r := range rs {
		out[r.Path] = r.Outcome
	}
	return out
}

func read(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A type's files are created, a file there is kept byte for byte, and a file the workspace does not
// index (the type's schema) is created and, on a re-apply, kept: never refused.
func TestScaffoldCreatesAndKeeps(t *testing.T) {
	m, root := scaffolded(t)
	ctx := context.Background()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	files := []rpc.ScaffoldFile{
		{Path: "AGENTS.md", Content: []byte("the type's")},
		{Path: "permanent/ABOUT.md", Content: []byte("# permanent/\n")},
		{Path: "_schema/frontmatter.yaml", Content: []byte("version: 2\n")},
	}
	rs, err := m.Scaffold(ctx, "kb", files)
	if err != nil {
		t.Fatal(err)
	}
	got := outcomes(rs)
	if got["AGENTS.md"] != rpc.ScaffoldKept || got["permanent/ABOUT.md"] != rpc.ScaffoldCreated || got["_schema/frontmatter.yaml"] != rpc.ScaffoldCreated {
		t.Fatalf("outcomes %v", got)
	}
	if read(t, filepath.Join(root, "AGENTS.md")) != "mine" {
		t.Fatal("an existing file was overwritten")
	}
	// the re-apply: everything there is kept, the unindexed schema included
	rs, err = m.Scaffold(ctx, "kb", files)
	if err != nil {
		t.Fatal(err)
	}
	for p, o := range outcomes(rs) {
		if o != rpc.ScaffoldKept {
			t.Errorf("re-apply: %s was %s, want kept", p, o)
		}
	}
	// an admitted new file reaches the index through the follower; the schema, not admitted, doesn't
	indexed(t, m, "kb", 2) // AGENTS.md and permanent/ABOUT.md
}

// A folder inside the KB that is a symlink to a directory outside: nothing is created outside, the
// file is refused, and the other files land. The same when the folder is swapped in between calls.
func TestScaffoldStaysUnderTheRoot(t *testing.T) {
	m, root := scaffolded(t)
	ctx := context.Background()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "notes")); err != nil {
		t.Skip("no symlinks here:", err)
	}
	rs, err := m.Scaffold(ctx, "kb", []rpc.ScaffoldFile{
		{Path: "notes/ABOUT.md", Content: []byte("x")},
		{Path: "other/ABOUT.md", Content: []byte("y")},
	})
	if err != nil {
		t.Fatal(err)
	}
	got := outcomes(rs)
	if got["notes/ABOUT.md"] != rpc.ScaffoldRefused || got["other/ABOUT.md"] != rpc.ScaffoldCreated {
		t.Fatalf("outcomes %v", got)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("a file was created outside the root: %v", entries)
	}
	// swapped: a real folder the first call wrote into becomes a symlink outside before the next
	if err := os.RemoveAll(filepath.Join(root, "other")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "other")); err != nil {
		t.Fatal(err)
	}
	rs, err = m.Scaffold(ctx, "kb", []rpc.ScaffoldFile{{Path: "other/new.md", Content: []byte("z")}})
	if err != nil {
		t.Fatal(err)
	}
	if outcomes(rs)["other/new.md"] != rpc.ScaffoldRefused {
		t.Fatalf("after the swap: %v", outcomes(rs))
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("after the swap, a file was created outside the root: %v", entries)
	}
}

// An immutable folder's descriptor is skipped when the folder holds anything, created when it is
// absent or empty; a mark naming another folder, or a file that is not the descriptor, is refused.
func TestScaffoldImmutableFolders(t *testing.T) {
	m, root := scaffolded(t)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(root, "raw"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "raw", "source.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	rs, err := m.Scaffold(ctx, "kb", []rpc.ScaffoldFile{
		{Path: "raw/ABOUT.md", Content: []byte("x"), Immutable: "raw"},
		{Path: "empty/ABOUT.md", Content: []byte("x"), Immutable: "empty"},
		{Path: "fresh/ABOUT.md", Content: []byte("x"), Immutable: "fresh"},
		{Path: "other/ABOUT.md", Content: []byte("x"), Immutable: "raw"},
		{Path: "raw/notes.md", Content: []byte("x"), Immutable: "raw"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"raw/ABOUT.md": rpc.ScaffoldSkipped, "empty/ABOUT.md": rpc.ScaffoldCreated, "fresh/ABOUT.md": rpc.ScaffoldCreated,
		"other/ABOUT.md": rpc.ScaffoldRefused, "raw/notes.md": rpc.ScaffoldRefused,
	}
	if got := outcomes(rs); !equalMaps(got, want) {
		t.Fatalf("outcomes %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(root, "raw", "ABOUT.md")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a descriptor was written into a non-empty immutable folder")
	}
}

// The root KB_OPERATIONS.md, marked managed, replaces only a copy whose version parses and is
// older; managed on any other path is a plain create.
func TestScaffoldManaged(t *testing.T) {
	m, root := scaffolded(t)
	ctx := context.Background()
	ops := func(v string) []byte {
		return []byte("---\ntype: kb\nautodoc_version: " + v + "\n---\nops " + v + "\n")
	}
	write := func(rel string, b []byte) {
		if err := os.WriteFile(filepath.Join(root, rel), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		have    []byte
		outcome string
		after   string
	}{
		{nil, rpc.ScaffoldCreated, "ops 0.2.0"},
		{ops("0.1.20"), rpc.ScaffoldUpdated, "ops 0.2.0"},
		{ops("0.2.0"), rpc.ScaffoldKept, "ops 0.2.0"},
		{ops("0.3.0"), rpc.ScaffoldKept, "ops 0.3.0"},
		{[]byte("---\ntype: kb\n---\nmine\n"), rpc.ScaffoldKept, "mine"},
	} {
		_ = os.Remove(filepath.Join(root, "KB_OPERATIONS.md"))
		if c.have != nil {
			write("KB_OPERATIONS.md", c.have)
		}
		rs, err := m.Scaffold(ctx, "kb", []rpc.ScaffoldFile{{Path: "KB_OPERATIONS.md", Content: ops("0.2.0"), Managed: true}})
		if err != nil {
			t.Fatal(err)
		}
		if got := rs[0].Outcome; got != c.outcome || !strings.Contains(read(t, filepath.Join(root, "KB_OPERATIONS.md")), c.after) {
			t.Errorf("have %q: %s, the file %q; want %s, holding %q", c.have, got, read(t, filepath.Join(root, "KB_OPERATIONS.md")), c.outcome, c.after)
		}
	}
	write("NOTES.md", ops("0.1.0"))
	rs, err := m.Scaffold(ctx, "kb", []rpc.ScaffoldFile{{Path: "NOTES.md", Content: ops("0.2.0"), Managed: true}})
	if err != nil {
		t.Fatal(err)
	}
	if rs[0].Outcome != rpc.ScaffoldKept || !strings.Contains(read(t, filepath.Join(root, "NOTES.md")), "ops 0.1.0") {
		t.Fatalf("managed on another path replaced it: %+v", rs[0])
	}
}

// The daemon's own bounds: a bad file is refused as its result while the others land; too many
// files is the call's error before anything is written; an unknown workspace is the call's error.
func TestScaffoldBounds(t *testing.T) {
	m, root := scaffolded(t)
	ctx := context.Background()
	// notes/ exists, so the filesystem itself would resolve notes/../sneaky.md: only the daemon's
	// own path check refuses it
	if err := os.MkdirAll(filepath.Join(root, "notes"), 0o755); err != nil {
		t.Fatal(err)
	}
	rs, err := m.Scaffold(ctx, "kb", []rpc.ScaffoldFile{
		{Path: "../escape.md", Content: []byte("x")},
		{Path: "notes/../sneaky.md", Content: []byte("x")}, // not clean: the daemon refuses what the filesystem would resolve
		{Path: "/abs.md", Content: []byte("x")},
		{Path: "", Content: []byte("x")},
		{Path: "bin.md", Content: []byte("a\x00b")},
		{Path: "big.md", Content: []byte(strings.Repeat("a", 256<<10+1))},
		{Path: "ok.md", Content: []byte("x")},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rs {
		want := rpc.ScaffoldRefused
		if r.Path == "ok.md" {
			want = rpc.ScaffoldCreated
		}
		if r.Outcome != want || (want == rpc.ScaffoldRefused && r.Err == "") {
			t.Errorf("%q: %s %q, want %s", r.Path, r.Outcome, r.Err, want)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "sneaky.md")); err == nil {
		t.Fatal("a path that was not clean was written")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root), "escape.md")); err == nil {
		t.Fatal("a file was written outside the root")
	}
	many := make([]rpc.ScaffoldFile, rpc.MaxScaffoldFiles+1)
	for i := range many {
		many[i] = rpc.ScaffoldFile{Path: "many/" + strings.Repeat("a", i+1) + ".md", Content: []byte("x")}
	}
	if _, err := m.Scaffold(ctx, "kb", many); err == nil {
		t.Fatal("65 files were taken")
	}
	if _, err := os.Stat(filepath.Join(root, "many")); err == nil {
		t.Fatal("a call refused for too many files wrote some")
	}
	if _, err := m.Scaffold(ctx, "nope", nil); !errors.Is(err, store.ErrNoWorkspace) {
		t.Fatalf("an unknown workspace: %v", err)
	}
}

// A cancelled call returns what landed before it with the error, never claiming nothing did.
func TestScaffoldCancelledKeepsWhatLanded(t *testing.T) {
	m, _ := scaffolded(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	rs, err := m.Scaffold(ctx, "kb", []rpc.ScaffoldFile{{Path: "a.md", Content: []byte("x")}})
	if !errors.Is(err, context.Canceled) || len(rs) != 0 {
		t.Fatalf("a call cancelled before its first file: %v, %v", rs, err)
	}
}

func equalMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}
