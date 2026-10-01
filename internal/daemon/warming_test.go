package daemon

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/index"
)

// TestAProviderChangeNeverHoldsUpReads: while a provider change restarts a workspace — here held
// mid-restart — listing it, getting it, searching it and asking its status all answer at once, and
// its status says it is restarting; once the restart is done it no longer does.
func TestAProviderChangeNeverHoldsUpReads(t *testing.T) {
	m, _ := open(t)
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.md"), []byte("# A\n\nkestrel\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root}); err != nil {
		t.Fatal(err)
	}
	indexed(t, m, "kb", 1)

	held, release := make(chan struct{}), make(chan struct{})
	restartHook = func(string) { close(held); <-release }
	t.Cleanup(func() { restartHook = nil })
	done := make(chan struct{})
	go func() { m.SetEmbedding(nil); close(done) }()
	<-held // the workspace's indexer and follower stopped; its replacement not started

	within := func(what string, fn func()) {
		t.Helper()
		ok := make(chan struct{})
		go func() { fn(); close(ok) }()
		select {
		case <-ok:
		case <-time.After(2 * time.Second):
			t.Fatalf("%s waited on the restart", what)
		}
	}
	within("List", func() {
		if len(m.List()) != 1 {
			t.Error("the workspace is not listed")
		}
	})
	within("Get, Search and status", func() {
		ws, ok := m.Get("kb")
		if !ok {
			t.Error("the workspace is not served")
			return
		}
		res, err := ws.Index.Search(context.Background(), "kestrel", index.QueryOpts{Mode: index.ModeLexical})
		if err != nil || len(res.Hits) != 1 {
			t.Errorf("search while restarting: %d hits, %v", len(res.Hits), err)
		}
		if got := ws.Warming(); !slices.Contains(got, warmRestarting) {
			t.Errorf("while restarting, warming = %v, want %q", got, warmRestarting)
		}
		if _, err := ws.Docs.Read(context.Background(), "a.md"); err != nil {
			t.Errorf("reading a note while restarting: %v", err)
		}
	})
	close(release)
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the provider change did not finish")
	}
	ws, _ := m.Get("kb")
	if got := ws.Warming(); len(got) != 0 {
		t.Errorf("after the restart, warming = %v, want nothing", got)
	}
	indexed(t, m, "kb", 1) // the replacement serves the same index
}

// TestSettingUpAProviderIsReported: while a provider is set up, every workspace's status says so.
func TestSettingUpAProviderIsReported(t *testing.T) {
	m, _ := open(t)
	root := t.TempDir()
	if _, err := m.Add(context.Background(), config.Workspace{Name: "kb", Root: root}); err != nil {
		t.Fatal(err)
	}
	ws, _ := m.Get("kb")
	m.SetWarming("setting up the embedding provider Gemma")
	if got := ws.Warming(); !slices.Equal(got, []string{"setting up the embedding provider Gemma"}) {
		t.Errorf("warming = %v", got)
	}
	m.SetWarming("")
	if got := ws.Warming(); len(got) != 0 {
		t.Errorf("after the setup, warming = %v", got)
	}
}

// TestAPastDefaultExcludeMovesToTheCurrentOne: a workspace stored with exactly the old default
// exclude (.git/** alone) gets the current default when the daemon starts, as one added today
// would; one with patterns of its own keeps them.
func TestAPastDefaultExcludeMovesToTheCurrentOne(t *testing.T) {
	m, db := open(t)
	ctx := context.Background()
	if _, err := db.AddWorkspace(ctx, "old", t.TempDir(), []string{"**/*.md"}, []string{".git/**"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddWorkspace(ctx, "custom", t.TempDir(), []string{"**/*.md"}, []string{".git/**", "drafts/**"}); err != nil {
		t.Fatal(err)
	}
	if err := m.OpenAll(); err != nil {
		t.Fatal(err)
	}
	ws, err := db.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]string{}
	for _, w := range ws {
		got[w.Name] = w.Exclude
	}
	if !slices.Equal(got["old"], config.DefaultExclude) {
		t.Errorf("the workspace on the old default excludes %v, want %v", got["old"], config.DefaultExclude)
	}
	if !slices.Equal(got["custom"], []string{".git/**", "drafts/**"}) {
		t.Errorf("the workspace with its own patterns excludes %v, want them kept", got["custom"])
	}
	if w, _ := m.Get("old"); !slices.Equal(w.Exclude, config.DefaultExclude) {
		t.Errorf("the served workspace excludes %v, want the current default", w.Exclude)
	}
}
