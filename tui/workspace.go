package tui

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// THE WORKSPACES — the daemon's, the one in use, and the manager that lists them beside the
// settings of the one under its cursor, and adds, edits and deletes them (the store keeps them;
// the daemon serves each while it exists). The settings are one dialog: settings.go.

type wsInfo struct {
	name, root, state, embeddingPolicy string
	sectionTokens                      int64
	include, exclude                   []string
	schema                             schemaInfo
	textExtensions                     []string // the workspace's own plain-text extensions
	provider, providerErr              string   // its own embedding provider ("" the daemon's), and why it is not set up
	db                                 databasesInfo
}

func workspacePatterns(value any) []string {
	var patterns []string
	for _, item := range asList(value) {
		if pattern, ok := item.(string); ok {
			patterns = append(patterns, pattern)
		}
	}
	return patterns
}

func patternArgs(patterns []string) []any {
	args := make([]any, len(patterns))
	for index, pattern := range patterns {
		args[index] = pattern
	}
	return args
}

// loadWorkspaces lists the daemon's workspaces and uses the current one if it is still served,
// else the first that is.
func (h *Host) loadWorkspaces() {
	ep := h.epoch
	type answer struct {
		list []wsInfo
		err  error
	}
	do(h, func(ctx context.Context) answer {
		list, err := h.listWorkspaces(ctx)
		return answer{list: list, err: err}
	}, func(a answer) {
		if ep != h.epoch {
			return
		}
		if a.err != nil {
			h.failed("workspaces", a.err)
			return
		}
		h.wsList = a.list
		h.showWorkspacesInExplorer(a.list)
		pick := -1
		for i, w := range a.list {
			if w.state == "ready" && (pick < 0 || w.name == h.ws) {
				pick = i
			}
		}
		h.showWorkspaceRows()
		if pick < 0 {
			h.ws, h.filesAll = "", nil
			if h.keepDraft {
				h.keepDraft = false
			} else {
				h.closeFile()
			}
			h.setWhere("autodoc · no workspace")
			h.notify("no workspace: Go › Manage workspaces… adds one")
			return
		}
		if a.list[pick].name != h.ws || !h.entered {
			h.enter(a.list[pick].name)
		}
	})
}

// showWorkspaceRows shows h.wsList in the picker and the manager, and the settings of the
// manager's row.
func (h *Host) showWorkspaceRows() {
	var rows, managed []rowOf
	for _, w := range h.wsList {
		label := w.name
		if w.state != "ready" {
			label += "  (" + w.state + ")"
		}
		rows = append(rows, rowOf{"key": w.name, "label": label})
		managed = append(managed, rowOf{"key": w.name, "name": label, "root": w.root})
	}
	h.workspaces.Reset(rows)
	h.managed.Reset(managed)
	h.showManagerDetail()
}

func (h *Host) activeWorkspaceInfo() (wsInfo, bool) {
	for _, workspace := range h.wsList {
		if workspace.name == h.ws {
			return workspace, true
		}
	}
	return wsInfo{}, false
}

// pickWorkspace opens the picker.
func (h *Host) pickWorkspace() {
	current := 0
	for i, w := range h.wsList {
		if w.name == h.ws {
			current = i
			break
		}
	}
	h.set("App.workspaceIndex", current)
	h.open("workspacePicker")
}

// useWorkspace switches to the picker's row, asking first over unsaved changes.
func (h *Host) useWorkspace(i int) {
	if i < 0 || i >= len(h.wsList) {
		return
	}
	w := h.wsList[i]
	if w.name == h.ws {
		h.closeDialog("workspacePicker")
		return
	}
	if w.state != "ready" {
		h.notify(fmt.Sprintf("%s cannot be served: its root is gone (Go › Manage workspaces…)", w.name))
		return
	}
	h.closeDialog("workspacePicker")
	h.guard("switch to "+w.name, func() { h.enter(w.name) })
}

// enter makes name the workspace in use: the file closes, and its files are listed for the pickers.
func (h *Host) enter(name string) {
	h.epoch++
	h.ws, h.entered, h.filesAll = name, true, nil
	if ws, ok := h.activeWorkspaceInfo(); ok {
		h.set("App.terminalDir", termDir(ws.root)) // the terminal's next start; a running shell stays
	}
	h.focusSent = time.Time{}
	if h.remember != nil {
		h.remember(name)
	}
	h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), name))
	if h.keepDraft {
		h.keepDraft = false // the draft a removed workspace left stays on the page (events.go)
	} else {
		h.closeFile()
	}
	h.prog = progress{}
	if h.searchCancel != nil {
		h.searchCancel()
		h.searchCancel = nil
	}
	h.searchSeq++
	h.hitList = nil
	h.hits.Reset(nil)
	h.set("App.hitsTitle", "hits · searching "+name)
	h.showPreview("search", "", "", 0)
	h.refreshSearch()
	h.listFiles()
	h.poll()
}

