package tui

import (
	"context"
	"fmt"
)

// THE WORKSPACES — the daemon's, and the one in use.

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
		var rows []rowOf
		pick := -1
		for i, w := range a.list {
			label := w.name
			if w.state != "ready" {
				label += "  (" + w.state + ")"
			}
			rows = append(rows, rowOf{"key": w.name, "label": label})
			if w.state == "ready" && (pick < 0 || w.name == h.ws) {
				pick = i
			}
		}
		h.workspaces.Reset(rows)
		if pick < 0 {
			h.setWhere("autodoc · no workspace")
			h.setStatus("no workspace: add a [[workspace]] to the config (Help › About names it)")
			return
		}
		h.enter(a.list[pick].name)
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
		h.setStatus(fmt.Sprintf("%s is %s: another autodoc serves it", w.name, w.state))
		return
	}
	h.closeDialog("workspacePicker")
	h.guard("switch to "+w.name, func() { h.enter(w.name) })
}

// enter makes name the workspace in use: the note closes, the notes pane lists its notes.
func (h *Host) enter(name string) {
	h.epoch++
	h.ws = name
	h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), name))
	h.closeNote()
	h.prog = progress{}
	h.listNotes()
	h.poll()
}
