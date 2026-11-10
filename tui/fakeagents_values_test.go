package tui

import (
	"encoding/json"
	"strings"
)

// Copies of the agents' preference keys, panel edges and two small functions the host's cells use
// (agent.go, agentplace.go): the host's own code names none of them.
const (
	prefAgentDefaultCopy = "tui.agent.default"
	prefAgentEdgeCopy    = "tui.agent.edge"
	defaultAgentEdgeCopy = "bottom"
)

var agentEdgesCopy = []string{"bottom", "top", "left", "right", "center"}

// shellQuoteCopy is s as one POSIX shell word, as the agents quote one (shellQuote).
func shellQuoteCopy(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// agentProfilesJSONCopy is ps as the preference stores them, as the agents write it (agentProfilesJSON).
func agentProfilesJSONCopy(ps []agentProfile) string {
	if len(ps) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(ps)
	return string(b)
}
