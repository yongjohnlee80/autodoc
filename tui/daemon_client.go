package tui

import (
	"errors"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/internal/client"
)

// The TUI's session to the daemon is internal/client's; these names keep the host's and the
// features' code as it was.
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

// wireMessage is what a failed call says to the user: the daemon's constant message, or the error.
func wireMessage(err error) string {
	var e *golibrpc.Error
	if errors.As(err, &e) {
		return e.Message
	}
	return err.Error()
}

// code is a failed call's error code, 0 when it is not the daemon's.
func code(err error) int64 {
	var e *golibrpc.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}
