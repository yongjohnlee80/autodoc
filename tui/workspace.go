package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// THE WORKSPACES — the daemon's, the one in use, and the manager that lists them beside the
// settings of the one under its cursor, and adds, edits and deletes them (the store keeps them;
// the daemon serves each while it exists). The settings are one dialog: settings.go.

type wsInfo struct {
	id                                 int64 // the store's own id, which a rename keeps (0 from a daemon before it)
	name, root, state, embeddingPolicy string
	sectionTokens                      int64
	include, exclude                   []string
	schema                             schemaInfo
	textExtensions                     []string // the workspace's own plain-text extensions
	provider, providerErr              string   // its own embedding provider ("" the daemon's), and why it is not set up
	db                                 databasesInfo
	// graphOnly are its graph_only patterns, todoSearch whether its todo tasks are in search, and
	// todoExcluded the exclude patterns that keep a todo store of it out of the index
	// (docs/decisions/todos.md#graph-only-indexing)
	graphOnly, todoExcluded []string
	todoSearch              bool
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

// loadWorkspaces lists the daemon's workspaces and uses the current one if it is still served,
// else the first that is.
func (h *Host) loadWorkspaces() {
	tk := h.ticket(nil, TicketWorkspace)
	do(h, func(ctx context.Context) answerOf[[]wsInfo] {
		list, err := h.listWorkspaces(ctx)
		return answered(list, err)
	}, func(a answerOf[[]wsInfo]) {
		if !tk.Current() {
			return
		}
		if a.err != nil {
			h.failed(h.tr("autodoc.notice.workspace.workspaces"), a.err)
			return
		}
		pick := -1
		for i, w := range a.v {
			if w.state == "ready" && (pick < 0 || w.name == h.ws) {
				pick = i
			}
		}
		h.showWorkspaceRows(a.v)
		h.noteTodoExcluded(a.v)
		if h.ws != "" && (pick < 0 || a.v[pick].name != h.ws) {
			h.keepDraftOnLeave() // the workspace in use is not served now: its unsaved text stays, as a draft
		}
		if pick < 0 {
			h.leaveWorkspace()
			h.ws = ""
			h.resetFiles()
			h.setWhere(h.tr("autodoc.text.workspace.autodoc_no_workspace"))
			h.notify(h.tr("autodoc.notice.workspace.no_workspace_go_manage_workspaces_adds_one"))
			return
		}
		if a.v[pick].name != h.ws || !h.entered {
			h.enter(a.v[pick].name)
		}
	})
}

// showWorkspaceRows shows list in the picker and the manager, and the settings of the manager's
// row.
func (h *Host) showWorkspaceRows(list []wsInfo) {
	h.workspaces.Reset(list)
	h.managed.Reset(list)
	h.showManagerDetail()
	h.reg.listed()
}

// workspaceLabel is a workspace's name, with its state when it is not ready.
func workspaceLabel(w wsInfo) string {
	if w.state != "ready" {
		return w.name + "  (" + w.state + ")"
	}
	return w.name
}

// workspaceRowOf is a workspace as the picker shows it; managedRowOf, as the manager does.
func workspaceRowOf(_ int, w wsInfo) rowOf { return rowOf{"key": w.name, "label": workspaceLabel(w)} }
func managedRowOf(_ int, w wsInfo) rowOf {
	return rowOf{"key": w.name, "name": workspaceLabel(w), "root": w.root}
}

func (h *Host) activeWorkspaceInfo() (wsInfo, bool) {
	for _, workspace := range h.workspaces.All() {
		if workspace.name == h.ws {
			return workspace, true
		}
	}
	return wsInfo{}, false
}

// pickWorkspace opens the picker.
func (h *Host) pickWorkspace() {
	current := 0
	for i, w := range h.workspaces.All() {
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
	w, ok := h.workspaces.At(i)
	if !ok {
		return
	}
	if w.name == h.ws {
		h.closeDialog("workspacePicker")
		return
	}
	if w.state != "ready" {
		h.notify(h.trf("autodoc.notice.workspace.cannot_be_served_its_root_is_gone_go_manage_work", w.name))
		return
	}
	h.closeDialog("workspacePicker")
	h.enter(w.name) // nothing is asked: every buffer stays open, in its own workspace (buffers.go)
}

// enter makes name the workspace in use, and lists its files for the pickers. Every buffer stays
// open, in its own workspace: search, links and the outline follow the workspace in use, and a
// buffer's file is still read and written in its own (buffers.go). Entering the one in use again, as
// a new connection does, keeps every buffer as it is, unsaved text, cursor and all: only the
// connection changed, so each open buffer is checked against the disk (recheckFile).
func (h *Host) enter(name string) {
	again := name == h.ws
	h.epoch++
	if !again {
		h.leaveWorkspace()
	}
	h.ws, h.entered = name, true
	h.resetFiles()
	h.focusSent = time.Time{}
	if h.remember != nil {
		h.remember(name)
	}
	h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), name))
	switch {
	case again:
		h.recheckFile() // a new connection: every open buffer is checked against the disk
	case !h.file.open:
		// the draft shown on entering another workspace (the first, at start) is set up as the page,
		// its text kept: the watchers (outline, HTML pane, plugin feed) hear its edits from here
		h.readPage()
		h.showPage()
	}
	h.prog = progress{}
	if h.searchCancel != nil {
		h.searchCancel()
		h.searchCancel = nil
	}
	h.searchSeq++
	h.searchPick.show(nil)
	h.set("App.hitsTitle", h.trf("autodoc.text.workspace.hits_searching", name))
	h.refreshSearch()
	h.listFiles()
	h.poll()
	h.reg.entered(name)
}

