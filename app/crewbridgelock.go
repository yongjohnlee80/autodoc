package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/filelock"
)

// lockBridge takes the session's bridge lock, or answers errBridgeRunning. It is the OS's own file
// lock (internal/filelock), which ends with its holder however it ends, so a crashed bridge never
// blocks the next. The lock file is in the agent's folder, so a link put there is refused, never
// followed.
func lockBridge(dir, kind, session string) (func(), error) {
	lockPath, _ := bridgeFiles(dir, kind, session)
	unlock, err := filelock.TryLock(lockPath, filelock.NoFollow())
	if errors.Is(err, filelock.ErrHeld) {
		return nil, errBridgeRunning
	}
	if err != nil {
		return nil, fmt.Errorf("the bridge's lock: %w", err)
	}
	return func() { _ = unlock() }, nil
}

// lockFolderPoll is how often lockFolder tries a lock another holds.
const lockFolderPoll = 20 * time.Millisecond

// lockFolder takes the file lock at path, waiting while another holds it, until ctx ends: one Claude
// hook at a time for a session (parallel tools' hooks come at once). It ends with its holder however
// it ends, and a link at path is refused.
func lockFolder(ctx context.Context, path string) (func(), error) {
	for {
		unlock, err := filelock.TryLock(path, filelock.NoFollow())
		if err == nil {
			return func() { _ = unlock() }, nil
		}
		if !errors.Is(err, filelock.ErrHeld) {
			return nil, fmt.Errorf("the prompts' lock: %w", err)
		}
		// NOTE: can't announce: a released lock tells no one waiting for it, so a hook that finds it
		// held tries again on a timer until its own bound.
		t := time.NewTimer(lockFolderPoll)
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, fmt.Errorf("waiting for %s: %w", path, ctx.Err())
		case <-t.C:
		}
	}
}
