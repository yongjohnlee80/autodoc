//go:build !unix

package tui

import (
	"errors"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// agentServer is a crew agent's server where there are no process groups to run one in: none is
// started, so a setup that needs one refuses the agent's start (agentserver.go).
type agentServer struct{}

func startAgentServer(argv []string, socket, dir string, environ []string) (*agentServer, error) {
	return nil, errors.New("a crew agent's server needs process groups, which this platform lacks")
}

func (s *agentServer) stop() {}

func bindServer(proc widget.TerminalProcess, _ *agentServer) widget.TerminalProcess { return proc }
