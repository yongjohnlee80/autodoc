package tui

import "fmt"

// WORKSPACE SWITCHES, TOLD TO THE AGENTS
// (docs/decisions/crew.md#workspace-switches-are-told-not-imposed) — an agent AutoDoc started is
// bound to the workspace it started in: its env, its folder and its guide name it, and a switch
// never changes them. A switch is told when the agent asks, never typed into its terminal: each
// agent instance keeps the workspace it last saw, and editor.workspace answers one switch from that
// to the TUI's, however many came between (A→B→C is A→C, A→B→A none). A Claude agent asks with each
// prompt, through the UserPromptSubmit hook in its folder (agenthook.go). editor.document and
// editor.buffers say when a buffer is in a workspace other than the asking agent's.

// wsRef is a workspace as an agent sees it: its name and root. wsInfo's fields are the TUI's own.
type wsRef struct{ Name, Root string }

// value is r on the socket: {name, root}, msgpack's map.
func (r wsRef) value() map[string]any { return map[string]any{"name": r.Name, "root": r.Root} }

// wsSwitch is the change an agent hasn't been told of: from the workspace it last saw to the TUI's.
type wsSwitch struct {
	Kind     string // switchEntered, switchRenamed or switchLeft
	From, To wsRef  // the workspace last seen, and the one in use; To empty for none
	Document string // the active buffer's path, when it is in To
}

// value is s on the socket: {kind, from, to, document?, notice}.
func (s wsSwitch) value(spawn wsRef) map[string]any {
	m := map[string]any{"kind": s.Kind, "from": s.From.value(), "to": s.To.value(), "notice": s.notice(spawn)}
	if s.Document != "" {
		m["document"] = s.Document
	}
	return m
}

// notice is s as one line for the agent spawned in spawn: what `autodoc --workspace-notice` prints
// into its context.
func (s wsSwitch) notice(spawn wsRef) string {
	switch s.Kind {
	case switchRenamed:
		return fmt.Sprintf("[AutoDoc] the workspace %s was renamed %s (the same root, %s): $AUTODOC_WORKSPACE still says %s.",
			s.From.Name, s.To.Name, s.To.Root, spawn.Name)
	case switchLeft:
		return fmt.Sprintf("[AutoDoc] the editor has no workspace open now. You stay in %s ($AUTODOC_ROOT, %s).", spawn.Name, spawn.Root)
	}
	open := ""
	if s.Document != "" {
		open = "; open: " + s.Document
	}
	return fmt.Sprintf("[AutoDoc] the editor moved to workspace %s (%s), from %s (%s)%s. You stay in %s ($AUTODOC_WORKSPACE, "+
		"$AUTODOC_ROOT): resolve a path from editor.document in the workspace it names.", s.To.Name, s.To.Root, s.From.Name, s.From.Root, open, spawn.Name)
}

const (
	switchEntered = "entered" // another workspace, by root
	switchRenamed = "renamed" // the same root under another name: $AUTODOC_WORKSPACE names the old one
	switchLeft    = "left"    // no workspace in use: none is ready, or the one in use was deleted
)

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

// takeSwitch is the switch agent hasn't been told of, and marks the workspace in use as told: false
// for none, or when agent has no instance. A workspace the list doesn't name yet has no root to
// compare: its switch waits for the next ask. Loop only.
func (h *Host) takeSwitch(agent string) (wsSwitch, bool) {
	inst := h.agents.instances[agent]
	if inst == nil {
		return wsSwitch{}, false
	}
	if inst.seenToken != inst.token { // a new incarnation: it knows only the workspace it started in
		inst.seen, inst.seenToken = wsRef{Name: inst.workspace.name, Root: inst.workspace.root}, inst.token
	}
	cur := h.wsRefOf(h.ws)
	if cur == inst.seen || (cur.Name != "" && cur.Root == "") {
		return wsSwitch{}, false
	}
	sw := wsSwitch{Kind: switchEntered, From: inst.seen, To: cur}
	switch {
	case cur.Name == "":
		sw.Kind = switchLeft
	case cur.Root == inst.seen.Root:
		sw.Kind = switchRenamed // the same files under another name: never told as a switch
	}
	if b := h.active; b != nil && b.file.open && b.file.ws == cur.Name {
		sw.Document = b.file.path
	}
	inst.seen = cur
	return sw, true
}

// spawnWorkspace is the workspace the agent codename was started in, from its instance; false
// once its instance is gone (a token outlives it).
func (a *agents) spawnWorkspace(codename string) (wsRef, bool) {
	inst := a.instances[codename]
	if inst == nil {
		return wsRef{}, false
	}
	return wsRef{Name: inst.workspace.name, Root: inst.workspace.root}, true
}

// editorWorkspace is editor.workspace: the asking agent's workspace (spawn_workspace), the TUI's
// (workspace_in_use) and, when it has moved since the agent last asked, the one switch between
// (takeSwitch). It takes no parameter: spawn is the asking token's instance's.
func (h *Host) editorWorkspace(agent string, a editorArgs) (any, error) {
	if len(a) > 0 {
		return nil, invalid("editor.workspace takes no params: the workspace you started in is your token's")
	}
	out := map[string]any{"workspace_in_use": h.wsRefOf(h.ws).value()}
	spawn, ok := h.agents.spawnWorkspace(agent)
	if ok {
		out["spawn_workspace"] = spawn.value()
	}
	if sw, ok := h.takeSwitch(agent); ok {
		out["switch"] = sw.value(spawn)
	}
	return out, nil
}

// markForeign adds to an editor answer about a buffer in ws the asking agent's own workspace, and
// foreign_workspace when the buffer's is another: its path is then that workspace's root's, not
// $AUTODOC_ROOT's.
func (h *Host) markForeign(agent, ws string, doc map[string]any) {
	spawn, ok := h.agents.spawnWorkspace(agent)
	if !ok {
		return
	}
	doc["spawn_workspace"] = spawn.Name
	if ws != "" && ws != spawn.Name {
		doc["foreign_workspace"] = true
	}
}
