package daemon

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// operationsAt is a managed KB_OPERATIONS.md declaring autodoc_version v.
func operationsAt(v string) []byte {
	return []byte("---\ntype: kb\nautodoc_version: " + v + "\n---\nops " + v + "\n")
}

// A served workspace's root KB_OPERATIONS.md is brought up to this build's copy when it declares an
// older autodoc_version, and only then: one of the same or a newer version is kept (nothing is
// downgraded), one with no readable version is kept, and a workspace with none gets none.
func TestAServedKBsOlderOperationsIsRefreshed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "autodoc.db"))
	if err != nil {
		t.Fatal(err)
	}
	m := New(ctx, db, Options{Poll: 20 * time.Millisecond, BatchDelay: 5 * time.Millisecond,
		Operations: func() []byte { return operationsAt("0.3.8") }})
	t.Cleanup(func() {
		m.StopAll()
		cancel()
		_ = db.Close()
	})
	for i, c := range []struct {
		what  string
		have  []byte // nil: no copy
		after string // "": still no copy
	}{
		{"an older copy", operationsAt("0.3.6"), "ops 0.3.8"},
		{"the same version", operationsAt("0.3.8"), "ops 0.3.8"},
		{"a newer copy", operationsAt("0.9.0"), "ops 0.9.0"},
		{"no readable version", []byte("---\ntype: kb\n---\nmine\n"), "mine"},
		{"no copy", nil, ""},
	} {
		root := t.TempDir()
		p := filepath.Join(root, "KB_OPERATIONS.md")
		if c.have != nil {
			if err := os.WriteFile(p, c.have, 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := m.Add(context.Background(), workspace.Spec{Name: "kb" + string(rune('a'+i)), Root: root}); err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(p)
		switch {
		case c.after == "" && !errors.Is(err, fs.ErrNotExist):
			t.Errorf("%s: the workspace's start wrote %q (%v), want no copy", c.what, b, err)
		case c.after != "" && (err != nil || !strings.Contains(string(b), c.after)):
			t.Errorf("%s: the copy after the start is %q (%v), want it holding %q", c.what, b, err, c.after)
		}
	}
}

// The daemon of a newer release refreshes the KBs it already serves when it starts: a KB added
// under a build that refreshed nothing keeps its older copy until the newer daemon opens it.
func TestANewerDaemonRefreshesTheKBsItServesAtItsStart(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "autodoc.db")
	root := t.TempDir()
	p := filepath.Join(root, "KB_OPERATIONS.md")
	if err := os.WriteFile(p, operationsAt("0.3.6"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(o Options, first bool) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		db, err := store.Open(ctx, dbPath)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		o.Poll, o.BatchDelay = 20*time.Millisecond, 5*time.Millisecond
		m := New(ctx, db, o)
		defer m.StopAll()
		if first {
			if _, err := m.Add(ctx, workspace.Spec{Name: "kb", Root: root}); err != nil {
				t.Fatal(err)
			}
			return
		}
		if err := m.OpenAll(); err != nil {
			t.Fatal(err)
		}
	}
	run(Options{}, true) // a build with no copy of its own to give
	if got := read(t, p); !strings.Contains(got, "ops 0.3.6") {
		t.Fatalf("the premise, the older copy as it was: %q", got)
	}
	run(Options{Operations: func() []byte { return operationsAt("0.3.8") }}, false)
	if got := read(t, p); !strings.Contains(got, "ops 0.3.8") {
		t.Fatalf("after the newer daemon's start the copy is %q, want this build's (0.3.8)", got)
	}
}