// manageWorkspaces opens the manager over the store's workspaces: the list on the left, the
// settings of the one under the cursor on the right. Whether the edition offers the database
// settings is asked once a connection, for the right-hand pane.
func (h *Host) manageWorkspaces() {
	h.closeDialog("workspacePicker")
	h.set("App.managerHelp", managerHelp)
	h.managerIndex = 0
	h.set("App.managerIndex", -1)
	h.set("App.managerIndex", 0)
	h.open("workspaceManager")
	ep := h.epoch
	do(h, func(ctx context.Context) bool {
		caps, err := h.call(ctx, "sys.capabilities")
		on, _ := asMap(caps)["databases"].(bool)
		return err == nil && on
	}, func(on bool) {
		if ep == h.epoch {
			h.databases = on
			h.showManagerDetail()
		}
	})
	h.loadWorkspaces()
}

const managerHelp = "Add… a directory · Edit… or Advanced… the one under the cursor · Delete… keeps its files"

// managerRow is the manager's row i: the one under its cursor when a button was pressed, read
// from the table then, since a move of the cursor may not have reached the host yet.
// listWorkspaces is workspace.list, read into wsInfo; from a worker.
func (h *Host) listWorkspaces(ctx context.Context) ([]wsInfo, error) {
	res, err := h.call(ctx, "workspace.list")
	if err != nil {
		return nil, err
	}
	var out []wsInfo
	for _, w := range asList(res) {
		m := asMap(w)
		out = append(out, wsInfo{name: str(m, "name"), root: str(m, "root"), state: str(m, "state"), sectionTokens: num(m, "section_tokens"),
			embeddingPolicy: str(m, "embedding_policy"), include: workspacePatterns(m["include"]), exclude: workspacePatterns(m["exclude"]),
			schema: readSchemaInfo(m["schema"]), textExtensions: workspacePatterns(m["text_extensions"]),
			provider: str(m, "provider"), providerErr: str(m, "provider_error"), db: readDatabasesInfo(m["databases"])})
	}
	return out, nil
}

// withCurrent runs fn on the loop with the workspace named name as the daemon has it NOW, not as
// the last listing had it: a dialog opened right after its own save (whose relisting is still on
// its way) must show what was saved (Lector's review of #30). extra, when set, runs in the same
// worker, for a dialog that needs more from the daemon. A workspace gone meanwhile says so. Any
// dialog opened or closed after it (open, closeDialog, the manager's own dismissal) supersedes it:
// an answer arriving late never replaces what the user went to since.
func (h *Host) withCurrent(name string, extra func(ctx context.Context) (any, error), fn func(w wsInfo, more any)) {
	ep := h.epoch
	h.dialogSeq++
	seq := h.dialogSeq
	type answer struct {
		w     wsInfo
		found bool
		more  any
		err   error
	}
	do(h, func(ctx context.Context) answer {
		list, err := h.listWorkspaces(ctx)
		if err != nil {
			return answer{err: err}
		}
		var a answer
		for _, w := range list {
			if w.name == name {
				a.w, a.found = w, true
			}
		}
		if a.found && extra != nil {
			a.more, a.err = extra(ctx)
		}
		return a
	}, func(a answer) {
		switch {
		case ep != h.epoch, seq != h.dialogSeq:
			// another connection, or a later dialog asked for since: this answer is nobody's
		case a.err != nil:
			h.failed(name, a.err)
		case !a.found:
			h.set("App.managerHelp", "the workspace "+name+" is gone")
			h.loadWorkspaces()
		default:
			fn(a.w, a.more)
		}
	})
}

// withCurrentRow is withCurrent for the manager's row i.
func (h *Host) withCurrentRow(i int, fn func(w wsInfo)) {
	if w, ok := h.managerRow(i); ok {
		h.withCurrent(w.name, nil, func(w wsInfo, _ any) { fn(w) })
	}
}

func (h *Host) managerRow(i int) (wsInfo, bool) {
	if i < 0 || i >= len(h.wsList) {
		h.set("App.managerHelp", "no workspace under the cursor · Add… makes one")
		return wsInfo{}, false
	}
	return h.wsList[i], true
}

func splitPatternRules(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return []string{}, nil
	}
	parts := strings.Split(value, ";")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
		if parts[i] == "" {
			return nil, fmt.Errorf("remove the empty rule between semicolons")
		}
	}
	return parts, nil
}

// startRemoveWorkspace asks before deleting the manager's row i, naming what goes and what stays.
func (h *Host) startRemoveWorkspace(i int) {
	w, ok := h.managerRow(i)
	if !ok {
		return
	}
	h.removing = w.name
	h.set("App.removeQuestion", fmt.Sprintf("Delete the workspace %s? Its index goes: the files' search, links and "+
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
				h.closeFile()
			}
			h.notify("deleted workspace " + name + " (its files stay)")
			h.loadWorkspaces()
		})
	}
	if h.ws == name {
		h.guard("delete its workspace", remove)
		return
	}
	remove()
}
