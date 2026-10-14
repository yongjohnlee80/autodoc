//go:build gui

package app

import (
	"regexp"
	"strings"
	"sync/atomic"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// A NOTE'S EMBEDS IN THE RENDERED VIEW, under --gui. A line that embeds a file beside the note,
// ![title](flow.mmd) or ![title](data.csv) alone in its paragraph, is drawn as if the file were
// written there: a Mermaid diagram as a mermaid fence, drawn natively as golib draws one, and a
// table as a Markdown table, laid out natively by golib's own renderer. The file is read as the
// HTML preview reads it (core/export's EmbedMarkdown). The line stays the note's: with the cursor
// on it, it is its source, editable, and a click on what it draws puts the cursor there.

// embedTableRows is how many rows the Rendered view shows of a table a note embeds; a last row says
// how many more there are. The whole table is the HTML preview's.
const embedTableRows = 16

// noteEmbeds is the open note's file, told by the host as each document opens (Options.NoteFile),
// and read by every Markdown renderer the window builds.
type noteEmbeds struct{ note atomic.Pointer[string] }

// setNote is the host's NoteFile: the open note's file on disk, "" for none.
func (n *noteEmbeds) setNote(path string) { n.note.Store(&path) }

func (n *noteEmbeds) file() string {
	if p := n.note.Load(); p != nil {
		return *p
	}
	return ""
}

// renderer is golib's Markdown renderer for a MarkdownRenderer declaration, drawing the note's
// embeds: the window's style makes every editor's Rendered view with it (WithRendererFor).
func (n *noteEmbeds) renderer(chain guiwidget.Diagrammer) func(tuidecl.RendererSpec) guiwidget.Renderer {
	return func(s tuidecl.RendererSpec) guiwidget.Renderer {
		var opts []guiwidget.MarkdownOption
		if m, ok := s.(tuidecl.MarkdownSpec); ok {
			opts = append(opts, guiwidget.WithCodeLanguages(m.SourceLanguages))
			if m.HeadingScale > 0 {
				opts = append(opts, guiwidget.HeadingScale(m.HeadingScale))
			}
			if m.Mermaid {
				opts = append(opts, guiwidget.WithDiagrams(chain))
			}
		}
		return &embedRenderer{MarkdownRenderer: guiwidget.NewMarkdownRenderer(opts...), notes: n, diagrams: chain}
	}
}

// embedRenderer is golib's MarkdownRenderer with an embed line, off the cursor, drawn as its file.
type embedRenderer struct {
	*guiwidget.MarkdownRenderer
	notes    *noteEmbeds
	diagrams guiwidget.Diagrammer // an embedded diagram's, whatever the declaration's mermaid flag
}

// embedLine is a line holding only an embed: ![title](path), up to three spaces before it.
var embedLine = regexp.MustCompile(`^ {0,3}!\[[^\]]*\]\(\s*(<[^>]*>|[^\s)]+)(?:\s+"[^"]*")?\s*\)\s*$`)

// LayOut lays block b out: an embed line off the cursor as its file, anything else as golib does.
func (r *embedRenderer) LayOut(b guiwidget.Block, lines []string, width float32, inside bool, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	if !inside {
		if md, ok := r.embedded(lines, b); ok {
			return r.layEmbed(b, md, width, t, th, host)
		}
	}
	return r.MarkdownRenderer.LayOut(b, lines, width, inside, t, th, host)
}

// embedded is the Markdown drawn for block b when it is a line that embeds a file alone in its
// paragraph, of the open note, and the file is one the Rendered view draws.
func (r *embedRenderer) embedded(lines []string, b guiwidget.Block) (string, bool) {
	ln := b.From
	if b.To-b.From != 1 || ln >= len(lines) {
		return "", false
	}
	m := embedLine.FindStringSubmatch(lines[ln])
	if m == nil || ln > 0 && strings.TrimSpace(lines[ln-1]) != "" || ln+1 < len(lines) && strings.TrimSpace(lines[ln+1]) != "" {
		return "", false
	}
	note := r.notes.file()
	if note == "" {
		return "", false
	}
	return export.EmbedMarkdown(note, strings.Trim(m[1], "<>"), embedTableRows)
}

// layEmbed lays the embed of block b out as md, the Markdown its file stands for, drawn as the
// block's picture: the line's caret, number and clicks stay the line's own. A diagram is asked of
// the window's host for this block, as a mermaid fence's is; while it is not ready, or when it is
// declined, md is laid out as golib lays it, the diagram's source as code.
func (r *embedRenderer) layEmbed(b guiwidget.Block, md string, width float32, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	injected := strings.Split(md, "\n")
	if fence := strings.TrimLeft(injected[0], "`"); fence == "mermaid" && host != nil && r.diagrams != nil && len(injected) > 2 {
		src := strings.Join(injected[1:len(injected)-1], "\n")
		a := host.Diagram(r.diagrams, b, guiwidget.DiagramRequest{Lang: "mermaid", Src: src, Width: width, Scale: t.Scale(), Theme: th})
		if a.State != guiwidget.Declined && a.Pic != nil {
			return guiwidget.BlockLayout{Picture: a.Pic, PictureSize: a.Size, Height: a.Size.H}
		}
	}
	// no host: the lines are not the buffer's, so no diagram is asked for them
	bl := r.MarkdownRenderer.LayOut(guiwidget.Block{From: 0, To: len(injected)}, injected, width, false, t, th, nil)
	return guiwidget.BlockLayout{Picture: laidBlock{bl: bl, width: width}, PictureSize: gui.Size{W: width, H: bl.Height}, Height: bl.Height}
}

// laidBlock paints a block golib laid out, as the editor paints its blocks: its ground, then each
// line's marks and text, then its picture under them.
type laidBlock struct {
	bl    guiwidget.BlockLayout
	width float32
}

func (v laidBlock) Paint(c gui.Canvas) {
	if v.bl.Background.A > 0 {
		c.FillRect(gui.Rect{W: v.width, H: v.bl.Height}, gui.Solid(v.bl.Background))
	}
	var below float32
	for _, ll := range v.bl.Lines {
		at := gui.Pt(0, ll.Y)
		for _, m := range ll.Marks {
			c.FillRect(gui.Rect{X: at.X + m.Rect.X, Y: at.Y + m.Rect.Y, W: m.Rect.W, H: m.Rect.H}, gui.Solid(m.Color))
		}
		ll.Para.Paint(c, at, ll.Spans)
		below = ll.Y + ll.Para.Height
	}
	if v.bl.Picture != nil {
		v.bl.Picture.Paint(c.Sub(gui.Rect{Y: below, W: v.bl.PictureSize.W, H: v.bl.PictureSize.H}))
	}
}
