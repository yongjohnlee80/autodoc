package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
	"time"
)

type inodeHold interface{ release() }

// socketIdentity is the socket this process bound: its stat, and a hold on its inode where the
// platform has one.
type socketIdentity struct {
	hold inodeHold
	stat os.FileInfo
}

// listen binds the unix socket at path. A socket file nothing answers on is a crashed daemon's, and
// is replaced; one something answers on is left for the caller to probe.
func listen(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err == nil || !errors.Is(err, syscall.EADDRINUSE) {
		return ln, err
	}
	if c, derr := net.DialTimeout("unix", path, 500*time.Millisecond); derr == nil {
		_ = c.Close()
		return nil, err // in use: the caller probes who
	}
	if rerr := os.Remove(path); rerr != nil {
		return nil, fmt.Errorf("stale socket %s: %w", path, rerr)
	}
	return net.Listen("unix", path)
}

// ownSocket makes the bound socket 0600 (the file is the access control: no login locally), keeps
// closing the listener from unlinking it by name, and takes its identity.
func ownSocket(ln net.Listener, path string) (socketIdentity, error) {
	if err := os.Chmod(path, 0o600); err != nil {
		return socketIdentity{}, fmt.Errorf("chmod %s: %w", path, err)
	}
	if ul, ok := ln.(*net.UnixListener); ok {
		ul.SetUnlinkOnClose(false)
	}
	st, err := os.Stat(path)
	if err != nil {
		return socketIdentity{}, fmt.Errorf("stat %s: %w", path, err)
	}
	h, herr := holdInode(path)
	if herr != nil {
		return socketIdentity{stat: st}, nil
	}
	return socketIdentity{hold: h, stat: st}, nil
}

// removeIfStillOurs unlinks path only while it is the file this process bound, so a daemon shutting
// down never deletes a successor's socket. The hold is released only after the comparison: that
// ordering is what keeps the inode number from being reused in between.
func removeIfStillOurs(path string, id socketIdentity) {
	if id.hold != nil {
		defer id.hold.release()
	}
	now, err := os.Stat(path)
	if err != nil || !os.SameFile(id.stat, now) {
		return
	}
	_ = os.Remove(path)
}
