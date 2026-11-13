package explorer

import (
	"encoding/json"
	"slices"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// RECENT DOCUMENTS — File › Recent files… (SPC r): the files opened last, newest first, across
// workspaces, as tui.recent keeps them. Enter opens one, entering its workspace when it is not the
// one in use. A file of a workspace that is gone, or no longer in the workspace in use, is not
// listed; Clear list forgets them all.

const (
	PrefRecent = "tui.recent"
	// maxRecent is how many files the list keeps.
	maxRecent = 20
)

// recentDoc is a file opened: its workspace and its path in it.
type RecentDoc struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
}

// recentOf reads the recent preference; anything but a list of files reads as none.
func recentOf(s string) []RecentDoc {
	var all []RecentDoc
	if json.Unmarshal([]byte(s), &all) != nil {
		return nil
	}
	return all
}

func RecentJSON(docs []RecentDoc) string {
	if len(docs) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(docs)
	return string(b)
}

// withRecent is docs with d first, once, at most maxRecent of them.
func withRecent(docs []RecentDoc, d RecentDoc) []RecentDoc {
	out := []RecentDoc{d}
	for _, o := range docs {
		if o != d && len(out) < maxRecent {
			out = append(out, o)
		}
	}
	return out
}

// recent is File › Recent files…, a feature of its own: its list is its preference (tui.recent),
// noted on every file shown and listed again whenever the workspaces or their files are.
type Recent struct {
	c    feature.Core
	Rows *feature.Rows[RecentDoc]
}

func NewRecent(c feature.Core) *Recent {
	return &Recent{c: c, Rows: feature.NewRows(tuidecl.NewListModel("key", "workspace", "path"),
		func(_ int, d RecentDoc) feature.RowOf { return recentRowOf(c, d) })}
}

func (*Recent) Name() string { return "recent" }

func (r *Recent) State() map[string]any {
	return map[string]any{"App.recentFiles": r.Rows.Model(), "App.recentStatus": ""}
}

func (r *Recent) Commands() map[string]feature.Command {
	return map[string]feature.Command{
		"App.openRecent":   feature.None(r.Open),
		"App.recentSelect": feature.OneNumber("a row", r.Choose),
		"App.clearRecent":  feature.None(r.Clear),
	}
}

// Prefs is the list, as the store keeps it: no chooser, the feature sets it (SetPref).
// Catalog is the recent files' user command (feature/catalog.go): it runs what App.openRecent runs.
func (r *Recent) Catalog() []feature.Entry {
	return []feature.Entry{feature.Builtin("file.open_recent", "recent files", "App.openRecent()", r.Open)}
}

func (r *Recent) Prefs() []feature.PrefSpec {
	return []feature.PrefSpec{feature.ValuePref(PrefRecent, "[]", nil)}
}

// docs are the recent files the preference holds, newest first.
func (r *Recent) Docs() []RecentDoc { return recentOf(r.c.Prefs().Value(PrefRecent)) }

// Shown notes a file shown: first among the recent files. The draft is none.
func (r *Recent) Shown(p feature.Page) {
	if p.Ref.Path == "" {
		return
	}
	r.c.SetPref(PrefRecent, RecentJSON(withRecent(r.Docs(), RecentDoc{Workspace: p.Ref.WS, Path: p.Ref.Path})))
}

func (*Recent) Edited(feature.Page) {}
func (*Recent) Moved(int, int)      {}
func (*Recent) Reread(feature.Page) {}
func (*Recent) Closed()             {}

// Listed lists the recent files again: a workspace's files, or the workspaces, listed anew decide
// which are still there.
func (r *Recent) Listed() { r.list() }

// open lists the recent files and opens the dialog.
func (r *Recent) Open() {
	r.list()
	r.c.Open("recent")
}

// LanguageChanged lists the recent files again, in the UI's language.
func (r *Recent) LanguageChanged() { r.list() }

// list shows the recent files still there: their workspace known, and, in the workspace in use,
// still among its files.
func (r *Recent) list() {
	var listed []RecentDoc
	for _, d := range r.Docs() {
		if r.listed(d) {
			listed = append(listed, d)
		}
	}
	r.Rows.Reset(listed)
	if len(listed) == 0 {
		r.c.Set("App.recentStatus", r.c.Tr("autodoc.text.recent.no_recent_files_the_files_you_open_are_listed_he"))
		return
	}
	r.c.Set("App.recentStatus", r.c.Trf("autodoc.text.recent.recent_files_the_newest_first", len(listed)))
}

func (r *Recent) listed(d RecentDoc) bool {
	if d.Workspace == "" {
		return true // a file outside every workspace: opening it says when it is gone
	}
	if !slices.ContainsFunc(r.c.Workspaces(), func(w feature.Workspace) bool { return w.Name == d.Workspace }) {
		return false
	}
	return d.Workspace != r.c.Workspace() || slices.Contains(r.c.Files().Paths, d.Path)
}

// choose opens the recent file at row i.
func (r *Recent) Choose(i int) {
	d, ok := r.Rows.At(i)
	if !ok {
		return
	}
	r.c.Close("recent")
	r.c.OpenRef(d.Workspace, d.Path)
}

// clear forgets the recent files.
func (r *Recent) Clear() {
	r.c.SetPref(PrefRecent, "[]")
	r.list()
	r.c.Say(r.c.Tr("autodoc.notice.recent.cleared_the_recent_files"))
}

// recentRowOf is a recent file as the list shows it, in c's language.
func recentRowOf(c feature.Core, d RecentDoc) feature.RowOf {
	label := d.Workspace
	if label == "" {
		label = c.Tr("autodoc.text.recent.no_workspace")
	}
	return feature.RowOf{"key": d.Workspace + "\x00" + d.Path, "workspace": label, "path": d.Path}
}
