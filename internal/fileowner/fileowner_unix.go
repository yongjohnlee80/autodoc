//go:build linux || darwin

package fileowner

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

// ownerOfFile is the uid of the object f holds, and whether it is this process's.
func ownerOfFile(f *os.File) (string, bool, error) {
	fi, err := f.Stat()
	if err != nil {
		return "", false, err
	}
	return uidOwner(fi)
}

// uidOwner is fi's uid, and whether it is this process's.
func uidOwner(fi os.FileInfo) (string, bool, error) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false, errors.New("no owner for the file")
	}
	return fmt.Sprintf("uid %d", st.Uid), int(st.Uid) == os.Getuid(), nil
}

// owner is the entry's uid, and whether it is this process's.
func owner(path string) (string, bool, error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return "", false, err
	}
	return uidOwner(fi)
}
