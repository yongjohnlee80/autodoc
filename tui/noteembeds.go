package tui

import (
	"os"
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
type termEmbeds struct {
	note  string // the note's file on disk
	trf   func(id string, args ...any) string
	files map[string]termEmbed // by the line's destination
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
		return "", false
	}
	st, err := os.Stat(p)
	if err != nil {
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
	return md, ok
}
