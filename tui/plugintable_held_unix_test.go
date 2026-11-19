//go:build linux || darwin

package tui

import (
	"fmt"
	"testing"
)

// On Unix, a file held open refuses no rename: an export over one replaces it as over any file,
// and no notice says it could not.
func TestAnExportOverAFileHeldOpenReplacesItOnUnix(t *testing.T) {
	t.Parallel()
	r, path, rows := exportOverAHeldFile(t)
	r.waitNotice(t, fmt.Sprintf("%d loaded rows exported to %s", rows, path))
	if records := readCSVFile(t, path); len(records) != rows+1 {
		t.Fatalf("the replaced file holds %d records, want %d", len(records), rows+1)
	}
	for _, n := range onLoop(r, func() []string {
		var out []string
		for _, n := range r.h.noticeLog.Items {
			out = append(out, n.Text)
		}
		return out
	}) {
		if n == notReplacedNotice(path) {
			t.Errorf("a notice says the file could not be replaced: %q", n)
		}
	}
}
