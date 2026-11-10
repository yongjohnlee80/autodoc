//go:build !windows

package app

import (
	"fmt"
	"os"
	"testing"
)

// socketIsPrivate reports whether the daemon's endpoint sock is this user's alone, as Unix keeps a
// socket: its file 0600.
func socketIsPrivate(t *testing.T, sock string) error {
	t.Helper()
	fi, err := os.Stat(sock)
	if err != nil {
		return err
	}
	if fi.Mode().Perm() != 0o600 {
		return fmt.Errorf("the socket's mode is %v, want 0600", fi.Mode().Perm())
	}
	return nil
}
