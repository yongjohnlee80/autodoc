//go:build !unix

package fsbounds

import "os"

// links is 1: this platform's FileInfo carries no link count.
// NOTE: a second hard link is then not seen, the limit S5 names; AutoDoc's daemon serves unix.
func links(os.FileInfo) uint64 { return 1 }
