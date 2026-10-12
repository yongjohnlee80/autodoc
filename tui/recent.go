package tui

import (
	"encoding/json"
	"fmt"
	"slices"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
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

// recent is File › Recent files…, a feature of its own: its list is its preference (tui.recent),
// noted on every file shown and listed again whenever the workspaces or their files are.
type recent struct {
	c    Core
	rows *rows[recentDoc]
}

func newRecent(c Core) *recent {
	return &recent{c: c, rows: newRows(tuidecl.NewListModel("key", "workspace", "path"), recentRowOf)}
}

func (*recent) Name() string { return "recent" }

func (r *recent) State() map[string]any {
	return map[string]any{"App.recentFiles": r.rows.Model(), "App.recentStatus": ""}
}

func (r *recent) Commands() map[string]Command {
	return map[string]Command{
		"App.openRecent":   none(r.open),
		"App.recentSelect": oneNumber("a row", r.choose),
		"App.clearRecent":  none(r.clear),
	}
}

// Prefs is the list, as the store keeps it: no chooser, the feature sets it (SetPref).
// Catalog is the recent files' user command (catalog.go): it runs what App.openRecent runs.
func (r *recent) Catalog() []Entry {
	return []Entry{builtin("file.open_recent", "recent files", "App.openRecent()", r.open)}
}

func (r *recent) Prefs() []PrefSpec { return []PrefSpec{ValuePref(prefRecent, "[]", nil)} }

// docs are the recent files the preference holds, newest first.
func (r *recent) docs() []recentDoc { return recentOf(r.c.Prefs().Value(prefRecent)) }

// Shown notes a file shown: first among the recent files. The draft is none.
func (r *recent) Shown(p Page) {
	if p.Ref.path == "" {
		return
	}
	r.c.SetPref(prefRecent, recentJSON(withRecent(r.docs(), recentDoc{Workspace: p.Ref.ws, Path: p.Ref.path})))
}

func (*recent) Edited(Page)    {}
func (*recent) Moved(int, int) {}
func (*recent) Reread(Page)    {}
func (*recent) Closed()        {}

// Listed lists the recent files again: a workspace's files, or the workspaces, listed anew decide
// which are still there.
func (r *recent) Listed() { r.list() }

// open lists the recent files and opens the dialog.
func (r *recent) open() {
	r.list()
	r.c.Open("recent")
}

// list shows the recent files still there: their workspace known, and, in the workspace in use,
// still among its files.
func (r *recent) list() {
	var listed []recentDoc
	for _, d := range r.docs() {
		if r.listed(d) {
			listed = append(listed, d)
		}
	}
	r.rows.Reset(listed)
	if len(listed) == 0 {
		r.c.Set("App.recentStatus", "no recent files: the files you open are listed here")
		return
	}
	r.c.Set("App.recentStatus", fmt.Sprintf("%d recent files, the newest first", len(listed)))
}

func (r *recent) listed(d recentDoc) bool {
	if d.Workspace == "" {
		return true // a file outside every workspace: opening it says when it is gone
	}
	if !slices.ContainsFunc(r.c.Workspaces(), func(w Workspace) bool { return w.Name == d.Workspace }) {
		return false
	}
	return d.Workspace != r.c.Workspace() || slices.Contains(r.c.Files().Paths, d.Path)
}

// choose opens the recent file at row i.
func (r *recent) choose(i int) {
	d, ok := r.rows.At(i)
	if !ok {
		return
	}
	r.c.Close("recent")
	r.c.OpenRef(d.Workspace, d.Path)
}

// clear forgets the recent files.
func (r *recent) clear() {
	r.c.SetPref(prefRecent, "[]")
	r.list()
	r.c.Say("cleared the recent files")
}

// recentRowOf is a recent file as the list shows it.
func recentRowOf(_ int, d recentDoc) rowOf {
	label := d.Workspace
	if label == "" {
		label = "(" + outsideBadge + ")"
	}
	return rowOf{"key": d.Workspace + "\x00" + d.Path, "workspace": label, "path": d.Path}
}
