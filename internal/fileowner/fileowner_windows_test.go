//go:build windows

package fileowner

import (
	"os"
	"path/filepath"
	"testing"
)

// othersFile is a file another user owns: a system file of Windows', which TrustedInstaller owns.
func othersFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll")
	if _, err := os.Stat(p); err != nil {
		t.Skipf("no %s here: %v", p, err)
	}
	return p
}
