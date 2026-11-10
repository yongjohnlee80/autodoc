package tui

// THE EDITOR'S WORKSPACE, as the host knows it: the workspace in use, with its root, and the active
// buffer's path, which the crew's agents are told of (agentswitch.go).

// wsRefOf is the workspace name names, with its root; the name alone for one the TUI doesn't list.
func (h *Host) wsRefOf(name string) wsRef {
	if name == "" {
		return wsRef{}
	}
	for _, w := range h.workspaces.All() {
		if w.name == name {
			return wsRef{Name: w.name, Root: w.root}
		}
	}
	return wsRef{Name: name}
}

// activeDocumentIn is the active buffer's path when it is a file of workspace ws, else "".
func (h *Host) activeDocumentIn(ws string) string {
	if b := h.active; b != nil && b.file.open && b.file.ws == ws {
		return b.file.path
	}
	return ""
}

// takeSwitch is the switch agent hasn't been told of, from the workspace in use (agents.takeSwitch).
// Loop only.
func (h *Host) takeSwitch(agent string) (wsSwitch, bool) {
	return h.agents.takeSwitch(agent, h.wsRefOf(h.ws), h.activeDocumentIn)
}

// editorWorkspace is editor.workspace (agents.workspaceAnswer). It takes no parameter: the
// workspace an agent started in is its token's.
func (h *Host) editorWorkspace(agent string, a editorArgs) (any, error) {
	if len(a) > 0 {
		return nil, invalid("editor.workspace takes no params: the workspace you started in is your token's")
	}
	return h.agents.workspaceAnswer(agent, h.wsRefOf(h.ws), h.activeDocumentIn), nil
}
