//go:build linux || darwin

package fileowner

import (
	"os"
	"path/filepath"
	"testing"
)

// othersFile is a file another user owns: as root (a container), one this test makes and gives to an
// unprivileged uid; otherwise the root folder, which root owns.
func othersFile(t *testing.T) string {
	t.Helper()
	if os.Getuid() != 0 {
		return "/"
	}
	p := filepath.Join(t.TempDir(), "theirs")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(p, 54321, 54321); err != nil {
		t.Fatal(err)
	}
	return p
}
