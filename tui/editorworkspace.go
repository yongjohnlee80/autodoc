package tui

import "github.com/yongjohnlee80/autodoc/tui/agents"

// THE EDITOR'S WORKSPACE, as the host knows it: the workspace in use, with its root, and the active
// buffer's path, which the crew's agents are told of (agentswitch.go).

// wsRefOf is the workspace name names, with its root; the name alone for one the TUI doesn't list.
func (h *Host) wsRefOf(name string) agents.WsRef {
	if name == "" {
		return agents.WsRef{}
	}
	for _, w := range h.workspaces.All() {
		if w.name == name {
			return agents.WsRef{Name: w.name, Root: w.root}
		}
	}
	return agents.WsRef{Name: name}
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
func (h *Host) takeSwitch(agent string) (agents.WsSwitch, bool) {
	return h.agents.TakeSwitch(agent, h.wsRefOf(h.ws), h.activeDocumentIn)
}

// editorWorkspace is editor.workspace (agents.workspaceAnswer). It takes no parameter: the
// workspace an agent started in is its token's.
func (h *Host) editorWorkspace(agent string, a editorArgs) (any, error) {
	if len(a) > 0 {
		return nil, invalid("editor.workspace takes no params: the workspace you started in is your token's")
	}
	return h.agents.WorkspaceAnswer(agent, h.wsRefOf(h.ws), h.activeDocumentIn), nil
}