// manageWorkspaces opens the manager over the store's workspaces: the list on the left, the
// settings of the one under the cursor on the right. Whether the edition offers the database
// settings is asked once a connection, for the right-hand pane.
func (h *Host) manageWorkspaces() {
	h.closeDialog("workspacePicker")
	h.set("App.managerHelp", h.tr("autodoc.text.workspace.add_a_directory_edit_or_advanced_the_one_under_t"))
	h.managerIndex = 0
	h.set("App.managerIndex", -1)
	h.set("App.managerIndex", 0)
	h.open("workspaceManager")
	tk := h.ticket(nil, TicketWorkspace)
	do(h, func(ctx context.Context) answerOf[bool] {
		caps, err := h.call(ctx, "sys.capabilities")
		on, _ := asMap(caps)["databases"].(bool)
		return answered(err == nil && on, nil)
	}, func(a answerOf[bool]) {
		if a.err != nil {
			h.failed(h.tr("autodoc.notice.workspace.the_backend_s_capabilities"), a.err) // it panicked: no databases, said
		}
		if tk.Current() {
			h.databases = a.v
			h.showManagerDetail()
		}
	})
	h.loadWorkspaces()
}

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
		out = append(out, wsInfo{id: num(m, "id"), name: str(m, "name"), root: str(m, "root"), state: str(m, "state"), sectionTokens: num(m, "section_tokens"),
			embeddingPolicy: str(m, "embedding_policy"), include: workspacePatterns(m["include"]), exclude: workspacePatterns(m["exclude"]),
			schema: readSchemaInfo(m["schema"]), textExtensions: workspacePatterns(m["text_extensions"]),
			provider: str(m, "provider"), providerErr: str(m, "provider_error"), db: readDatabasesInfo(m["databases"]),
			graphOnly: workspacePatterns(m["graph_only"]), todoExcluded: workspacePatterns(m["todo_excluded"])})
		w := &out[len(out)-1]
		w.todoSearch, _ = m["todo_search"].(bool)
		if _, ok := m["graph_only"]; !ok {
			w.graphOnly = workspace.DefaultGraphOnly() // a daemon before protocol 23 indexes no store graph-only
		}
	}
	return out, nil
}

