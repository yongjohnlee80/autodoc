package tui

import (
	"path"
	"strings"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// A CSV OR TSV FILE'S TABLE (ADR 1792303119 §4.7): its Rendered view is an editable table, in the
// GUI's editor and in the terminal's. Each such buffer has its own TableDocument, made when the
// buffer is first shown and kept with it, over the editor's core: the editor shows one buffer at a
// time, and a table is the document view only while its own buffer is shown (documentFor), so it
// reads that buffer's text and its edits are that buffer's, undone as typing is. Ctrl+T shows the
// text; Raw is always the text.

// tableComma is the separator of a table file's records: ',' for .csv, '\t' for .tsv; 0 for any
// other file.
func tableComma(p string) rune {
	switch strings.ToLower(path.Ext(p)) {
	case ".csv":
		return ','
	case ".tsv":
		return '\t'
	}
	return 0
}

// tableOf is b's table, made on first use: the GUI's drawn in pixels (newTableDocument). An edit
// dropped by a change under it says so.
func (h *Host) tableOf(b *openBuffer, comma rune) *widget.TableDocument {
	if b.table == nil {
		b.table = newTableDocument(h.editor, h.core, comma,
			widget.TableRowNumbers(true), // each row's number in a gutter
			widget.TableOnEditDropped(func() { h.notify(h.tr("autodoc.notice.tabledoc.the_table_changed_the_edit_was_dropped")) }),
			widget.TableOnPasteClipped(func() { h.notify(h.tr("autodoc.notice.tabledoc.the_paste_was_cut_off_at_the_table_s_edge")) }))
	}
	return b.table
}
