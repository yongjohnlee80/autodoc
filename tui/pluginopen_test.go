package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// openerManifest is a protocol-9 plugin whose [open] claims .ipynb for its import command.
const openerManifest = "[[commands]]\nid = \"import\"\ntitle = \"Import\"\n[open]\nextensions = [\".ipynb\"]\ncommand = \"import\"\n"

// A file a plugin opens (its manifest's [open], ADR 1792457255) goes to that plugin's command, by
// its absolute path, from File › Open file… (and so file.open_path) and from any open the
// choke point serves; it never opens as text. Another file opens as it did. A second plugin claiming
// the extension is shadowed: never sent the file, and Manage plugins… says so on its row alone.
func TestAFileAPluginOpensGoesToItsCommand(t *testing.T) {
	dir := t.TempDir()
	installPluginAt(t, dir, "nb", "dialog", "session", openerManifest, 9)
	installPluginAt(t, dir, "zz", "dialog", "session", openerManifest, 9) // after nb in the list
	r, logs := pluginTUI(t, dir)
	// kb entered first: entering it after the open asked where the file lies would drop the open (a
	// TicketWorkspace), as a loaded runner's coverage job saw
	r.ready(t)
	nbDir := t.TempDir()
	nb := filepath.Join(nbDir, "x.ipynb")
	if err := os.WriteFile(nb, []byte("{\"cells\": []}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r.h.p.Post(func() { r.h.openAbsolute(nb) })
	waitSaid(t, logs, "nb", fmt.Sprintf("args import [%q]", nb))
	time.Sleep(200 * time.Millisecond) // an open of the file as text would have landed by now
	if f := r.file(); f.open && f.path == nb {
		t.Fatalf("the .ipynb opened as text, as well as going to its plugin: %+v", f)
	}

	r.h.p.Post(func() { r.h.openIn("", nb) }) // the choke point itself, as every other entry reaches it
	waitSaidN(t, logs, "nb", "args import", 2)

	txt := filepath.Join(nbDir, "notes.txt")
	if err := os.WriteFile(txt, []byte("plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	r.h.p.Post(func() { r.h.openAbsolute(txt) })
	r.waitOpen(t, "", txt)
	if got := said(t, logs, "zz"); len(got) != 0 {
		t.Errorf("the shadowed claimant was started or sent the file: %q", got)
	}

	r.h.p.Post(r.h.plugins.managePlugins)
	r.s.WaitFor(t, "the manager listed", func(string) bool { return onLoop(r, func() int { return len(r.h.plugins.managedPlugins.All()) }) == 2 })
	names := onLoop(r, func() map[string]any {
		out := map[string]any{}
		for i, m := range r.h.plugins.managedPlugins.All() {
			out[m.e.m.Name] = r.h.plugins.managedPluginRowOf(i, m)["name"]
		}
		return out
	})
	if names["zz"] != "Zz — opens .ipynb: shadowed by nb" || names["nb"] != "Nb" {
		t.Errorf("the manager's rows: %q; want zz's claim shadowed by nb, nb's plain", names)
	}
}

// A plugin's document names its workspace's root, so the plugin finds the note on disk
// (plugin.Document.File; ADR 1792457255): the Jupyter plugin starts a kernel in the note's folder
// and keeps its outputs beside it.
func TestAPluginsDocumentNamesItsWorkspacesRoot(t *testing.T) {
	r, _ := pluginTUI(t, t.TempDir())
	r.ready(t)
	r.h.p.Post(func() { r.h.openRef(fileRef{"kb", "a.md"}, false, nil) })
	r.waitOpen(t, "kb", "a.md")
	root := onLoop(r, func() string { return r.h.rootOf("kb") })
	if root == "" {
		t.Fatal("the workspace's root is not listed")
	}
	d := onLoop(r, func() plugin.Document { return r.h.plugins.document() })
	if d.Workspace != "kb" || d.Path != "a.md" || d.Root != root || d.File() != filepath.Join(root, "a.md") {
		t.Errorf("the document %+v (File %q); want kb's a.md under %s", d, d.File(), root)
	}
}
