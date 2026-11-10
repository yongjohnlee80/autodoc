//go:build linux || darwin

package private

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// expose gives others read access, as a file written 0644 has.
func expose(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
}

// On Unix any group or other bit exposes a file; the owner's own bits alone don't.
func TestEachGroupOrOtherBitExposesOnUnix(t *testing.T) {
	key := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(key, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for mode, exposed := range map[os.FileMode]bool{0o600: false, 0o400: false, 0o700: false, 0o640: true, 0o620: true, 0o604: true, 0o602: true} {
		if err := os.Chmod(key, mode); err != nil {
			t.Fatal(err)
		}
		if got := errors.Is(Check(key), ErrExposed); got != exposed {
			t.Errorf("mode %v: exposed %v, want %v", mode, got, exposed)
		}
	}
}