// withCurrent runs fn on the loop with the workspace named name as the daemon has it NOW, not as
// the last listing had it: a dialog opened right after its own save (whose relisting is still on
// its way) must show what was saved. extra, when set, runs in the same
// worker, for a dialog that needs more from the daemon. A workspace gone meanwhile says so. Any
// dialog opened or closed after it (open, closeDialog, the manager's own dismissal) supersedes it:
// an answer arriving late never replaces what the user went to since.
func (h *Host) withCurrent(name string, extra func(ctx context.Context) (any, error), fn func(w wsInfo, more any)) {
	tk := h.ticket(&h.dialogSeq, TicketWorkspace)
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
		case !tk.Current():
			// another connection, or a later dialog asked for since: this answer is nobody's
		case a.err != nil:
			h.failed(name, a.err)
		case !a.found:
			h.set("App.managerHelp", h.trf("autodoc.text.workspace.the_workspace_is_gone", name))
			h.loadWorkspaces()
		default:
			fn(a.w, a.more)
		}
	})
}

func (h *Host) managerRow(i int) (wsInfo, bool) {
	w, ok := h.managed.At(i)
	if !ok {
		h.set("App.managerHelp", h.tr("autodoc.text.workspace.no_workspace_under_the_cursor_add_makes_one"))
	}
	return w, ok
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
	h.ask(Question{Title: h.tr("autodoc.question.workspace.delete_the_workspace"), Text: h.trf("autodoc.question.workspace.delete_the_workspace_its_index_goes_the_files_se", w.name, w.root),
		No: h.tr("autodoc.question.workspace.no_keep_it"), Yes: h.tr("autodoc.question.workspace.yes_delete_it"), OnYes: func() { h.removeWorkspaceConfirmed(w.name) }})
}

// removeWorkspaceConfirmed deletes the workspace; when it is the one in use, unsaved changes are
// asked about first, and another workspace, if any, is entered.
func (h *Host) removeWorkspaceConfirmed(name string) {
	remove := func() {
		do(h, func(ctx context.Context) error {
			_, err := h.call(ctx, "workspace.remove", name)
			return err
		}, func(err error) {
			if err != nil {
				h.failed(h.trf("autodoc.notice.workspace.delete", name), err)
				return
			}
			if h.ws == name {
				h.keepDraftOnLeave() // its buffers: each asked about first, so only Stay-free ones are left
				h.ws, h.entered = "", false
			}
			h.notify(h.trf("autodoc.notice.workspace.deleted_workspace_its_files_stay", name))
			h.loadWorkspaces()
		})
	}
	if h.ws == name {
		var its []*openBuffer
		for _, b := range h.buffers {
			if b.file.open && b.file.ws == name {
				its = append(its, b)
			}
		}
		h.guardEach(its, h.askBefore("delete_workspace"), remove)
		return
	}
	remove()
}

// noteTodoExcluded says once a session, for each workspace whose excludes keep a todo store out of
// the index, which patterns do (docs/decisions/todos.md#excluded-todo-stores-are-reported): its
// tasks open with no relations, and its settings offer to index them graph-only instead.
func (h *Host) noteTodoExcluded(list []wsInfo) {
	for _, w := range list {
		key := w.name + "\x00" + strings.Join(w.todoExcluded, ";")
		if len(w.todoExcluded) == 0 || h.todoNoted[key] {
			continue
		}
		if h.todoNoted == nil {
			h.todoNoted = map[string]bool{}
		}
		h.todoNoted[key] = true
		h.notify(h.trf("autodoc.notice.workspace.exclude_keeps_its_todo_tasks_out_of_the_index_it", w.name, strings.Join(w.todoExcluded, "; ")))
	}
}

// leaveWorkspace says the workspace in use is left: what a gated script queued there, not sent yet,
// is refused when it would run (authority.go), never sent to the one entered next.
func (h *Host) leaveWorkspace() {
	close(h.wsLeft)
	h.wsLeft = make(chan struct{})
}
