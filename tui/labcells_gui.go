//go:build gui

package tui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"image"
	_ "image/gif" // decoders: the Rendered view draws a PNG, a JPEG or a GIF
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/lab"
)

// A LAB NOTE'S CELLS IN THE RENDERED VIEW (ADR 1792457255). In a lab note (lab.Is), a whole fence off
// the cursor is drawn with the outputs a run left for it under it (lab.Outputs): text as a text fence,
// a diagram, a chart or a table as an embed of that file is drawn, an image as the image. The
// fence's own lines stay the editor's (its source, clicks and cursor); the outputs are the block's
// picture. An image a note embeds alone on its line is drawn as the image too.

// closedFence is block b's info string and code when b is a whole fence, its closing fence written:
// one being typed, or clipped, is not. A backtick fence's info string holds no backtick
// (CommonMark), or it is no fence.
func closedFence(lines []string, b guiwidget.Block) (info, code string, ok bool) {
	if b.To-b.From < 2 || b.To > len(lines) {
		return "", "", false
	}
	open := strings.TrimLeft(lines[b.From], " ")
	if len(lines[b.From])-len(open) > 3 || len(open) < 3 || (open[0] != '`' && open[0] != '~') {
		return "", "", false
	}
	ch, n := open[0], 0
	for n < len(open) && open[n] == ch {
		n++
	}
	if n < 3 || (ch == '`' && strings.Contains(open[n:], "`")) {
		return "", "", false
	}
	last := strings.TrimLeft(lines[b.To-1], " ")
	if len(lines[b.To-1])-len(last) > 3 || len(last) < n || strings.Trim(strings.TrimRight(last, " \t"), string(ch)) != "" {
		return "", "", false // not closed in the block
	}
	return open[n:], strings.Join(lines[b.From+1:b.To-1], "\n"), true
}

// cellOutputs are the output files of block b when it is a cell of the open lab note that a run left
// outputs for: slash paths in the note's folder, in name order.
func (r *embedRenderer) cellOutputs(lines []string, b guiwidget.Block) (note string, files []string) {
	note = r.notes.file()
	if !lab.Is(note) {
		return "", nil
	}
	info, code, ok := closedFence(lines, b)
	if !ok {
		return "", nil
	}
	return note, lab.Outputs(os.DirFS(filepath.Dir(note)), lab.OutDir(note), lab.CellKey(lab.FenceLang(info), code))
}

// layCell lays out the fence b as code, then each output file under it, one under another, as the
// block's picture. The host keeps one diagram request per block, so the cell's diagrams and charts
// are one request (cellDiagrams), and the fence itself, a cell in its kernel's language, asks none.
func (r *embedRenderer) layCell(b guiwidget.Block, lines []string, note string, files []string, width float32, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	out := r.MarkdownRenderer.LayOut(b, lines, width, false, t, th, nil)
	under := pictureHeight(out) // golib's own picture under the fence's lines, which the stack takes first
	type output struct {
		pic  imagePicture // an image's picture; otherwise md is the output as Markdown
		md   string
		part int // the index of its diagram or chart in parts; -1 for none
	}
	var outputs []output
	var parts []guiwidget.DiagramRequest
	for _, f := range files {
		if pic, ok := r.image(note, f, width); ok {
			outputs = append(outputs, output{pic: pic, part: -1})
			continue
		}
		md, ok := export.OutputMarkdown(note, f, embedTableRows)
		if !ok {
			continue
		}
		o := output{md: md, part: -1}
		if lang, src, ok := diagramSource(md); ok {
			o.part = len(parts)
			parts = append(parts, guiwidget.DiagramRequest{Lang: lang, Src: src, Width: width, Scale: t.Scale(), Theme: th})
		}
		outputs = append(outputs, o)
	}
	var drawn cellAnswers
	if len(parts) > 0 && host != nil && r.diagrams != nil {
		drawn, _ = host.Diagram(cellDiagrams{d: r.diagrams, parts: parts}, b, cellRequest(parts)).Pic.(cellAnswers)
	}
	var stack cellStack
	if out.Picture != nil {
		stack.add(out.Picture, out.PictureSize)
	}
	for _, o := range outputs {
		switch {
		case o.pic.img != nil:
			stack.add(o.pic, o.pic.size)
		case o.part >= 0 && o.part < len(drawn) && drawn[o.part].State != guiwidget.Declined && drawn[o.part].Pic != nil:
			stack.add(drawn[o.part].Pic, drawn[o.part].Size)
		default: // text, a table, or a diagram not drawn (yet): its Markdown as golib lays it
			bl := r.layEmbed(b, o.md, width, t, th, nil)
			stack.add(bl.Picture, bl.PictureSize)
		}
	}
	out.Picture, out.PictureSize = stack, gui.Size{W: width, H: stack.height}
	out.Height += stack.height - under
	return out
}

// cellDiagramLang is the language of a cell's request for its outputs' diagrams: no fence's, so the
// host never shows another block's picture for it while it is pending.
const cellDiagramLang = "autodoc/lab-cell"

// cellRequest is a cell's one request of the host for its diagrams and charts: its source is every
// part's, so a rerun's changed outputs are a new request.
func cellRequest(parts []guiwidget.DiagramRequest) guiwidget.DiagramRequest {
	var src strings.Builder
	for _, p := range parts {
		src.WriteString(p.Lang + " " + strconv.Itoa(len(p.Src)) + "\n" + p.Src)
	}
	return guiwidget.DiagramRequest{Lang: cellDiagramLang, Src: src.String(), Width: parts[0].Width, Scale: parts[0].Scale, Theme: parts[0].Theme}
}

