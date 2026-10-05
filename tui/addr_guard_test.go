package tui

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// TestSessionAddrIsReadOnlyThroughItsAccessor: a text assertion over the package's sources. The
// session's address changes under its mutex when a resolver re-points it (setAddr, from Connect on
// a worker), so it is read through address() everywhere; a direct `.addr` read elsewhere races
// with that write. Only client.go, which owns the field and its lock, may touch it.
func TestSessionAddrIsReadOnlyThroughItsAccessor(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	direct := regexp.MustCompile(`session\.addr\b`)
	for _, f := range files {
		if f == "client.go" || strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(data), "\n") {
			if direct.MatchString(line) {
				t.Errorf("%s:%d reads session.addr directly; use session.address(): %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
