package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"time"

	"github.com/yongjohnlee80/golib/decl"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/export/mermaid"
)

// IMAGE PREVIEWS — a Mermaid diagram and a file's HTML, as images in the terminal (ADR 0212 §6).
//
// With View › Image previews on, a terminal that confirmed kitty's graphics protocol, and a
// headless Chromium or Chrome, the preview is an image: the exported HTML, or a page of the one
// diagram drawn by the vendored mermaid (core/export/mermaid), rendered offline in the active
// theme's colours. The whole page is rendered, at the preview's width (a diagram at its own), and
// the image scrolls in its dialog: the arrows, j k h l, Page Up/Down, [ ], Home/End and the wheel
// (golib's scrollable Image). Zoom in and Zoom out render it again at another scale, as a
// browser's zoom does; the dialog keeps its size. Otherwise the HTML opens in the default browser
// and the diagram's dialog shows its source, and the preview says why. Turning the preference off
// is how both can be compared on the same document.

// zooms are the previews' zoom steps; zoomDefault is 100%.
var zooms = []float64{0.5, 0.67, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2}

const zoomDefault = 4

// imageWait is how long a preview's image cells may take to be laid out, and to become usable after
// a resize, once its dialog opens; then it falls back.
const imageWait = 2 * time.Second

// minImageCols and minImageRows are the smallest image area worth rendering into: on a smaller one
// a diagram or a page is unreadable, and the fallback serves better.
const (
	minImageCols = 16
	minImageRows = 4
)

// imageMode reports whether a preview can be an image, and why not when it cannot.
func (h *Host) imageMode() (bool, string) {
	if !h.prefs.images {
		return false, "image previews are off (View › Image previews)"
	}
	switch h.graphics() {
	case tuicore.TriNo:
		return false, "this terminal does not draw images (no kitty graphics)"
	case tuicore.TriUnknown:
		return false, "the terminal did not confirm kitty graphics (inside tmux: set -g allow-passthrough on)"
	}
	if _, ok := widget.HTMLRasterizer(); !ok && h.rasterizeOverride == nil {
		return false, "no headless Chromium or Chrome to render with"
	}
	return true, ""
}

// graphics is the terminal's kitty graphics answer; graphicsOverride replaces it in tests.
func (h *Host) graphics() tuicore.Tri {
	if h.graphicsOverride != nil {
		return h.graphicsOverride()
	}
	return h.p.App().Capabilities().KittyGraphics
}

// rasterizer renders a page as an image: golib's headless browser, or rasterizeOverride in tests.
// Taken on the loop, it is called off it.
func (h *Host) rasterizer() func(context.Context, []byte, widget.Page) ([]byte, error) {
	if h.rasterizeOverride != nil {
		return h.rasterizeOverride
	}
	return widget.RasterizeHTMLPage
}

// exportTheme is the export palette of the active theme.
func (h *Host) exportTheme() string { return export.ThemeOf(h.theme) }

// toggleImagePreviews is View › Image previews.
func (h *Host) toggleImagePreviews() {
	v := !h.prefs.images
	h.setPref(prefImages, strconv.FormatBool(v), func(p *prefs) { p.images = v })
	if v {
		if ok, why := h.imageMode(); !ok {
			h.say("image previews on; for now: " + why)
			return
		}
	}
	h.say("image previews " + map[bool]string{true: "on", false: "off: diagram sources and the browser"}[v])
}

func (h *Host) setImagesIndex(i int) {
	if i == 0 || i == 1 {
		v := i == 0
		h.setPref(prefImages, strconv.FormatBool(v), func(p *prefs) { p.images = v })
	}
}

// previewing is the image preview open: its dialog, the page it renders, whether that page is as
// wide as its content (a diagram) and how tall it may be, and its zoom step.
type previewing struct {
	dialog    string
	page      []byte
	wide      bool
	maxHeight int
	zoom      int
	// background is the page's colour, what the render cuts below and beside it
	background string
}

