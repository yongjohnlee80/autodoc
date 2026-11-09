//go:build linux || darwin

package fileowner

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// owner is the entry's uid, and whether it is this process's.
func owner(path string) (string, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false, errors.New("no owner for the file")
	}
	return fmt.Sprintf("uid %d", st.Uid), int(st.Uid) == os.Getuid(), nil
}
