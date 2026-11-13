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

// NewSession is the session to the daemon on the unix socket addr (client.NewSession), its
// long-polls kept apart on a connection of their own (pollsApart).
func NewSession(addr string, spawn func() (string, error)) *Session {
	return client.NewSession(addr, spawn).Apart(pollsApart...)
}

// pollsApart are the window's long-polls: the inbox's watch and the crew host's fallback jobs,
// each one call at a time that waits up to 25 s and is asked again at once. On the primary
// connection they would hold two of the handler slots the daemon reads that connection's
// requests in, for as long as the window has a crew, leaving fewer for every open and save.
// Apart, they wait on a connection of their own. mail.fallback_done, the fallback's report, is an
// ordinary call and stays on the primary.
var pollsApart = []string{"inbox.watch", "mail.fallbacks"}

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
