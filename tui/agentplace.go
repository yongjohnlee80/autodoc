package tui

// AGENT PLACE — where the agent panel opens: an edge or the centre, chosen in Preferences as the
// terminal's place is (terminal.go), at that place's size and length. A size or length is kept
// per place, chosen in Preferences or dragged with the panel's grip (panelResized):
// tui.agent.edge, and tui.agent.<place>.size and .length.

const prefAgentEdge = "tui.agent.edge"

// agentEdges are where the agent opens, in the order the Preferences dialog offers them; the top
// first, as it opens by default.
var agentEdges = []string{"top", "bottom", "left", "right", "center"}

var agentEdgeLabels = []string{"top", "bottom", "left", "right", "centre"}

const defaultAgentEdge = "top"

// agentDefault is a place's size and length before the user sets them: half the Window across the
// edge and its full length along it, so an agent's CLI has room; 80 by 80 centred.
func agentDefault(edge string) (size, length int) {
	if edge == "center" {
		return 80, 80
	}
	return 50, 100
}

// readAgentPrefs reads where the agent opens, keeping the default for a place it cannot use. Its
// sizes are read with the side panels' (readPanelPrefs).
func readAgentPrefs(p *prefs, m map[string]any) {
	if s, _ := m[prefAgentEdge].(string); indexOfOK(agentEdges, s) {
		p.agentEdge = s
	}
}

// agentState is what the document reads of where the agent opens.
func agentState(p prefs) map[string]any {
	size, length := p.panelGeometry("agent", p.agentEdge)
	return map[string]any{
		"App.agentEdge":   p.agentEdge,
		"App.agentSize":   size,
		"App.agentLength": length,
	}
}

// syncAgentDialog sets the Preferences dialog's agent choosers.
func (h *Host) syncAgentDialog() {
	size, length := h.prefs.panelGeometry("agent", h.prefs.agentEdge)
	h.set("App.agentEdgeIndex", indexOf(agentEdges, h.prefs.agentEdge))
	h.set("App.agentSizeIndex", nearest(termSizes, size))
	h.set("App.agentLengthIndex", nearest(termLengths, length))
}

func (h *Host) setAgentEdge(i int) {
	if i >= 0 && i < len(agentEdges) {
		e := agentEdges[i]
		h.setPref(prefAgentEdge, e, func(p *prefs) { p.agentEdge = e })
		h.dockPanel("agent")
		h.syncAgentDialog() // the size and length shown are the new place's
	}
}

// setAgentSize and setAgentLength set the size at the place the agent opens from now.
func (h *Host) setAgentSize(i int) {
	if i >= 0 && i < len(termSizes) {
		h.keepPanelGeo("agent", "size", termSizes[i])
	}
}

func (h *Host) setAgentLength(i int) {
	if i >= 0 && i < len(termLengths) {
		h.keepPanelGeo("agent", "length", termLengths[i])
	}
}