// diagramMaxHeight bounds a diagram's page, in pixels: rendered as wide as it is, a window as tall as
// a long page's would take the browser seconds.
const diagramMaxHeight = 4096

// showDiagram shows a Mermaid block: an image where it can be, else its source, saying why.
func (h *Host) showDiagram(source string) {
	ok, why := h.imageMode()
	if !ok {
		h.showDiagramText(source, "Diagram source · "+why)
		return
	}
	theme := h.exportTheme()
	page, err := export.DiagramPage(source, theme)
	if err != nil {
		h.showDiagramText(source, "Diagram source · "+err.Error())
		return
	}
	h.diagramImage = true
	h.diagramHelpText = "Image · drawn offline by mermaid " + mermaid.Version + " in the " + theme + " theme · scroll: arrows, j k h l, PgUp/PgDn, wheel"
	h.set("App.diagramTextShown", false)
	h.set("App.diagramImageShown", true)
	h.set("App.diagramHelp", h.diagramHelpText)
	h.open("diagram")
	h.imagePreview = previewing{dialog: "diagram", page: page, wide: true, maxHeight: diagramMaxHeight, zoom: zoomDefault,
		background: export.Background(theme)}
	h.renderPreview(func(why string) { h.showDiagramText(source, "Diagram source · "+why) })
}

// showDiagramText shows the diagram's source (or a diagnostic), with help saying why it is not an
// image.
func (h *Host) showDiagramText(text, help string) {
	h.diagramImage, h.diagramHelpText = false, help
	h.set("App.diagramImageShown", false)
	h.set("App.diagramText", text)
	h.set("App.diagramTextShown", true)
	h.set("App.diagramHelp", help)
	h.open("diagram")
}

// renderPreview renders the open preview's page into its Image at its zoom, once the Image has its
// cells, keeping the part of it shown where it was; fallback is the preview's, for an image that
// cannot be had.
func (h *Host) renderPreview(fallback func(why string)) {
	pv := h.imagePreview
	h.withImageCells(pv.dialog, fallback, func(img *widget.Image, cols, rows int) {
		shown, w, ht := img.Scroll()
		gen, rasterize := h.previewGen, h.rasterizer()
		// a page taller than a terminal shows an image (widget.MaxImagePixels) is cut into strips
		// here, off the loop: a long file's page, whole, shows nothing at all
		do(h, func(ctx context.Context) answerOf[widget.Strips] {
			png, err := rasterize(ctx, pv.page, widget.Page{Width: cols * widget.CellPixelsW,
				MinHeight: rows * widget.CellPixelsH, MaxHeight: pv.maxHeight, Scale: zooms[pv.zoom], Wide: pv.wide, Background: pv.background})
			if err != nil {
				return answerOf[widget.Strips]{err: err}
			}
			strips, err := widget.SplitPNG(png)
			return answerOf[widget.Strips]{v: strips, err: err}
		}, func(a answerOf[widget.Strips]) {
			if gen != h.previewGen {
				return // closed, or another preview since
			}
			if a.err != nil {
				fallback("the image failed: " + a.err.Error())
				return
			}
			img.SetStrips(a.v)
			if w > 0 && ht > 0 { // the same place in the page, at the new scale
				_, nw, nh := img.Scroll()
				img.ScrollTo(shown.X*nw/w, shown.Y*nh/ht)
			}
		})
	})
}

// zoomPreview is Zoom in (step 1) and Zoom out (-1) in an image preview: the page rendered again
// at the next scale, the dialog as it was.
func (h *Host) zoomPreview(step int) {
	pv := &h.imagePreview
	if pv.page == nil || (pv.dialog == "diagram" && !h.diagramImage) {
		return
	}
	z := min(max(pv.zoom+step, 0), len(zooms)-1)
	if z == pv.zoom {
		h.say(fmt.Sprintf("zoom is at its %s", map[bool]string{true: "largest", false: "smallest"}[step > 0]))
		return
	}
	pv.zoom = z
	h.say(fmt.Sprintf("zoom %d%%", int(zooms[z]*100+0.5)))
	fallback := func(why string) { h.say("zoom: " + why) }
	h.renderPreview(fallback)
}

