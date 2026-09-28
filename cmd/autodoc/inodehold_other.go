//go:build !linux

package main

import "errors"

// holdInode has no portable form (O_PATH is Linux's). The caller falls back to comparing the path's
// stat: sound where inode numbers are not recycled, and no worse than declining to remove the
// socket, which would make the next start look occupied while nothing listens.
func holdInode(string) (inodeHold, error) {
	return nil, errors.New("no inode hold on this platform")
}
