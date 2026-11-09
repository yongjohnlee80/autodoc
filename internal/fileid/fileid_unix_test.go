//go:build linux || darwin

package fileid

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// The Unix id is the "dev-ino" string core/store's identity() has always made, so no store id on
// Linux or macOS changes with the seam.
func TestTheUnixIDIsTheStoresOldIdentity(t *testing.T) {
	p := filepath.Join(t.TempDir(), "store.db")
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if got, want := func() string { id, _ := ID(p); return id }(), fmt.Sprintf("%d-%d", st.Dev, st.Ino); got != want {
		t.Errorf("ID = %q; the store's identity was %q", got, want)
	}
}
