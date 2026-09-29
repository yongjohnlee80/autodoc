package tui

import (
	"context"
	"fmt"
)

// THE WORKSPACES — the daemon's, the one in use, and the manager that adds, renames and deletes
// them (the store keeps them; the daemon serves each while it exists).

type wsInfo struct{ name, root, state string }

// loadWorkspaces lists the daemon's workspaces and uses the current one if it is still served,
// else the first that is.
func (h *Host) loadWorkspaces() {
	ep := h.epoch
	type answer struct {
		list []wsInfo
		err  error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "workspace.list")
		if err != nil {
			return answer{err: err}
		}
		var out []wsInfo
		for _, w := range asList(res) {
			m := asMap(w)
			out = append(out, wsInfo{str(m, "name"), str(m, "root"), str(m, "state")})
		}
		return answer{list: out}
	}, func(a answer) {
		if ep != h.epoch {
			return
		}
		if a.err != nil {
			h.failed("workspaces", a.err)
			return
		}
		h.wsList = a.list
		var rows, managed []rowOf
		pick := -1
		for i, w := range a.list {
			label := w.name
			if w.state != "ready" {
				label += "  (" + w.state + ")"
			}
			rows = append(rows, rowOf{"key": w.name, "label": label})
			managed = append(managed, rowOf{"key": w.name, "name": w.name, "state": w.state, "root": w.root})
			if w.state == "ready" && (pick < 0 || w.name == h.ws) {
				pick = i
			}
		}
		h.workspaces.Reset(rows)
		h.managed.Reset(managed)
		h.mgrIndex = min(h.mgrIndex, len(a.list)-1)
		if pick < 0 {
			h.ws = ""
			h.closeNote()
			h.setWhere("autodoc · no workspace")
			h.setStatus("no workspace: Go › Manage workspaces… adds one")
			return
		}
		if a.list[pick].name != h.ws || !h.entered {
			h.enter(a.list[pick].name)
		}
	})
}

// pickWorkspace opens the picker.
func (h *Host) pickWorkspace() { h.open("workspacePicker") }

// useWorkspace switches to the picker's row, asking first over unsaved changes.
func (h *Host) useWorkspace(i int) {
	if i < 0 || i >= len(h.wsList) {
		return
	}
	w := h.wsList[i]
	if w.state != "ready" {
		h.setStatus(fmt.Sprintf("%s cannot be served: its root is gone (Go › Manage workspaces…)", w.name))
		return
	}
	h.closeDialog("workspacePicker")
	h.guard("switch to "+w.name, func() { h.enter(w.name) })
}

// enter makes name the workspace in use: the note closes, the notes pane lists its notes.
func (h *Host) enter(name string) {
	h.epoch++
	h.ws, h.entered = name, true
	if h.remember != nil {
		h.remember(name)
	}
	h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), name))
	h.closeNote()
	h.prog = progress{}
	h.listNotes()
	h.poll()
}

// manageWorkspaces opens the manager over the store's workspaces.
func (h *Host) manageWorkspaces() {
	h.closeDialog("workspacePicker")
	h.set("App.managerHelp", managerHelp)
	h.open("workspaceManager")
	h.loadWorkspaces()
}

const managerHelp = "Add… a directory · Rename… or Delete… the one under the cursor · deleting keeps its files"

// managerSelect follows the manager's cursor.
func (h *Host) managerSelect(i int) { h.mgrIndex = i }

// managerRow is the workspace under the manager's cursor.
func (h *Host) managerRow() (wsInfo, bool) {
	if h.mgrIndex < 0 || h.mgrIndex >= len(h.wsList) {
		h.set("App.managerHelp", "no workspace under the cursor · Add… makes one")
		return wsInfo{}, false
	}
	return h.wsList[h.mgrIndex], true
}

const addHelp = "a name, and a directory: its **/*.md are indexed, .git skipped"

// startAddWorkspace asks for a new workspace's name and root.
func (h *Host) startAddWorkspace() {
	h.set("App.workspaceAddError", addHelp)
	h.open("workspaceAdd")
}

// addWorkspace adds a workspace; the daemon serves it at once. A refusal asks again, saying why.
func (h *Host) addWorkspace(name, root string) {
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "workspace.add", name, root)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.workspaceAddError", "not added: "+wireMessage(err))
			h.open("workspaceAdd")
			return
		}
		h.setStatus("added workspace " + name)
		h.set("App.managerHelp", managerHelp)
		h.loadWorkspaces()
	})
}

// startRenameWorkspace asks for a new name for the manager's row.
func (h *Host) startRenameWorkspace() {
	w, ok := h.managerRow()
	if !ok {
		return
	}
	h.renaming = w.name
	h.set("App.renameFrom", w.name)
	h.set("App.workspaceRenameError", "the index is kept: only the name changes")
	h.open("workspaceRename")
}

// renameWorkspace renames the workspace the rename was started on.
func (h *Host) renameWorkspace(to string) {
	from := h.renaming
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "workspace.rename", from, to)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.workspaceRenameError", "not renamed: "+wireMessage(err))
			h.set("App.renameFrom", to)
			h.open("workspaceRename")
			return
		}
		if h.ws == from {
			// the same workspace under another name: the open note stays open
			h.ws = to
			h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), to))
			if h.remember != nil {
				h.remember(to)
			}
		}
		h.setStatus(fmt.Sprintf("renamed %s to %s", from, to))
		h.loadWorkspaces()
	})
}

// startRemoveWorkspace asks before deleting the manager's row, naming what goes and what stays.
func (h *Host) startRemoveWorkspace() {
	w, ok := h.managerRow()
	if !ok {
		return
	}
	h.removing = w.name
	h.set("App.removeQuestion", fmt.Sprintf("Delete the workspace %s? Its index goes: the notes' search, links and "+
		"history in autodoc. Its files in %s stay as they are, and adding the directory again indexes them anew.", w.name, w.root))
	h.open("workspaceRemove")
}

// removeWorkspaceConfirmed deletes the workspace; when it is the one in use, unsaved changes are
// asked about first, and another workspace, if any, is entered.
func (h *Host) removeWorkspaceConfirmed() {
	name := h.removing
	remove := func() {
		do(h, func(ctx context.Context) error {
			_, err := h.call(ctx, "workspace.remove", name)
			return err
		}, func(err error) {
			if err != nil {
				h.failed("delete "+name, err)
				return
			}
			if h.ws == name {
				h.ws, h.entered = "", false
				h.closeNote()
			}
			h.setStatus("deleted workspace " + name + " (its files stay)")
			h.loadWorkspaces()
		})
	}
	if h.ws == name {
		h.guard("delete its workspace", remove)
		return
	}
	remove()
}
