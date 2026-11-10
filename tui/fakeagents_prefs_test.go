package tui

// Copies of the agents' preference keys and panel edges the host's cells write and read
// (agent.go, agentplace.go): the host's own code names none of them.
const (
	prefAgentDefaultCopy = "tui.agent.default"
	prefAgentEdgeCopy    = "tui.agent.edge"
	defaultAgentEdgeCopy = "bottom"
)

var agentEdgesCopy = []string{"bottom", "top", "left", "right", "center"}
