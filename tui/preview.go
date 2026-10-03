package tui

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/decl"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/diagram"
	"github.com/yongjohnlee80/autodoc/core/export"
)

// IMAGE PREVIEWS — a Mermaid diagram and a note's HTML, as images in the terminal (ADR 0212 §6).
//
// With View › Image previews on, a terminal that confirmed kitty's graphics protocol, and the tool
// to render with (rsvg-convert for a diagram, a headless Chromium or Chrome for HTML), the preview
// is an image: the diagram's SVG or the exported HTML rendered offline to a PNG the size of the
// preview's cells, in the active theme's colours. Otherwise the diagram is drawn as a terminal
// graph and the HTML opens in the default browser, and the preview says why. Turning the
// preference off is how both can be compared on the same document.

// cellPixels is the pixels a terminal cell is taken to be, to render a PNG at the cells' aspect.
const (
	cellPixelsW = 10
	cellPixelsH = 20
)

// imageWait is how long a preview's image cells may take to be laid out after its dialog opens.
const imageWait = 2 * time.Second

// imageMode reports whether a preview can be an image, and why not when it cannot.
func (h *Host) imageMode(html bool) (bool, string) {
	if !h.prefs.images {
		return false, "image previews are off (View › Image previews)"
	}
	switch h.graphics() {
	case tuicore.TriNo:
		return false, "this terminal does not draw images (no kitty graphics)"
	case tuicore.TriUnknown:
		return false, "the terminal did not confirm kitty graphics (inside tmux: set -g allow-passthrough on)"
	}
	if html {
		if _, ok := widget.HTMLRasterizer(); !ok {
			return false, "no headless Chromium or Chrome to render HTML"
		}
	} else if _, ok := widget.SVGRasterizer(); !ok {
		return false, "rsvg-convert is not installed"
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

// exportTheme is the export palette of the active theme.
func (h *Host) exportTheme() string { return export.ThemeOf(h.theme) }

// toggleImagePreviews is View › Image previews.
func (h *Host) toggleImagePreviews() {
	v := !h.prefs.images
	h.setPref(prefImages, strconv.FormatBool(v), func(p *prefs) { p.images = v })
	if v {
		if ok, why := h.imageMode(false); !ok {
			h.say("image previews on; for now: " + why)
			return
		}
	}
	h.say("image previews " + map[bool]string{true: "on", false: "off: terminal graphs and the browser"}[v])
}

func (h *Host) setImagesIndex(i int) {
	if i == 0 || i == 1 {
		v := i == 0
		h.setPref(prefImages, strconv.FormatBool(v), func(p *prefs) { p.images = v })
	}
}

// showDiagram shows a parsed diagram: an image when it can be, else the terminal graph.
func (h *Host) showDiagram(model diagram.Model) {
	ok, why := h.imageMode(false)
	if !ok {
		h.showDiagramText(model.Terminal(), "Terminal graph · "+why)
		return
	}
	theme := h.exportTheme()
	h.set("App.diagramTextShown", false)
	h.set("App.diagramImageShown", true)
	h.set("App.diagramHelp", "Image · rendered offline by rsvg-convert in the "+theme+" theme · View › Image previews turns it off")
	h.open("diagram")
	h.withImageCells("diagram", func(img *widget.Image, cols, rows int) {
		w, ht := cols*cellPixelsW, rows*cellPixelsH
		svg, err := export.DiagramSVG(model, theme)
		if err != nil {
			h.showDiagramText(model.Terminal(), "Terminal graph · "+err.Error())
			return
		}
		fitted := fitSVG(svg, w, ht, theme)
		gen := h.previewGen
		do(h, func(ctx context.Context) answerOf[[]byte] {
			png, err := widget.RasterizeSVG(ctx, []byte(fitted), w)
			return answerOf[[]byte]{v: png, err: err}
		}, func(a answerOf[[]byte]) {
			if gen != h.previewGen {
				return // closed, or another preview since
			}
			if a.err != nil {
				h.showDiagramText(model.Terminal(), "Terminal graph · the image failed: "+a.err.Error())
				return
			}
			img.SetPNG(a.v)
		})
	})
}

// showDiagramText shows the terminal graph (or a diagnostic) with help saying why it is not an image.
func (h *Host) showDiagramText(text, help string) {
	h.set("App.diagramImageShown", false)
	h.set("App.diagramText", text)
	h.set("App.diagramTextShown", true)
	h.set("App.diagramHelp", help)
	h.open("diagram")
}

// withImageCells runs fn once the Image in the dialog (main.qml's id for it) has been laid out,
// with its cells; a new preview or a dialog closed in the meantime drops it.
func (h *Host) withImageCells(dialog string, fn func(img *widget.Image, cols, rows int)) {
	h.previewGen++
	gen := h.previewGen
	deadline := time.Now().Add(imageWait)
	var try func()
	try = func() {
		if gen != h.previewGen {
			return
		}
		img, ok := imageUnder(h.p, dialog)
		if ok {
			if cols, rows := img.Cells(); cols > 0 && rows > 0 {
				fn(img, cols, rows)
				return
			}
		}
		if time.Now().After(deadline) {
			h.say("the preview's image area was never laid out")
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

// previewClosed is a preview dialog closing: what it was waiting on is dropped.
func (h *Host) previewClosed() { h.previewGen++ }

// fitSVG places svg, letterboxed and centred, in a w × h pixel canvas of the theme's background,
// so the PNG has the cells' aspect and the terminal does not stretch the diagram.
func fitSVG(svg string, w, h int, theme string) string {
	inner := strings.Replace(svg, "<svg ", fmt.Sprintf(`<svg x="0" y="0" width="%d" height="%d" preserveAspectRatio="xMidYMid meet" `, w, h), 1)
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d"><rect width="100%%" height="100%%" fill="%s"/>%s</svg>`,
		w, h, w, h, export.Background(theme), inner)
}

// previewHTMLImage renders the exported HTML into the HTML preview's image; Open in browser has the
// file the image was made from.
func (h *Host) previewHTMLImage(content []byte, path string) {
	h.htmlPreviewPath = path
	h.set("App.htmlPreviewTitle", "HTML preview · "+h.note.name())
	h.set("App.htmlPreviewHelp", "Image · rendered offline by a headless browser in the "+h.exportTheme()+
		" theme · links and selection: Open in browser · View › Image previews turns it off")
	h.open("htmlPreview")
	h.withImageCells("htmlPreview", func(img *widget.Image, cols, rows int) {
		gen := h.previewGen
		do(h, func(ctx context.Context) answerOf[[]byte] {
			png, err := widget.RasterizeHTML(ctx, content, cols*cellPixelsW, rows*cellPixelsH)
			return answerOf[[]byte]{v: png, err: err}
		}, func(a answerOf[[]byte]) {
			if gen != h.previewGen {
				return
			}
			if a.err != nil {
				h.closeDialog("htmlPreview")
				h.notify("HTML preview as an image failed (" + a.err.Error() + "); opening it in the browser")
				h.openPreviewInBrowser()
				return
			}
			img.SetPNG(a.v)
		})
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
