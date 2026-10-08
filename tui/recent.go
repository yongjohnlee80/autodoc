package tui

import (
	"encoding/json"
	"fmt"
	"slices"
)

// RECENT DOCUMENTS — File › Recent files… (SPC r): the files opened last, newest first, across
// workspaces, as tui.recent keeps them. Enter opens one, entering its workspace when it is not the
// one in use. A file of a workspace that is gone, or no longer in the workspace in use, is not
// listed; Clear list forgets them all.

const (
	prefRecent = "tui.recent"
	// maxRecent is how many files the list keeps.
	maxRecent = 20
)

// recentDoc is a file opened: its workspace and its path in it.
type recentDoc struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
}

// recentOf reads the recent preference; anything but a list of files reads as none.
func recentOf(s string) []recentDoc {
	var all []recentDoc
	if json.Unmarshal([]byte(s), &all) != nil {
		return nil
	}
	return all
}

func recentJSON(docs []recentDoc) string {
	if len(docs) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(docs)
	return string(b)
}

// withRecent is docs with d first, once, at most maxRecent of them.
func withRecent(docs []recentDoc, d recentDoc) []recentDoc {
	out := []recentDoc{d}
	for _, o := range docs {
		if o != d && len(out) < maxRecent {
			out = append(out, o)
		}
	}
	return out
}

// noteRecent puts file p of workspace ws first among the recent files.
func (h *Host) noteRecent(ws, p string) {
	h.prefs.recent = withRecent(h.prefs.recent, recentDoc{Workspace: ws, Path: p})
	h.storePref(prefRecent, recentJSON(h.prefs.recent))
}

// openRecent lists the recent files and opens the dialog.
func (h *Host) openRecent() {
	h.listRecent()
	h.open("recent")
}

// listRecent shows the recent files still there: their workspace known, and, in the workspace in
// use, still among its files.
func (h *Host) listRecent() {
	h.recentRows = h.recentRows[:0]
	for _, d := range h.prefs.recent {
		if h.recentListed(d) {
			h.recentRows = append(h.recentRows, d)
		}
	}
	rows := make([]rowOf, len(h.recentRows))
	for i, d := range h.recentRows {
		label := d.Workspace
		if label == "" {
			label = "(" + outsideBadge + ")"
		}
		rows[i] = rowOf{"key": d.Workspace + "\x00" + d.Path, "workspace": label, "path": d.Path}
	}
	h.recentModel.Reset(rows)
	if len(rows) == 0 {
		h.set("App.recentStatus", "no recent files: the files you open are listed here")
		return
	}
	h.set("App.recentStatus", fmt.Sprintf("%d recent files, the newest first", len(rows)))
}

func (h *Host) recentListed(d recentDoc) bool {
	if d.Workspace == "" {
		return true // a file outside every workspace: opening it says when it is gone
	}
	if !slices.ContainsFunc(h.wsList, func(w wsInfo) bool { return w.name == d.Workspace }) {
		return false
	}
	return d.Workspace != h.ws || slices.Contains(h.filesAll, d.Path)
}

// recentSelect opens the recent file at row i.
func (h *Host) recentSelect(i int) {
	if i < 0 || i >= len(h.recentRows) {
		return
	}
	d := h.recentRows[i]
	h.closeDialog("recent")
	h.openIn(d.Workspace, d.Path)
}

// clearRecent forgets the recent files.
func (h *Host) clearRecent() {
	h.setPref(prefRecent, "[]", func(pr *prefs) { pr.recent = nil })
	h.listRecent()
	h.say("cleared the recent files")
}
