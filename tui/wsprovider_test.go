package tui

import (
	"context"
	"testing"

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
