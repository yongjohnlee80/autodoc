package tui

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	wsfilter "github.com/yongjohnlee80/autodoc/core/workspace"
)

// THE WORKSPACES — the daemon's, the one in use, and the manager that adds, renames and deletes
// them (the store keeps them; the daemon serves each while it exists).

type wsInfo struct {
	name, root, state, embeddingPolicy string
	sectionTokens                      int64
	include, exclude                   []string
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
		res, err := h.call(ctx, "workspace.list")
		if err != nil {
			return answer{err: err}
		}
		var out []wsInfo
		for _, w := range asList(res) {
			m := asMap(w)
			out = append(out, wsInfo{name: str(m, "name"), root: str(m, "root"), state: str(m, "state"), sectionTokens: num(m, "section_tokens"), embeddingPolicy: str(m, "embedding_policy"), include: workspacePatterns(m["include"]), exclude: workspacePatterns(m["exclude"])})
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
		h.showWorkspacesInExplorer(a.list)
		var rows, managed []rowOf
		pick := -1
		for i, w := range a.list {
			label := w.name
			if w.state != "ready" {
				label += "  (" + w.state + ")"
			}
			rows = append(rows, rowOf{"key": w.name, "label": label})
			managed = append(managed, rowOf{"key": w.name, "name": w.name, "state": w.state, "root": w.root, "section": fmt.Sprint(w.sectionTokens), "policy": w.embeddingPolicy})
			if w.state == "ready" && (pick < 0 || w.name == h.ws) {
				pick = i
			}
		}
		h.workspaces.Reset(rows)
		h.managed.Reset(managed)
		h.syncFileTypes()
		if pick < 0 {
			h.ws, h.notesAll = "", nil
			h.closeNote()
			h.setWhere("autodoc · no workspace")
			h.notify("no workspace: Go › Manage workspaces… adds one")
			return
		}
		if a.list[pick].name != h.ws || !h.entered {
			h.enter(a.list[pick].name)
		}
	})
}

func (h *Host) activeWorkspaceInfo() (wsInfo, bool) {
	for _, workspace := range h.wsList {
		if workspace.name == h.ws {
			return workspace, true
		}
	}
	return wsInfo{}, false
}

func (h *Host) syncFileTypes() {
	workspace, ok := h.activeWorkspaceInfo()
	if !ok {
		return
	}
	matcher := wsfilter.NewMatcher(workspace.include, workspace.exclude)
	h.set("App.fileTypesTitle", "file types · "+workspace.name)
	h.set("App.fileTypesRoot", "workspace root: "+workspace.root)
	h.set("App.markdownTypeIndex", boolIndex(matcher.Match("sample.md")))
	h.set("App.textTypeIndex", boolIndex(matcher.Match("sample.txt")))
	h.set("App.yamlTypeIndex", boolIndex(matcher.Match("sample.yaml") && matcher.Match("sample.yml")))
	patterns := "include: " + strings.Join(workspace.include, ", ") + " · exclude: " + strings.Join(workspace.exclude, ", ")
	h.set("App.fileTypesPatterns", patterns)
}

func (h *Host) openFileTypes() {
	if _, ok := h.activeWorkspaceInfo(); !ok {
		h.notify("choose a workspace before changing file types")
		return
	}
	h.syncFileTypes()
	h.set("App.fileTypesHelp", "Text types are editable; Pro document types are unavailable in Community")
	h.open("fileTypes")
}

func (h *Host) setFileType(index int, extension string) {
	if index < 0 || index > 1 || (extension != "md" && extension != "txt" && extension != "yaml") || h.fileTypesPending {
		return
	}
	workspace, ok := h.activeWorkspaceInfo()
	if !ok {
		return
	}
	include := slices.Clone(workspace.include)
	exclude := slices.Clone(workspace.exclude)
	extensions := []string{extension}
	if extension == "yaml" {
		extensions = []string{"yaml", "yml"}
	}
	for _, ext := range extensions {
		pattern := "**/*." + ext
		if index == 0 {
			exclude = slices.DeleteFunc(exclude, func(item string) bool { return item == pattern })
			if !slices.Contains(include, pattern) {
				include = append(include, pattern)
			}
		} else {
			include = slices.DeleteFunc(include, func(item string) bool { return item == pattern })
			if !slices.Contains(exclude, pattern) {
				exclude = append(exclude, pattern)
			}
		}
	}
	h.fileTypesPending = true
	h.set("App.fileTypesHelp", "updating the workspace…")
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "workspace.set_patterns", workspace.name, patternArgs(include), patternArgs(exclude))
		return err
	}, func(err error) {
		h.fileTypesPending = false
		if err != nil {
			h.set("App.fileTypesHelp", "not changed: "+wireMessage(err))
			h.syncFileTypes()
			return
		}
		h.set("App.fileTypesHelp", "updated; the workspace is reconciling")
		h.loadWorkspaces()
	})
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