// cellDiagrams asks d for each of a cell's diagrams and charts under the host's one request: its
// context and its ready. It calls ready once, when the last pending part is ready: the host then
// asks again and cancels this request's context, which would withdraw a part still being laid out.
type cellDiagrams struct {
	d     guiwidget.Diagrammer
	parts []guiwidget.DiagramRequest
}

func (c cellDiagrams) Diagram(ctx context.Context, _ guiwidget.DiagramRequest, ready func()) guiwidget.DiagramAnswer {
	answers := make(cellAnswers, len(c.parts))
	var left atomic.Int32
	left.Store(1) // the asking's own: no part's ready ends the wait while parts are still being asked
	done := func() {
		if left.Add(-1) == 0 {
			ready()
		}
	}
	state := guiwidget.Ready
	for i, p := range c.parts {
		left.Add(1)
		var once sync.Once
		answers[i] = c.d.Diagram(ctx, p, func() { once.Do(done) })
		if answers[i].State == guiwidget.Pending {
			state = guiwidget.Pending
		} else {
			left.Add(-1) // answered: its ready is never called
		}
	}
	if state == guiwidget.Pending {
		done()
	}
	return guiwidget.DiagramAnswer{State: state, Pic: answers, Size: answers.size()}
}

// cellAnswers are a cell's parts' answers, in its outputs' order: the picture the host keeps for the
// cell, which layCell takes apart to draw each diagram under its own output.
type cellAnswers []guiwidget.DiagramAnswer

func (a cellAnswers) drawn() cellStack {
	var stack cellStack
	for _, p := range a {
		if p.Pic != nil && p.State != guiwidget.Declined {
			stack.add(p.Pic, p.Size)
		}
	}
	return stack
}

func (a cellAnswers) size() gui.Size {
	var s gui.Size
	for _, it := range a.drawn().items {
		s.W, s.H = max(s.W, it.size.W), s.H+it.size.H
	}
	return s
}

// Paint draws the parts drawn, one under another; layCell draws them among the cell's other outputs.
func (a cellAnswers) Paint(c gui.Canvas) { a.drawn().Paint(c) }

// pictureHeight is the height of the picture golib laid out under a block's lines; the block's height
// counts it, and the stack replaces it.
func pictureHeight(bl guiwidget.BlockLayout) float32 {
	if bl.Picture == nil {
		return 0
	}
	return bl.PictureSize.H
}

// cellStack draws pictures one under another.
type cellStack struct {
	items  []stackItem
	height float32
}

type stackItem struct {
	pic  gui.View
	size gui.Size
}

func (s *cellStack) add(pic gui.View, size gui.Size) {
	if pic == nil || size.H <= 0 {
		return
	}
	s.items = append(s.items, stackItem{pic, size})
	s.height += size.H
}

func (s cellStack) Paint(c gui.Canvas) {
	var y float32
	for _, it := range s.items {
		it.pic.Paint(c.Sub(gui.Rect{Y: y, W: it.size.W, H: it.size.H}))
		y += it.size.H
	}
}

// imagePicture draws a decoded image at its laid-out size: its width at most the block's, its height
// in proportion.
type imagePicture struct {
	img  *gui.Image
	size gui.Size
}

func (p imagePicture) Paint(c gui.Canvas) {
	full := p.img.Size()
	c.DrawImage(p.img, gui.Rect{W: p.size.W, H: p.size.H}, gui.Rect{W: full.W, H: full.H})
}

// image is the picture of the image dest names for the note (export.EmbedImage finds and bounds it),
// decoded once per file and kept while the file is unchanged; false for a file that is not an image
// the standard decoders read (PNG, JPEG, GIF).
func (r *embedRenderer) image(note, dest string, width float32) (imagePicture, bool) {
	if !decodesImage(dest) {
		return imagePicture{}, false
	}
	data, ok := export.EmbedImage(note, dest)
	if !ok {
		return imagePicture{}, false
	}
	img, ok := r.images.get(note, dest, data)
	if !ok {
		return imagePicture{}, false
	}
	full := img.Size()
	if full.W <= 0 || full.H <= 0 {
		return imagePicture{}, false
	}
	w := min(width, full.W)
	return imagePicture{img: img, size: gui.Size{W: w, H: full.H * w / full.W}}, true
}

// decodesImage reports whether dest names an image the Rendered view decodes: a PNG, a JPEG or a GIF.
func decodesImage(dest string) bool {
	ext := strings.ToLower(path.Ext(dest))
	return ext == ".png" || ext == ".jpg" || ext == ".jpeg" || ext == ".gif"
}

// decodedImages are the images drawn so far, by note and destination, each kept with the digest of
// the bytes it was decoded from: gui.NewImage uploads it again when made again, so an unchanged file
// is reused, and a rerun's new image replaces the old one.
type decodedImages map[string]decodedImage

type decodedImage struct {
	sum [sha256.Size]byte
	img *gui.Image
}

func (d *decodedImages) get(note, dest string, data []byte) (*gui.Image, bool) {
	key, sum := note+"\x00"+dest, sha256.Sum256(data)
	if had, ok := (*d)[key]; ok && had.sum == sum {
		return had.img, true
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, false
	}
	if *d == nil {
		*d = decodedImages{}
	}
	img := gui.NewImage(decoded)
	(*d)[key] = decodedImage{sum: sum, img: img}
	return img, true
}
