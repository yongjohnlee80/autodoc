//go:build unix

package tui

import (
	"fmt"
	"os"
	"syscall"
)

// privateDir makes dir the user's alone before the editor socket goes in it: a real folder, not a
// link, owned by this user, mode 0700. A folder that was there with a wider mode is narrowed; one of
// another owner, or a link, is refused, and the socket is not served.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !fi.IsDir() {
		return fmt.Errorf("%s is not a folder", dir)
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() {
		return fmt.Errorf("%s is not this user's own", dir)
	}
	if fi.Mode().Perm() != 0o700 {
		return os.Chmod(dir, 0o700)
	}
	return nil
}
