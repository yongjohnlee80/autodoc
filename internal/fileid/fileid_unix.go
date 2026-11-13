//go:build linux || darwin

package fileid

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// id is the file's device and inode, as "dev-ino": the form core/store's ids have always had.
func id(path string) (string, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", errors.New("no inode for the file")
	}
	return fmt.Sprintf("%d-%d", st.Dev, st.Ino), nil
}
