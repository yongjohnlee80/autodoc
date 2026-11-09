package tui

import (
	"context"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/client"
)

// The TUI's session to the daemon is internal/client's; these names keep the host's code as
// it was, and tui's exported API as it was before the move.
type (
	Session            = client.Session
	ConnectError       = client.ConnectError
	MismatchError      = client.MismatchError
	StoreMismatchError = client.StoreMismatchError
	OlderServer        = client.OlderServer
)

// NewSession is the session to the daemon on the unix socket addr (client.NewSession).
func NewSession(addr string, spawn func() (string, error)) *Session {
	return client.NewSession(addr, spawn)
}

// SpawnServe starts this executable's --serve detached, logging to serve.log in stateDir
// (client.SpawnServe).
func SpawnServe(configPath, stateDir string) (string, error) {
	return client.SpawnServe(configPath, stateDir)
}

// WaitGone waits until process pid has exited, or d has passed (client.WaitGone).
func WaitGone(ctx context.Context, pid int64, d time.Duration) bool {
	return client.WaitGone(ctx, pid, d)
}

// WriteHandoff announces that process pid is restarting the daemon on sock (client.WriteHandoff).
func WriteHandoff(stateDir, sock string, pid int64) error {
	return client.WriteHandoff(stateDir, sock, pid)
}
