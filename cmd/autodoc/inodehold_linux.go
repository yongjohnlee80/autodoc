package main

import (
	"fmt"
	"syscall"
)

// holdInode takes an O_PATH reference to the socket's inode, which keeps the inode allocated, so a
// successor binding the same path cannot be handed its number: the shutdown identity check depends
// on exactly that. O_PATH opens the file itself, which is why it works on a socket (an ordinary open
// of a bound socket fails with ENXIO). The kernel drops the reference on exit, a hard kill included.
func holdInode(path string) (inodeHold, error) {
	// O_PATH is part of the Linux ABI but not exported by syscall on every Go version
	const oPath = 0x200000
	fd, err := syscall.Open(path, oPath|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("O_PATH open %s: %w", path, err)
	}
	return fdHold(fd), nil
}

type fdHold int

func (f fdHold) release() { _ = syscall.Close(int(f)) }
