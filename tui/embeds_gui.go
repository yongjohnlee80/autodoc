//go:build gui

package tui

import (
	"strings"
	"sync/atomic"

	"github.com/yongjohnlee80/golib/gui"
	guidecl "github.com/yongjohnlee80/golib/gui/decl"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// A NOTE'S EMBEDS IN THE RENDERED VIEW, under --gui. In the note editor (NoteRenderer), a line that
// holds only an image, ![title](flow.mmd), ![title](sales.chart.json) or ![title](data.csv), alone
// in its paragraph as the page's parser reads it (export.EmbedLines), is drawn as if the file were
// written there: a Mermaid diagram as a mermaid fence and a Vega-Lite chart as a vega-lite fence,
// each drawn natively as golib draws one (a chart golib does not draw is a short notice, not its
// spec: chartNotice), and
// a table as a Markdown table of literal cells, laid out natively by golib's own renderer. The file
// is read as the HTML preview reads it (export.EmbedMarkdown). The line stays the note's: while
// typing on it, it is its source, editable, and a click on what it draws puts the cursor on it.
//
// A ```csv fence is drawn as that table too, off the cursor, once its closing fence is written.
//
// Whether an image is alone depends on the lines around it, and the editor keeps the layout of a
// block wholly before an edit while the blocks before it are unchanged. So the paragraph's shape is
// in its blocks: an image alone is a block of its line, and a line holding only an image in a
// paragraph of more lines makes the paragraph one block. An edit that changes which it is changes
// the blocks there, and the editor lays the line out again.

// embedTableRows is how many rows the Rendered view shows of a table a note embeds; a last row says
// how many more there are. The whole table is the HTML preview's.
const embedTableRows = 16

// NoteEmbeds is the open note's file, which the host tells it as each document opens
// (Options.NoteFile), and the note editor's renderer reads.
type NoteEmbeds struct{ note atomic.Pointer[string] }

// SetNote is the host's NoteFile: the open note's file on disk, "" for none.
func (n *NoteEmbeds) SetNote(path string) { n.note.Store(&path) }

func (n *NoteEmbeds) file() string {
	if p := n.note.Load(); p != nil {
		return *p
	}
	return ""
}

// GUIStyle is the window's native style for AutoDoc's layout: golib's, opts added, with the note
// editor's renderer (NoteRenderer) drawing the open note's embeds, which notes is told. main.qml's
// note editor declares NoteRenderer, which golib's style alone has no renderer for: a window
// running AutoDoc's layout takes its style from here, or its note editor has no Rendered view.
func GUIStyle(notes *NoteEmbeds, opts ...guidecl.NativeOption) tuidecl.Style {
	note := guidecl.WithRendererFor(NoteRendererKind, noteRenderer(notes))
	return guidecl.Native(append([]guidecl.NativeOption{note}, opts...)...)
}

// noteRenderer is the note editor's renderer as GUIStyle installs it: notes' embeds, drawn through
// golib's native chain with AutoDoc's notice for a chart it declines.
func noteRenderer(notes *NoteEmbeds) func(tuidecl.RendererSpec) guiwidget.Renderer {
	return notes.Renderer(nativeDiagrams())
}

// Renderer makes the note editor's renderer for its NoteRenderer declaration (NoteRendererKind):
// golib's Markdown renderer with its settings, drawing the open note's embeds, its diagrams and the
// embeds' through chain.
func (n *NoteEmbeds) Renderer(chain guiwidget.Diagrammer) func(tuidecl.RendererSpec) guiwidget.Renderer {
	return func(s tuidecl.RendererSpec) guiwidget.Renderer {
		var m tuidecl.MarkdownSpec
		switch spec := s.(type) {
		case NoteRendererSpec:
			m = spec.MarkdownSpec
		case tuidecl.MarkdownSpec:
			m = spec
		}
		opts := []guiwidget.MarkdownOption{guiwidget.WithCodeLanguages(m.SourceLanguages)}
		if m.HeadingScale > 0 {
			opts = append(opts, guiwidget.HeadingScale(m.HeadingScale))
		}
		if m.Mermaid {
			opts = append(opts, guiwidget.WithDiagrams(chain))
		}
		return &embedRenderer{MarkdownRenderer: guiwidget.NewMarkdownRenderer(opts...), notes: n, diagrams: chain}
	}
}

// embedRenderer is golib's MarkdownRenderer with an embed line, off the cursor, drawn as its file.
type embedRenderer struct {
	*guiwidget.MarkdownRenderer
	notes    *NoteEmbeds
	diagrams guiwidget.Diagrammer // an embedded diagram's, whatever the declaration's mermaid flag
	// alone are the lines Blocks last found holding an image alone, with their text then: the
	// editor lays blocks out from the lines it split them from
	alone  map[int]aloneEmbed
	joined map[guiwidget.Block]bool // the paragraphs Blocks last made one block
}

type aloneEmbed struct{ text, dest string }

// Blocks are golib's, with a line holding an image alone a block of its own, and a paragraph of more
// lines holding an image on a line of its own one block: the paragraphs are found once, and golib's
// blocks are joined in one pass, so a note of many such paragraphs costs no more than its length.
func (r *embedRenderer) Blocks(lines []string, from, to int) []guiwidget.Block {
	blocks := r.MarkdownRenderer.Blocks(lines, 0, len(lines))
	r.alone, r.joined = nil, nil
	var joins []guiwidget.Block // in order: EmbedLines walks the source in order
	for _, e := range export.EmbedLines([]byte(strings.Join(lines, "\n"))) {
		switch {
		case e.Alone:
			if r.alone == nil {
				r.alone = map[int]aloneEmbed{}
			}
			r.alone[e.Line] = aloneEmbed{text: lines[e.Line], dest: e.Dest}
		case len(joins) == 0 || joins[len(joins)-1].From != e.From: // a paragraph's first such line
			joins = append(joins, guiwidget.Block{From: e.From, To: e.To})
		}
	}
	blocks, r.joined = joinRuns(blocks, joins)
	var out []guiwidget.Block
	for _, b := range blocks {
		if b.To > from && b.From < to {
			out = append(out, guiwidget.Block{From: max(b.From, from), To: min(b.To, to)})
		}
	}
	return out
}

// joinRuns makes each of joins (in order) one block where golib has its lines as blocks of one
// line each, in one pass over blocks; joined are those it made.
func joinRuns(blocks, joins []guiwidget.Block) (out []guiwidget.Block, joined map[guiwidget.Block]bool) {
	if len(joins) == 0 {
		return blocks, nil
	}
	out = make([]guiwidget.Block, 0, len(blocks))
	k := 0
	for i := 0; i < len(blocks); {
		for k < len(joins) && joins[k].From < blocks[i].From {
			k++
		}
		if k < len(joins) && joins[k].From == blocks[i].From {
			j := i
			for j < len(blocks) && blocks[j].To <= joins[k].To && blocks[j].To-blocks[j].From == 1 {
				j++
			}
			if j-i > 1 && blocks[j-1].To == joins[k].To {
				if joined == nil {
					joined = map[guiwidget.Block]bool{}
				}
				out, joined[joins[k]] = append(out, joins[k]), true
				i, k = j, k+1
				continue
			}
		}
		out = append(out, blocks[i])
		i++
	}
	return out, joined
}

// LayOut lays block b out: an embed line off the cursor as its file, a joined paragraph line by
// line, anything else as golib does.
func (r *embedRenderer) LayOut(b guiwidget.Block, lines []string, width float32, inside bool, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	if r.joined[b] {
		return r.layLines(b, lines, width, inside, t, th, host)
	}
	if !inside {
		if md, ok := r.embedded(lines, b); ok {
			return r.layEmbed(b, md, width, t, th, host)
		}
		if md, ok := csvFence(lines, b); ok {
			return r.layEmbed(b, md, width, t, th, host)
		}
	}
	return r.MarkdownRenderer.LayOut(b, lines, width, inside, t, th, host)
}

// layLines lays a joined paragraph out as golib lays each of its lines, one under another.
func (r *embedRenderer) layLines(b guiwidget.Block, lines []string, width float32, inside bool, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	var out guiwidget.BlockLayout
	for ln := b.From; ln < b.To; ln++ {
		one := r.MarkdownRenderer.LayOut(guiwidget.Block{From: ln, To: ln + 1}, lines, width, inside, t, th, host)
		for _, ll := range one.Lines {
			ll.Y += out.Height
			out.Lines = append(out.Lines, ll)
		}
		out.Height += one.Height
	}
	return out
}

// embedded is the Markdown drawn for block b when it is a line holding an image alone in its
// paragraph, of the open note, and the file is one the Rendered view draws.
func (r *embedRenderer) embedded(lines []string, b guiwidget.Block) (string, bool) {
	ln := b.From
	e, ok := r.alone[ln]
	if !ok || b.To-b.From != 1 || ln >= len(lines) || lines[ln] != e.text {
		return "", false
	}
	note := r.notes.file()
	if note == "" {
		return "", false
	}
	return export.EmbedMarkdown(note, e.dest, embedTableRows)
}

// csvFence is the table a ```csv fence, block b, is drawn as: its rows as a .csv embed's
// (export.CSVMarkdown). Only a whole fence, its closing fence written, is: one being typed, or
// clipped, stays code, as does one that is not CSV.
func csvFence(lines []string, b guiwidget.Block) (string, bool) {
	if b.To-b.From < 2 || b.To > len(lines) {
		return "", false
	}
	open := strings.TrimLeft(lines[b.From], " ")
	if len(lines[b.From])-len(open) > 3 || len(open) < 3 || (open[0] != '`' && open[0] != '~') {
		return "", false
	}
	ch, n := open[0], 0
	for n < len(open) && open[n] == ch {
		n++
	}
	info := strings.Fields(open[n:])
	if n < 3 || len(info) == 0 || !strings.EqualFold(info[0], "csv") || (ch == '`' && strings.Contains(open[n:], "`")) {
		return "", false
	}
	last := strings.TrimLeft(lines[b.To-1], " ")
	if len(lines[b.To-1])-len(last) > 3 || len(last) < n || strings.Trim(strings.TrimRight(last, " \t"), string(ch)) != "" {
		return "", false // not closed in the block
	}
	return export.CSVMarkdown([]byte(strings.Join(lines[b.From+1:b.To-1], "\n")), embedTableRows)
}

// layEmbed lays the embed of block b out as md, the Markdown its file stands for, drawn as the
// block's picture: the line's source identity and clicks stay the line's. A diagram or a chart is
// asked of the window's host for this block, as a mermaid or vega-lite fence's is; while it is not
// ready, or when it is declined (a chart that is not JSON), md is laid out as golib lays it, the
// source as code.
func (r *embedRenderer) layEmbed(b guiwidget.Block, md string, width float32, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	injected := strings.Split(md, "\n")
	if lang := strings.TrimLeft(injected[0], "`"); (lang == "mermaid" || lang == "vega-lite") && host != nil && r.diagrams != nil && len(injected) > 2 {
		src := strings.Join(injected[1:len(injected)-1], "\n")
		a := host.Diagram(r.diagrams, b, guiwidget.DiagramRequest{Lang: lang, Src: src, Width: width, Scale: t.Scale(), Theme: th})
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
