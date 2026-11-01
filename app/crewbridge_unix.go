//go:build unix

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// detachBridge starts the bridge argv and env name (bridgeChild), detached: its own session (so the
// agent's tool shell ending does not end it), stdin from /dev/null, stdout and stderr to the
// session's bridge log in dir. It answers the child's pid and the log's path.
func detachBridge(dir, kind, session string, argv, env []string) (int, string, error) {
	_, logPath := bridgeFiles(dir, kind, session)
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("the bridge's log: %w", err)
	}
	defer log.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		return 0, "", err
	}
	defer null.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, "", err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release() // never waited on: it outlives this process
	return pid, logPath, nil
}

// lockBridge takes the session's bridge lock, or answers errBridgeRunning: an flock, which the
// kernel drops when its holder ends however it ends, so a crashed bridge never blocks the next.
func lockBridge(dir, kind, session string) (func(), error) {
	lockPath, _ := bridgeFiles(dir, kind, session)
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the bridge's lock: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errBridgeRunning
		}
		return nil, fmt.Errorf("locking %s: %w", lockPath, err)
	}
	return func() { f.Close() }, nil
}

// lockFolderPoll is how often lockFolder tries a lock another holds.
const lockFolderPoll = 20 * time.Millisecond

// lockFolder takes the flock at path, waiting while another holds it, until ctx ends: one Claude
// hook at a time for a session (parallel tools' hooks come at once). The kernel drops it when its
// holder ends however it ends.
func lockFolder(ctx context.Context, path string) (func(), error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return nil, fmt.Errorf("the prompts' lock: %w", err)
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() { f.Close() }, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			f.Close()
			return nil, fmt.Errorf("locking %s: %w", path, err)
		}
		// NOTE: can't announce: an flock's release tells no one waiting for it, so a hook that finds
		// it held tries again on a timer until its own bound.
		t := time.NewTimer(lockFolderPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			f.Close()
			return nil, fmt.Errorf("waiting for %s: %w", path, ctx.Err())
		case <-t.C:
		}
	}
}
