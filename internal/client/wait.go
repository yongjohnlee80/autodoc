package client

import (
	"context"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/proctree"
)

// RestartWait bounds waiting for the old daemon to exit before the new one is started. Every build
// shares it, so a handoff another build wrote is never longer than this one's (handoffSpan).
const RestartWait = 15 * time.Second

// WaitGone waits until process pid has exited, or d has passed (false): the TUI's restart and
// `autodoc --print-endpoint --restart` wait on the stopped daemon so.
func WaitGone(ctx context.Context, pid int64, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if !alive(pid) {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
			// NOTE: polled: the process is not this one's child, so it cannot be waited on, and there is
			// no portable notice of another process's exit. 50 ms keeps a restart prompt.
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// alive is whether process pid exists (internal/proctree): nothing is sent to it.
func alive(pid int64) bool { return proctree.Alive(int(pid)) }
