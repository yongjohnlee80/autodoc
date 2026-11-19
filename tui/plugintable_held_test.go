package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// exportOverAHeldFile runs the table plugin's query, writes "old" where the export will go, holds
// that file open as another program would, exports the loaded rows over it and answers the replace
// question yes. It returns the export's path and the rows it was asked to write; the caller closes
// nothing (the hold ends with the cell).
func exportOverAHeldFile(t *testing.T) (r *running, path string, rows int) {
	t.Helper()
	dir := t.TempDir()
	installPlugin4(t, dir, "rows", "service", "table", tableManifest)
	r, _ = pluginTUI(t, dir)
	onLoop(r, func() bool { r.h.plugins.runPluginCommand("rows", "run"); return true })
	r.waitResults(t, "q1", tablePage+resultsPage)
	path = filepath.Join(t.TempDir(), "rows.csv")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path) // os.Open shares no delete: on Windows, nothing may rename over it
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = held.Close() })
	onLoop(r, func() bool { r.h.plugins.exportResults(path); return true })
	r.s.WaitFor(t, "the replace question", func(string) bool {
		return onLoop(r, func() bool { v, _ := r.h.p.Tree().Source("App.askTitle"); return v.Raw == "replace rows.csv?" })
	})
	onLoop(r, func() bool { r.h.asker.Accepted(); return true })
	return r, path, tablePage + resultsPage
}

// notReplacedNotice is what an export says when its rename over an existing file is refused.
func notReplacedNotice(path string) string {
	return fmt.Sprintf("%s could not be replaced: it may be open in another program, or the folder may not be writable. The old file is kept.", path)
}
