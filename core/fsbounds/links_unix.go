//go:build unix

package fsbounds

import (
	"os"
	"syscall"
)

// links is fi's hard-link count.
func links(fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}