// enter makes name the workspace in use: the note closes, and its notes are listed for the pickers.
func (h *Host) enter(name string) {
	h.epoch++
	h.ws, h.entered, h.notesAll = name, true, nil
	h.focusSent = time.Time{}
	if h.remember != nil {
		h.remember(name)
	}
	h.setWhere(fmt.Sprintf("autodoc %s · %s", h.session.Version(), name))
	h.closeNote()
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

// managerRow is the manager's row i: the one under its cursor when a button was pressed, read
// from the table then, since a move of the cursor may not have reached the host yet.
func (h *Host) managerRow(i int) (wsInfo, bool) {
	if i < 0 || i >= len(h.wsList) {
		h.set("App.managerHelp", "no workspace under the cursor · Add… makes one")
		return wsInfo{}, false
	}
	return h.wsList[i], true
}

// startAddWorkspace asks for a new workspace: its title (untitled, to begin with), and its folder,
// chosen from the home directory. The default text formats are indexed; .git is skipped.
func (h *Host) startAddWorkspace() {
	h.setField("App.wsTitle", "untitled")
	h.open("workspaceAdd")
}

// addWorkspace adds a workspace; the daemon serves it at once. A refusal asks again, saying why.
func (h *Host) addWorkspace(name, root string) {
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "workspace.add", name, root)
		return err
	}, func(err error) {
		if err != nil {
			// Select closed the picker: it opens again as it was, the reason on the status line
			h.notify("not added: " + wireMessage(err))
			h.open("workspaceAdd")
			return
		}
		h.notify("added workspace " + name)
		h.set("App.managerHelp", managerHelp)
		h.loadWorkspaces()
	})
}

// startRenameWorkspace asks for a new name for the manager's row i.
func (h *Host) startRenameWorkspace(i int) {
	w, ok := h.managerRow(i)
	if !ok {
		return
	}
	h.renaming = w.name
	h.set("App.renameFrom", w.name)
	h.set("App.workspaceRenameError", "the index is kept: only the name changes")
	h.open("workspaceRename")
}

// startSectionSize edits the workspace's shared chunk limit, not a provider setting.
func (h *Host) startSectionSize(i int) {
	w, ok := h.managerRow(i)
	if !ok {
		return
	}
	h.sectionWorkspace = w.name
	h.set("App.sectionTitle", "section size · "+w.name)
	h.setField("App.sectionSize", strconv.FormatInt(w.sectionTokens, 10))
	h.set("App.sectionError", "128–2048 estimated tokens; changing this re-chunks the workspace")
	h.open("workspaceSection")
}

func (h *Host) startPatterns(i int) {
	workspace, ok := h.managerRow(i)
	if !ok {
		return
	}
	h.showPatterns(workspace)
}

func (h *Host) editActivePatterns() {
	workspace, ok := h.activeWorkspaceInfo()
	if !ok {
		return
	}
	h.showPatterns(workspace)
}

func (h *Host) showPatterns(workspace wsInfo) {
	h.patternWorkspace = workspace.name
	h.set("App.patternTitle", "rules · "+workspace.name)
	h.set("App.patternRoot", "one root: "+workspace.root)
	h.setField("App.patternInclude", strings.Join(workspace.include, "; "))
	h.setField("App.patternExclude", strings.Join(workspace.exclude, "; "))
	h.set("App.patternHelp", "Patterns are relative to this root; ** spans folders. Saving reconciles the index.")
	h.open("workspacePatterns")
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

func (h *Host) savePatterns(includeText, excludeText string) {
	include, err := splitPatternRules(includeText)
	if err == nil {
		var exclude []string
		exclude, err = splitPatternRules(excludeText)
		if err == nil {
			name := h.patternWorkspace
			do(h, func(ctx context.Context) error {
				_, err := h.call(ctx, "workspace.set_patterns", name, patternArgs(include), patternArgs(exclude))
				return err
			}, func(err error) {
				if err != nil {
					h.set("App.patternHelp", "not saved: "+wireMessage(err))
					h.open("workspacePatterns")
					return
				}
				h.notify("rules saved for " + name + "; reconciling")
				h.loadWorkspaces()
			})
			return
		}
	}
	h.set("App.patternHelp", "not saved: "+err.Error())
	h.open("workspacePatterns")
}

func (h *Host) startEmbeddingPolicy(i int) {
	w, ok := h.managerRow(i)
	if !ok {
		return
	}
	h.policyWorkspace = w.name
	h.set("App.policyTitle", "embedding · "+w.name)
	h.setField("App.embeddingPolicy", w.embeddingPolicy)
	h.set("App.policyError", "always · when opened · never (words only)")
	h.open("workspacePolicy")
}

func (h *Host) saveEmbeddingPolicy(policy string) {
	name := h.policyWorkspace
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "workspace.embedding_policy", name, strings.TrimSpace(policy))
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.policyError", "not saved: "+wireMessage(err))
			h.open("workspacePolicy")
			return
		}
		h.notify(fmt.Sprintf("%s: embedding %s", name, strings.TrimSpace(policy)))
		h.loadWorkspaces()
	})
}

func (h *Host) saveSectionSize(text string) {
	n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || n < 128 || n > 2048 {
		h.set("App.sectionError", "section size must be 128–2048 estimated tokens")
		h.open("workspaceSection")
		return
	}
	name := h.sectionWorkspace
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "workspace.section_size", name, n)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.sectionError", "not saved: "+wireMessage(err))
			h.open("workspaceSection")
			return
		}
		h.notify(fmt.Sprintf("%s: section size %d; re-chunking", name, n))
		h.loadWorkspaces()
	})
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
		h.notify(fmt.Sprintf("renamed %s to %s", from, to))
		h.loadWorkspaces()
	})
}

// startRemoveWorkspace asks before deleting the manager's row i, naming what goes and what stays.
func (h *Host) startRemoveWorkspace(i int) {
	w, ok := h.managerRow(i)
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
