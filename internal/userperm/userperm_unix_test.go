//go:build linux || darwin

package userperm

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
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
	write(t, key, "secret")
	for mode, exposed := range map[os.FileMode]bool{0o600: false, 0o400: false, 0o700: false, 0o640: true, 0o620: true, 0o604: true, 0o602: true} {
		if err := os.Chmod(key, mode); err != nil {
			t.Fatal(err)
		}
		_, err := read(t, key)
		if got := errors.Is(err, ErrExposed); got != exposed {
			t.Errorf("mode %v: %v, want exposed %v", mode, err, exposed)
		}
	}
}

// A FIFO is refused as no regular file, and opening it never blocks.
func TestAFIFOIsRefusedWithoutBlockingOnUnix(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := read(t, fifo); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, ErrExposed) {
			t.Errorf("a FIFO: %v, want ErrExposed", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OpenPrivate blocked on a FIFO")
	}
}

// CreatePrivate's file is 0600 from its creation, whatever the umask lets through.
func TestACreatedFileIs0600OnUnix(t *testing.T) {
	old := syscall.Umask(0)
	defer syscall.Umask(old)
	key := filepath.Join(t.TempDir(), "key")
	f, err := CreatePrivate(key)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if p := fi.Mode().Perm(); p != 0o600 {
		t.Errorf("a created file is %v, want 0600 before anything is written", p)
	}
}
