//go:build gui

package tui

import (
	"bytes"
	"crypto/sha256"
	"image"
	_ "image/gif" // decoders: the Rendered view draws a PNG, a JPEG or a GIF
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path"
	"path/filepath"
	"strings"

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

// layCell lays out the fence b as golib does, then each output file under it, one under another,
// as the block's picture. Only the first diagram or chart is asked of the host, which keeps one
// diagram per block: any further one is laid out as its source.
func (r *embedRenderer) layCell(b guiwidget.Block, lines []string, note string, files []string, width float32, t *gui.TextShaper, th guiwidget.Theme, host guiwidget.DiagramHost) guiwidget.BlockLayout {
	out := r.MarkdownRenderer.LayOut(b, lines, width, false, t, th, host)
	under := pictureHeight(out) // golib's own picture under the fence's lines, which the stack takes first
	var stack cellStack
	if out.Picture != nil {
		stack.add(out.Picture, out.PictureSize)
	}
	diagram := host
	for _, f := range files {
		if pic, ok := r.image(note, f, width); ok {
			stack.add(pic, pic.size)
			continue
		}
		md, ok := export.OutputMarkdown(note, f, embedTableRows)
		if !ok {
			continue
		}
		bl := r.layEmbed(b, md, width, t, th, diagram)
		if lang := strings.TrimLeft(strings.SplitN(md, "\n", 2)[0], "`"); lang == "mermaid" || lang == "vega-lite" {
			diagram = nil
		}
		stack.add(bl.Picture, bl.PictureSize)
	}
	out.Picture, out.PictureSize = stack, gui.Size{W: width, H: stack.height}
	out.Height += stack.height - under
	return out
}

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
	ext := strings.ToLower(path.Ext(dest))
	if ext != ".png" && ext != ".jpg" && ext != ".jpeg" && ext != ".gif" {
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
