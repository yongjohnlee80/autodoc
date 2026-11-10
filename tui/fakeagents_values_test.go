package tui

// Copies of the agents' literals the host's cells use (agent.go, agentplace.go, agentserver.go):
// preference keys, the panel's edges and the agent server's pid file's name. The host's own code
// names none of them.
const (
	prefAgentDefaultCopy = "tui.agent.default"
	prefAgentEdgeCopy    = "tui.agent.edge"
	defaultAgentEdgeCopy = "bottom"
	serverPidFileCopy    = ".autodoc-server.pid"
)

var agentEdgesCopy = []string{"bottom", "top", "left", "right", "center"}
