package clock_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// waits are the timed waits a NOTE: must justify (the no-sleep-in-production convention). The
// package's own Sleep is their definition, not a use.
var waits = []string{"clock.Sleep(", "time.After(", "time.NewTimer(", "time.NewTicker(", "time.Tick("}

// TestProductionCodeSaysWhyItWaits: no production file sleeps (time.Sleep cannot be cancelled), and
// every timed wait has NOTE: in the comment directly above it, saying why the wait must be timed
// rather than a wait on the event itself. Tests may sleep.
func TestProductionCodeSaysWhyItWaits(t *testing.T) {
	root := filepath.Join("..", "..")
	checked, found := 0, 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "bin", "testdata", "node_modules", ".autodoc-test-logs":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") ||
			filepath.ToSlash(path) == "../../core/clock/clock.go" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		checked++
		lines := strings.Split(string(b), "\n")
		for i, line := range lines {
			code := strings.TrimSpace(line)
			if strings.HasPrefix(code, "//") {
				continue
			}
			if strings.Contains(code, "time.Sleep(") {
				t.Errorf("%s:%d: time.Sleep in production code: wait on the event, or on a cancellable timer with a NOTE", path, i+1)
			}
			for _, w := range waits {
				if !strings.Contains(code, w) {
					continue
				}
				found++
				if !noted(lines, i) {
					t.Errorf("%s:%d: %s without a NOTE: directly above saying why the wait is timed", path, i+1, w)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked < 100 || found < 10 {
		t.Fatalf("only %d files and %d waits checked: this test would show nothing", checked, found)
	}
}

// noted reports whether the comment block directly above line i holds NOTE:.
func noted(lines []string, i int) bool {
	for j := i - 1; j >= 0; j-- {
		l := strings.TrimSpace(lines[j])
		if !strings.HasPrefix(l, "//") {
			return false
		}
		if strings.Contains(l, "NOTE:") {
			return true
		}
	}
	return false
}
