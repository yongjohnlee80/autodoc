package tui

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// Manage workspaces › Provider…: the daemon's first, then the stored providers; choosing one gives
// this workspace its own, and a provider that does not set up is refused with the reason.
func TestWorkspaceProviderDialog(t *testing.T) {
	ollama := newFakeOllama(t, "embedder")
	d := startManaged(t, map[string]string{"kb": noteDir(t, "a.md", "# A\n\nalpha\n")})
	ctx := context.Background()
	for _, sp := range []store.ProviderSpec{
		{Name: "local", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "embedder"},
		{Name: "missing", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "no-such-model"},
	} {
		if _, err := d.db.AddProvider(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.manageWorkspaces() })
	r.s.WaitForText(t, "Provider…")
	r.h.p.Post(func() { r.h.startWorkspaceProvider(0) })
	r.s.WaitForText(t, "embedding provider · kb")
	r.s.WaitForText(t, "uses the daemon's provider")
	if got := onLoop(r, func() []string { return r.h.providerChoices }); len(got) != 3 || got[0] != "" {
		t.Fatalf("choices = %q, want the daemon's then the two providers", got)
	}
	missing := onLoop(r, func() int {
		for i, n := range r.h.providerChoices {
			if n == "missing" {
				return i
			}
		}
		return -1
	})
	r.h.p.Post(func() { r.h.saveWorkspaceProvider(missing) })
	r.s.WaitForText(t, "not changed:")
	local := missing ^ 3 // the other of 1 and 2
	r.h.p.Post(func() { r.h.saveWorkspaceProvider(local) })
	r.waitNoticed(t, "kb: embedding with local")
	ws, _ := d.db.Workspaces(ctx)
	if p, err := d.db.WorkspaceProvider(ctx, ws[0].ID); err != nil || p != "local" {
		t.Fatalf("stored = %q, %v", p, err)
	}
	r.h.p.Post(func() { r.h.startWorkspaceProvider(0) })
	r.s.WaitForText(t, "uses its own provider: local")
}

// Reopened at once after a save, while the save's relisting is held back, the dialog shows the
// saved provider: it reads the workspace from the daemon, not from the last listing (Lector's
// review of #30, round 2; the relisting raced the reopen).
func TestTheProviderDialogReopenedAtOnceShowsTheSavedChoice(t *testing.T) {
	ollama := newFakeOllama(t, "embedder")
	d := startManaged(t, map[string]string{"kb": noteDir(t, "a.md", "# A\n\nalpha\n")})
	if _, err := d.db.AddProvider(context.Background(), store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: ollama.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	var holding atomic.Bool
	release := make(chan struct{})
	sess := NewSession(d.sock, nil)
	sess.beforeCall = func(method string, _ []any) {
		if method == "workspace.list" && holding.Load() {
			<-release
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.manageWorkspaces() })
	r.s.WaitForText(t, "Provider…")
	holding.Store(true) // from here every listing waits: the save's relisting cannot land first
	r.h.p.Post(func() {
		r.h.providerWorkspace, r.h.providerChoices = "kb", []string{"", "local"}
		r.h.saveWorkspaceProvider(1)
	})
	r.waitNoticed(t, "kb: embedding with local")
	r.h.p.Post(func() { r.h.startWorkspaceProvider(0) })
	time.Sleep(100 * time.Millisecond) // the reopen is under way, its listing held with the relisting
	close(release)
	r.s.WaitForText(t, "uses its own provider: local")
	if strings.Contains(r.s.String(), "uses the daemon's provider") {
		t.Fatal("the dialog showed the provider from before the save")
	}
}

// Two manager dialogs asked for in turn, the first's read answering last, leave the second on
// screen: a late open never replaces the dialog asked for after it (Lector's review of #30, r3).
func TestALateDialogOpenNeverReplacesALaterOne(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": noteDir(t, "a.md", "# A\n")})
	var held atomic.Int32 // only the first listing after arming waits
	var armed atomic.Bool
	release := make(chan struct{})
	sess := NewSession(d.sock, nil)
	sess.beforeCall = func(method string, _ []any) {
		if method == "workspace.list" && armed.Load() && held.Add(1) == 1 {
			<-release
		}
	}
	r := runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.manageWorkspaces() })
	r.s.WaitForText(t, "Schema…")
	armed.Store(true)
	r.h.p.Post(func() { r.h.startSchema(0) }) // its read is held
	r.s.WaitFor(t, "the schema read held", func(string) bool { return held.Load() == 1 })
	r.h.p.Post(func() { r.h.startPatterns(0) }) // its read answers at once
	r.s.WaitForText(t, "rules · kb")
	close(release) // the schema read answers now, last
	r.h.p.Post(func() {})
	time.Sleep(150 * time.Millisecond)
	r.s.WaitForText(t, "rules · kb")
	if strings.Contains(r.s.String(), "frontmatter schema · kb") {
		t.Fatal("the late Schema open replaced the Rules dialog asked for after it")
	}
}

// heldFirstList runs the TUI over a daemon with kb, where the first workspace.list after arm()
// waits until the returned release is called; the manager is open.
func heldFirstList(t *testing.T) (r *running, arm func(), held func() bool, release func()) {
	t.Helper()
	d := startManaged(t, map[string]string{"kb": noteDir(t, "a.md", "# A\n")})
	var n atomic.Int32
	var armed atomic.Bool
	ch := make(chan struct{})
	sess := NewSession(d.sock, nil)
	sess.beforeCall = func(method string, _ []any) {
		if method == "workspace.list" && armed.Load() && n.Add(1) == 1 {
			<-ch
		}
	}
	r = runTUI(t, sess, Options{})
	r.s.WaitForText(t, "· kb")
	r.h.p.Post(func() { r.h.manageWorkspaces() })
	r.s.WaitForText(t, "Schema…")
	return r, func() { armed.Store(true) }, func() bool { return n.Load() >= 1 }, func() { close(ch) }
}

// A synchronous dialog asked for after a pending Schema… stays: Rename… is not replaced when the
// schema read answers late (Lector's review of #30, r4).
func TestALateDialogOpenNeverReplacesALaterSyncDialog(t *testing.T) {
	r, arm, held, release := heldFirstList(t)
	arm()
	r.h.p.Post(func() { r.h.startSchema(0) })
	r.s.WaitFor(t, "the schema read held", func(string) bool { return held() })
	r.h.p.Post(func() { r.h.startRenameWorkspace(0) })
	r.s.WaitForText(t, "rename the workspace")
	release()
	time.Sleep(150 * time.Millisecond)
	r.s.WaitForText(t, "rename the workspace")
	if strings.Contains(r.s.String(), "frontmatter schema · kb") {
		t.Fatal("the late Schema open replaced Rename")
	}
}

// Closing the manager while Schema… reads leaves nothing to open over the page.
func TestALateDialogOpenNeverReopensAfterTheManagerCloses(t *testing.T) {
	r, arm, held, release := heldFirstList(t)
	arm()
	r.h.p.Post(func() { r.h.startSchema(0) })
	r.s.WaitFor(t, "the schema read held", func(string) bool { return held() })
	r.keys(t, esc()) // the manager's own dismissal
	r.s.WaitFor(t, "the manager closed", func(sc string) bool { return !strings.Contains(sc, "Schema…") })
	release()
	time.Sleep(150 * time.Millisecond)
	if strings.Contains(r.s.String(), "frontmatter schema · kb") {
		t.Fatal("the late Schema open came up after the manager was closed")
	}
}
