//go:build gui

package tui

import (
	"testing"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// Under --gui's style the results' DataTable is laid out and drawn natively, and Ctrl+V still
// reaches it: a block from the first cell, j and l growing it, the page untouched. Esc ends a
// selection or a count first, and then closes the panel, as in the terminal.
func TestTheGUIResultsTablesKeys(t *testing.T) {
	dir := t.TempDir()
	installPlugin5(t, dir, "kinds", "service", "kinds", tableManifest)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{prefs: testPrefs})
	logs := t.TempDir()
	r := runTUI(t, NewSession(d.sock, nil), Options{Plugins: Plugins{Dir: dir, LogDir: logs, Socket: d.sock},
		ProgramOptions: []tuidecl.ProgramOption{tuidecl.WithStyle(GUIStyle(&NoteEmbeds{}))}})
	r.s.WaitForText(t, "connected — autodoc v-test")
	t.Cleanup(func() {
		if p := onLoop(r, func() *pluginRun { return r.h.plugins.running["kinds"] }); p != nil {
			p.shutdown()
		}
	})
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("kinds", "run"); return true })
	r.waitResults(t, "k1", len(kindsRows))
	if bound := onLoop(r, func() bool {
		dt := r.h.plugins.c.ResultsTable(resultsView)
		return dt != nil && dt.BoundLayout() != widget.TableLayout(dt)
	}); !bound {
		t.Fatal("the results' table is not laid out natively under the GUI style")
	}
	wantCtrlVBlock(t, r)
	onLoop(r, func() bool { r.resultsCore().SetAnchor(nil); return true })
	wantEscCloses(t, r, func() {
		onLoop(r, func() bool { r.h.plugins.runPluginCommand("kinds", "run"); return true })
		r.waitResults(t, "k1", len(kindsRows))
	})
}
