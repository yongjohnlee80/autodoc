//go:build !unix

package agents

import (
	"errors"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// agentServer is a crew agent's server where there are no process groups to run one in: none is
// started, so a setup that needs one refuses the agent's start (agentserver.go).
type agentServer struct{}

func StartAgentServer(argv []string, socket, dir string, environ []string, ready time.Duration) (*agentServer, error) {
	return nil, errors.New("a crew agent's server needs process groups, which this platform lacks")
}

func (s *agentServer) stop() {}

func bindServer(proc terminal.TerminalProcess, _ *agentServer) terminal.TerminalProcess { return proc }
