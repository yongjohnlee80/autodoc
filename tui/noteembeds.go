package tui

import (
	"os"
	"path/filepath"
	"time"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// A NOTE'S EMBEDS, both front ends'. A line holding only an image of a diagram (flow.mmd), a chart
// (sales.chart.json) or a table (data.csv) is drawn as the file would be written there: the
// Markdown noteEmbed reads it as, as the HTML preview reads it (export.EmbedMarkdown). The window's
// note editor draws it through its own renderer (embeds_gui.go); a terminal note's Rendered view
// through golib's MarkdownCellRenderer.Embeds, which termEmbeds answers (docs/decisions/editor.md).

// embedTableRows is how many rows the Rendered view shows of a table a note embeds; a last row says
// how many more there are. The whole table is the HTML preview's.
const embedTableRows = 16

// noteEmbed is the Markdown dest stands for in note: a mermaid or vega-lite fence, or a table, as
// export.EmbedMarkdown reads it. tooLarge: the file is over what an embed reads (1 MiB), refused
// unread.
func noteEmbed(note, dest string) (md string, tooLarge, ok bool) {
	return export.EmbedMarkdownSized(note, dest, embedTableRows)
}

// termEmbeds answers a terminal note's Rendered view for its embeds (MarkdownCellRenderer.Embeds).
// It is called on the UI loop alone, as the view is laid, once per embed line and pass, so it keeps
// each file's Markdown while the file's size and time stay: a pass costs a stat a file, not a read.
//
// While the note is shown, it follows the folder of each file it has read (follow, the host's
// followDir), so a file rewritten on disk draws anew: a change to one of them calls changed, which
// lays the view out again, and resolve then reads the file afresh by its size and time. close ends
// the follows.
type termEmbeds struct {
	note  string // the note's file on disk
	trf   func(id string, args ...any) string
	files map[string]termEmbed // by the line's destination
	// follow watches a folder, telling onChange each file in it that changes ("" anything); nil
	// follows nothing. changed lays the note's view out again.
	follow  func(dir string, onChange func(name string)) (release func())
	changed func()
	follows map[string]func() // each followed folder's release
	missing map[string]bool   // embedded files not on disk yet, followed for their writing
}

// awaited follows the folder of p, an embedded file not on disk, so its writing draws it.
func (t *termEmbeds) awaited(p string) {
	if t.follow == nil {
		return
	}
	if t.missing == nil {
		t.missing = map[string]bool{}
	}
	t.missing[p] = true
	t.followFor(p)
}

// followFor follows p's folder, once per folder.
func (t *termEmbeds) followFor(p string) {
	if t.follow == nil {
		return
	}
	dir := filepath.Dir(p)
	if _, ok := t.follows[dir]; ok {
		return
	}
	if t.follows == nil {
		t.follows = map[string]func(){}
	}
	t.follows[dir] = t.follow(dir, func(name string) {
		if name == "" || t.embeds(filepath.Join(dir, name)) {
			t.changed()
		}
	})
}

// embeds reports whether p is a file the note embeds, as last read.
func (t *termEmbeds) embeds(p string) bool {
	if t.missing[p] {
		return true
	}
	for _, f := range t.files {
		if f.path == p {
			return true
		}
	}
	return false
}

// close ends every follow: the note is no longer the one shown.
func (t *termEmbeds) close() {
	for dir, release := range t.follows {
		release()
		delete(t.follows, dir)
	}
}

// termEmbed is one file as termEmbeds last read it.
type termEmbed struct {
	path string
	size int64
	mod  time.Time
	md   string
	ok   bool
}

// resolve is MarkdownCellRenderer.Embeds: the Markdown dest's file stands for. A file too large to
// draw is a notice in its place; one that isn't there, or isn't a file a note embeds, is no embed
// (the line is an image's, its alt text).
func (t *termEmbeds) resolve(dest string) (string, bool) {
	if t.note == "" || !export.IsMarkdownEmbed(dest) {
		return "", false
	}
	p, found := export.EmbedPath(t.note, dest)
	if !found {
		delete(t.files, dest)
		// not on disk (EmbedPath finds only a file that is): followed where it would be written,
		// the note's own folder, so its writing draws it
		t.awaited(filepath.Join(filepath.Dir(t.note), filepath.FromSlash(dest)))
		return "", false
	}
	st, err := os.Stat(p)
	if err != nil { // gone since EmbedPath found it: the next pass finds it missing, and follows it
		delete(t.files, dest)
		return "", false
	}
	if c, ok := t.files[dest]; ok && c.path == p && c.size == st.Size() && c.mod.Equal(st.ModTime()) {
		return c.md, c.ok
	}
	md, tooLarge, ok := noteEmbed(t.note, dest)
	if tooLarge {
		md, ok = t.trf("autodoc.notice.embeds.is_too_large_to_draw_here", dest), true
	}
	if t.files == nil {
		t.files = map[string]termEmbed{}
	}
	t.files[dest] = termEmbed{path: p, size: st.Size(), mod: st.ModTime(), md: md, ok: ok}
	delete(t.missing, p)
	t.followFor(p)
	return md, ok
}
