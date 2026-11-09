//go:build linux || darwin

package proctree

import (
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// detachedFact is the helper's session id.
func detachedFact() string {
	sid, err := unix.Getsid(0)
	if err != nil {
		return "error " + err.Error()
	}
	return strconv.Itoa(sid)
}

// detached is whether the helper's session is not this process's: setsid gave it its own.
func detached(t *testing.T, fact string) bool {
	t.Helper()
	mine, err := unix.Getsid(0)
	if err != nil {
		t.Fatal(err)
	}
	return fact != strconv.Itoa(mine) && fact != "" && fact[0] != 'e'
}

// Unix has no console to give up.
func TestReleasingTheConsoleDoesNothingOnUnix(t *testing.T) {
	if ReleaseConsoleIfAlone() {
		t.Error("ReleaseConsoleIfAlone reported a console given up on Unix")
	}
}
