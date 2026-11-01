//go:build unix

package app

import (
	"slices"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestTheCrewMCPServerIsTheAgentsOwnAutodoc: the server crew setup offers a session's kinds is
// `<the agent's autodoc> crew mcp`, named MCPServerName, its tools crew.MCPToolNames, the server's
// own table's names too (one source); the command's words are read as a shell reads them, and one a
// shell would expand, or an unterminated quote, offers no server.
func TestTheCrewMCPServerIsTheAgentsOwnAutodoc(t *testing.T) {
	s, err := crewMCPServer(`/opt/auto\ doc "wrap mode" 'x y'`)
	if err != nil || s.Name != MCPServerName || !slices.Equal(s.Command, []string{"/opt/auto doc", "wrap mode", "x y", "crew", "mcp"}) ||
		len(s.Env) != 0 || !slices.Equal(s.Tools, crew.MCPToolNames()) || !slices.Equal(s.Tools, MCPTools()) {
		t.Fatalf("the crew's server: %+v %v", s, err)
	}
	var table []string
	for _, tool := range crewMCPTools {
		table = append(table, tool.Name)
	}
	if !slices.Equal(table, crew.MCPToolNames()) {
		t.Errorf("the server's tools %v, crew.MCPToolNames %v: want one list", table, crew.MCPToolNames())
	}
	for _, bad := range []string{"", "autodoc $(id)", "`id`", `"unterminated`, "a;b", "trailing\\"} {
		if _, err := crewMCPServer(bad); err == nil {
			t.Errorf("--autodoc %q: a server, want none", bad)
		}
	}
}
