package tui

import (
	"cmp"
	"time"
)

// WORKSPACE SWITCHES, TOLD TO THE AGENTS (ADR 1792988759) — an agent AutoDoc started is bound to the
// workspace it started in: its env, its folder and its guide name it, and a switch never changes
// them. A switch of the TUI's workspace is told instead: the TUI keeps a short numbered record of
// each, which an agent reads on the editor socket (editor.workspace), and editor.document and
// editor.buffers say when a buffer is in a workspace other than the asking agent's.

// wsRef is a workspace as an agent sees it: its name and root. wsInfo's fields are the TUI's own.
type wsRef struct{ Name, Root string }

// value is r on the socket: {name, root}, msgpack's map.
func (r wsRef) value() map[string]any { return map[string]any{"name": r.Name, "root": r.Root} }

// wsSwitch is one change of the TUI's workspace, as an agent reads it.
type wsSwitch struct {
	Seq      int64  // 1, 2, … for this TUI's run
	Kind     string // switchEntered, switchRenamed or switchLeft
	From, To wsRef  // the workspace before and after; empty for none
	Document string // the active buffer's path, when it is in To
	At       string // RFC 3339
}

// value is s on the socket: {seq, kind, from, to, document?, at}.
func (s wsSwitch) value() map[string]any {
	m := map[string]any{"seq": s.Seq, "kind": s.Kind, "from": s.From.value(), "to": s.To.value(), "at": s.At}
	if s.Document != "" {
		m["document"] = s.Document
	}
	return m
}

const (
	switchEntered = "entered" // another workspace entered
	switchRenamed = "renamed" // the workspace in use renamed: the same root, a new name
	switchLeft    = "left"    // no workspace in use now: none is ready, or the one in use was deleted
	keptSwitches  = 32        // the record is a ring: an older cursor reads from its oldest, lost
)

// wsRefOf is the workspace name names, with its root; the name alone for one the TUI no longer lists.
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

// noteSwitch records a change of the TUI's workspace from from to to, of kind. Loop only.
func (h *Host) noteSwitch(kind string, from, to wsRef) {
	if from == to {
		return
	}
	h.switchSeq++
	r := wsSwitch{Seq: h.switchSeq, Kind: kind, From: from, To: to, At: time.Now().UTC().Format(time.RFC3339)}
	if b := h.active; b != nil && b.file.open && b.file.ws == to.Name {
		r.Document = b.file.path
	}
	h.switches = append(h.switches, r)
	if len(h.switches) > keptSwitches {
		h.switches = h.switches[len(h.switches)-keptSwitches:]
	}
}

// noteRename records the workspace in use renamed from from to to: one root, whichever name the
// TUI's list holds now.
func (h *Host) noteRename(from, to string) {
	root := cmp.Or(h.wsRefOf(from).Root, h.wsRefOf(to).Root)
	h.noteSwitch(switchRenamed, wsRef{Name: from, Root: root}, wsRef{Name: to, Root: root})
}

// switchesAfter are the records after seq, and lost when some after it are no longer kept.
func (h *Host) switchesAfter(seq int64) (out []wsSwitch, lost bool) {
	out = []wsSwitch{}
	for _, r := range h.switches {
		if r.Seq > seq {
			out = append(out, r)
		}
	}
	lost = len(h.switches) > 0 && h.switches[0].Seq > seq+1
	return out, lost
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

// editorWorkspace is editor.workspace [after]: the asking agent's workspace, the TUI's, and the
// switches after the cursor after (0, or none, for every one kept). spawn is the asking token's
// instance's, never a parameter's.
func (h *Host) editorWorkspace(agent string, a editorArgs) (any, error) {
	after, _, err := a.num(0, "after", true)
	if err != nil {
		return nil, err
	}
	switches, lost := h.switchesAfter(int64(after))
	rows := make([]any, len(switches))
	for i, r := range switches {
		rows[i] = r.value()
	}
	out := map[string]any{"current": h.wsRefOf(h.ws).value(), "switches": rows, "cursor": h.switchSeq}
	if spawn, ok := h.agents.spawnWorkspace(agent); ok {
		out["spawn"] = spawn.value()
	}
	if lost {
		out["lost"] = true
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