// withImageCells runs fn once the Image in the dialog (main.qml's id for it) has been laid out with
// a usable area (minImageCols × minImageRows), with its cells. An area still unusable at imageWait
// (a screen too small, a layout that never gave it cells) runs fallback instead, with why: a
// preview always ends as an image or as its fallback, never blank (ADR 0212 §6). A screen resized
// to a usable size before then renders the image. A new preview or a closed dialog drops both.
func (h *Host) withImageCells(dialog string, fallback func(why string), fn func(img *widget.Image, cols, rows int)) {
	h.previewGen++
	gen := h.previewGen
	deadline := time.Now().Add(imageWait)
	var try func()
	try = func() {
		if gen != h.previewGen {
			return
		}
		cols, rows := 0, 0
		img, ok := imageUnder(h.p, dialog)
		if ok {
			cols, rows = img.Cells()
			if cols >= minImageCols && rows >= minImageRows {
				fn(img, cols, rows)
				return
			}
		}
		if time.Now().After(deadline) {
			if cols > 0 && rows > 0 {
				fallback(fmt.Sprintf("the screen leaves the image %d×%d cells, too small to read", cols, rows))
			} else {
				fallback("the preview's image area was never laid out")
			}
			return
		}
		h.after(20*time.Millisecond, try)
	}
	h.after(0, try)
}

// imageUnder is the Image in the subtree of the node with id: a dialog's own ids are its file's,
// out of the program's reach, so the dialog's Image is found by its type.
func imageUnder(p *tuidecl.Program, id string) (*widget.Image, bool) {
	root, ok := p.Tree().NodeByID(id)
	if !ok {
		return nil, false
	}
	queue := []decl.NodeID{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if c, ok := p.Adapter().Component(n); ok {
			if img, ok := c.(*widget.Image); ok {
				return img, true
			}
		}
		queue = append(queue, p.Tree().Children(n)...)
	}
	return nil, false
}

// lastDiagramImage reports whether the Mermaid preview is in image mode (not its fallback).
func (h *Host) lastDiagramImage() bool { return h.diagramImage }

// previewClosed is a preview dialog closing: what it was waiting on is dropped.
func (h *Host) previewClosed() { h.previewGen++ }

// previewHTMLImage renders the exported HTML into the HTML preview's image; Open in browser has the
// file the image was made from.
func (h *Host) previewHTMLImage(content []byte, path string) {
	h.htmlPreviewPath = path
	h.set("App.htmlPreviewTitle", "HTML preview · "+h.file.name())
	h.set("App.htmlPreviewHelp", "Image · rendered offline by a headless browser in the "+h.exportTheme()+
		" theme · scroll: arrows, j k h l, PgUp/PgDn, wheel · links and selection: Open in browser")
	h.open("htmlPreview")
	h.imagePreview = previewing{dialog: "htmlPreview", page: content, zoom: zoomDefault, background: export.Background(h.exportTheme())}
	h.renderPreview(func(why string) {
		h.closeDialog("htmlPreview")
		h.notify("HTML preview: " + why + "; opening it in the browser")
		h.openPreviewInBrowser()
	})
}

// openPreviewInBrowser opens the HTML the preview was made from in the default browser.
func (h *Host) openPreviewInBrowser() {
	path := h.htmlPreviewPath
	if path == "" {
		return
	}
	do(h, func(ctx context.Context) error { return h.browser(ctx, path) }, func(err error) {
		if err != nil {
			h.notify("the browser did not open: " + err.Error())
			return
		}
		h.say("HTML preview opened in the browser: " + filepath.Base(path))
	})
}
