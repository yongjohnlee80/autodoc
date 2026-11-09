package tui

import (
	"context"
	"slices"
	"strconv"
	"time"

	"github.com/yongjohnlee80/golib/decl"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget/htmlview"

	"github.com/yongjohnlee80/autodoc/core/export"
	"github.com/yongjohnlee80/autodoc/core/export/mermaid"
)

// IMAGE PREVIEWS — a Mermaid diagram as an image in the terminal
// (docs/decisions/search.md#outline-breadcrumbs-and-previews). A file's HTML is the pane beside the
// editor instead, in cells (htmlpane.go).
//
// With View › Image previews on, a terminal that confirmed kitty's graphics protocol, and a
// headless Chromium or Chrome, View › Diagram is an image: a page of the one diagram drawn by the
// vendored mermaid (core/export/mermaid), rendered offline in the active theme's colours, at its
// own width. The image scrolls in its dialog: the arrows, j k h l, Page Up/Down, [ ], Home/End and
// the wheel (golib's scrollable Image). Zoom in and Zoom out render it again at another scale, as a
// browser's zoom does; the dialog keeps its size. Otherwise the dialog shows the diagram's source,
// and says why. Turning the preference off is how both can be compared on the same diagram.

// zooms are the previews' zoom steps; zoomDefault is 100%.
var zooms = []float64{0.5, 0.67, 0.8, 0.9, 1, 1.1, 1.25, 1.5, 1.75, 2}

const zoomDefault = 4

// imageWait is how long the preview's image cells may take to be laid out, and to become usable
// after a resize, once its dialog opens; then it falls back.
const imageWait = 2 * time.Second

// minImageCols and minImageRows are the smallest image area worth rendering into: on a smaller one
// a diagram is unreadable, and its source serves better.
const (
	minImageCols = 16
	minImageRows = 4
)

// imageMode reports whether a preview can be an image, and why not when it cannot.
func (h *Host) imageMode() (bool, string) {
	if !h.prefs.images {
		return false, h.tr("autodoc.text.preview.image_previews_are_off_view_image_previews")
	}
	switch h.graphics() {
	case tuicore.TriNo:
		return false, h.tr("autodoc.text.preview.this_terminal_does_not_draw_images_no_kitty_grap")
	case tuicore.TriUnknown:
		return false, h.tr("autodoc.text.preview.the_terminal_did_not_confirm_kitty_graphics_insi")
	}
	if !h.browserFound() {
		return false, h.tr("autodoc.text.preview.no_headless_chromium_or_chrome_to_render_with")
	}
	return true, ""
}

// browserFound reports whether a headless browser draws mermaid.js here: golib's
// HTMLRasterizer (a browser on the PATH, or installed as a macOS application), or a test's.
func (h *Host) browserFound() bool {
	if h.browserOverride != nil {
		return h.browserOverride()
	}
	_, ok := htmlview.HTMLRasterizer()
	return ok || h.rasterizeOverride != nil
}

// graphics is the terminal's kitty graphics answer; graphicsOverride replaces it in tests.
func (h *Host) graphics() tuicore.Tri {
	if h.graphicsOverride != nil {
		return h.graphicsOverride()
	}
	return h.p.App().Capabilities().KittyGraphics
}

// rasterizer renders a diagram's page as an image: golib's headless browser, or rasterizeOverride
// in tests.
// Taken on the loop, it is called off it.
func (h *Host) rasterizer() func(context.Context, []byte, htmlview.Page) ([]byte, error) {
	if h.rasterizeOverride != nil {
		return h.rasterizeOverride
	}
	return htmlview.RasterizeHTMLPage
}

// exportTheme is the export palette of the active theme.
func (h *Host) exportTheme() string {
	if h.themeCatalog != nil {
		return h.themeCatalog.ExportStyle(h.theme)
	}
	return export.ThemeOf(h.theme)
}

// toggleImagePreviews is View › Image previews.
func (h *Host) toggleImagePreviews() {
	v := !h.prefs.images
	h.setPref(prefImages, strconv.FormatBool(v), func(p *prefs) { p.images = v })
	if v {
		if ok, why := h.imageMode(); !ok {
			h.say(h.trf("autodoc.notice.preview.image_previews_on_for_now", why))
			return
		}
	}
	state := h.tr("autodoc.notice.preview.off_diagram_sources")
	if v {
		state = h.tr("autodoc.notice.preview.on")
	}
	h.say(h.trf("autodoc.notice.preview.image_previews", state))
}

// mermaidEngines are what can draw a Mermaid diagram (File › Preview option): mermaid.js in a
// headless browser, as an image, as a browser would draw it, or golib's Mermaid, natively, at once
// but for the types golib draws: in a window's pixels (golib gui/mermaid), in a terminal's
// characters (golib tui/mermaid, cellDiagram).
var mermaidEngines = []string{"mermaidjs", "native"}

// mermaidNative reports whether golib's Mermaid draws the preview's diagrams: the user's choice.
func (h *Host) mermaidNative() bool { return h.prefs.mermaid == "native" }

// sourceAsked reports whether a terminal's previews are to show a diagram's source: image previews
// turned off (View › Image previews), whatever engine File › Preview option chooses. A window draws
// natively all the same.
func (h *Host) sourceAsked() bool { return !h.native() && !h.prefs.images }

// drawsNatively is what learns, off the loop, whether golib's Mermaid draws a diagram here: a
// window's engine, or a terminal's.
func (h *Host) drawsNatively() func(string) error {
	if h.native() {
		return nativeDiagram
	}
	return cellDiagram
}

// setMermaidEngine is File › Preview option: "native" or "mermaidjs".
func (h *Host) setMermaidEngine(name string) {
	if !slices.Contains(mermaidEngines, name) {
		h.say(h.trf("autodoc.notice.preview.no_mermaid_engine_native_or_mermaidjs", strconv.Quote(name)))
		return
	}
	if name == h.prefs.mermaid {
		return
	}
	h.setPref(prefMermaid, name, func(p *prefs) { p.mermaid = name })
	h.mermaidEngineChanged()
}

// mermaidEngineChanged has the open HTML pane draw its diagrams by the engine chosen, and says it.
func (h *Host) mermaidEngineChanged() {
	if h.htmlPaneShown() {
		if v, ok := h.htmlView(); ok {
			h.paneDiagrams(v)
			h.renderHTMLPane()
		}
	}
	drawn := "autodoc.notice.preview.drawn_by_mermaid_js"
	if h.prefs.mermaid == "native" {
		drawn = "autodoc.notice.preview.drawn_natively_no_zoom"
	}
	h.say(h.trf("autodoc.notice.preview.mermaid_diagrams", h.tr(drawn)))
}

// previewing is the image preview open (View › Diagram's): the page it renders, as wide as its
// diagram and at most diagramMaxHeight tall, and its zoom step.
type previewing struct {
	page []byte
	zoom int
	// background is the page's colour, what the render cuts below and beside it
	background string
}

// diagramMaxHeight bounds a diagram's page, in pixels: rendered as wide as it is, a window as tall as
// a long page's would take the browser seconds.
const diagramMaxHeight = 4096

// showDiagram shows a Mermaid block: an image where it can be, else its source, saying why.
func (h *Host) showDiagram(source string) {
	// mermaid.js draws it, as an image, in a terminal and a GUI window alike. Where File › Preview
	// option is the native engine, or where no image can be shown, golib's Mermaid draws it
	// natively (in a window's pixels, a terminal's characters) where golib draws its type; else
	// mermaid.js, else its source. A terminal whose image previews are off shows the source, as
	// asked, whichever engine is chosen (sourceAsked). Whether golib draws it is learned off the
	// loop: a newer preview, or the dialog closed, meanwhile withdraws this one (its ticket), and an
	// engine or a preference changed meanwhile is the one it is drawn by
	tk := h.ticket(&h.previewGen, TicketLocal)
	if h.sourceAsked() {
		h.showDiagramImage(source, "") // no image: the source, saying image previews are off
		return
	}
	draws := h.drawsNatively()
	if h.mermaidNative() {
		do(h, func(context.Context) error { return draws(source) }, func(err error) {
			switch {
			case !tk.Current():
			case h.sourceAsked():
				h.showDiagramImage(source, "")
			case !h.mermaidNative():
				h.showDiagramImage(source, "")
			case err == nil:
				h.showDiagramNative(source, "")
			default:
				h.showDiagramImage(source, h.trf("autodoc.text.preview.not_drawn_natively", err.Error()))
			}
		})
		return
	}
	ok, why := h.imageMode()
	if ok {
		h.showDiagramImage(source, "")
		return
	}
	do(h, func(context.Context) error { return draws(source) }, func(err error) {
		switch {
		case !tk.Current():
		case h.sourceAsked():
			h.showDiagramImage(source, "")
		case err == nil:
			h.showDiagramNative(source, why)
		default:
			h.showDiagramText(source, h.trf("autodoc.text.preview.diagram_source_not_drawn_natively", why, err.Error()))
		}
	})
}

// showDiagramImage shows source as an image drawn by mermaid.js in a headless browser, where one
// can, else its source, saying why; declined is said with why, "" for nothing more.
func (h *Host) showDiagramImage(source, declined string) {
	ok, why := h.imageMode()
	if declined != "" {
		why = declined + "; " + why
	}
	if !ok {
		h.showDiagramText(source, h.trf("autodoc.text.preview.diagram_source", why))
		return
	}
	theme := h.exportTheme()
	page, err := export.DiagramPage(source, theme)
	if err != nil {
		h.showDiagramText(source, h.trf("autodoc.text.preview.diagram_source", err.Error()))
		return
	}
	h.diagramImage = true
	h.diagramHelpText = h.trf("autodoc.text.preview.image_drawn_offline_by_mermaid_in_the_theme_scro", mermaid.Version, theme)
	if declined != "" {
		h.diagramHelpText = h.trf("autodoc.text.preview.image_declined_drawn_offline_by_mermaid", declined, mermaid.Version, theme)
	}
	h.set("App.diagramTextShown", false)
	h.set("App.diagramNativeShown", false)
	h.set("App.diagramImageShown", true)
	h.set("App.diagramDrawn", true)
	h.set("App.diagramHelp", h.diagramHelpText)
	h.open("diagram")
	h.imagePreview = previewing{page: page, zoom: zoomDefault, background: export.Background(theme)}
	h.renderPreview(true, func(why string) {
		if declined != "" {
			why = declined + "; " + why
		}
		h.showDiagramText(source, h.trf("autodoc.text.preview.diagram_source", why))
	})
}

// showDiagramNative shows source drawn natively, by golib's Mermaid in the dialog's HTML view (a
// window's pixels, a terminal's characters), where golib draws its type: chosen (why is ""), or
// where mermaid.js's image can't be shown (why).
func (h *Host) showDiagramNative(source, why string) {
	theme := h.exportTheme()
	page, err := export.DiagramPage(source, theme)
	if err != nil {
		h.showDiagramText(source, h.trf("autodoc.text.preview.diagram_source", err.Error()))
		return
	}
	help := h.tr("autodoc.text.preview.drawn_natively_file_preview_option_no_zoom_scrol")
	if why != "" {
		help = h.trf("autodoc.text.preview.drawn_natively_as_mermaid_js_is_not_scroll_arrow", why)
	}
	h.diagramImage, h.diagramHelpText = false, help
	h.set("App.diagramTextShown", false)
	h.set("App.diagramImageShown", false)
	h.set("App.diagramNativeShown", true)
	h.set("App.diagramDrawn", true)
	h.set("App.diagramHelp", h.diagramHelpText)
	h.open("diagram")
	if v, ok := componentUnder[*htmlview.HTMLView](h.p, "diagram"); ok {
		v.SetHTML(page)
	}
}

// showDiagramText shows the diagram's source (or a diagnostic), with help saying why it is not an
// image.
func (h *Host) showDiagramText(text, help string) {
	h.diagramImage, h.diagramHelpText = false, help
	h.set("App.diagramImageShown", false)
	h.set("App.diagramNativeShown", false)
	h.set("App.diagramText", text)
	h.set("App.diagramTextShown", true)
	h.set("App.diagramDrawn", false)
	h.set("App.diagramHelp", help)
	h.open("diagram")
}

// renderPreview renders the open preview's page into its Image at its zoom, once the Image has its
// cells; fallback is the preview's, for an image that cannot be had. A fresh page drops the picture
// the Image has, another diagram's, so the spinner shows until it is drawn; a zoom of the same page
// keeps it until the new one comes, and keeps the part of it shown where it was.
func (h *Host) renderPreview(fresh bool, fallback func(why string)) {
	pv := h.imagePreview
	h.withImageCells("diagram", fallback, func(tk Ticket, img *htmlview.Image, cols, rows int) {
		shown, w, ht := img.Scroll()
		if fresh {
			img.Clear()
			w, ht = 0, 0
		}
		rasterize := h.rasterizer()
		// a spinner while the page renders (a headless browser takes a moment), where the Image has
		// no picture
		img.SetLoadingText(h.tr("autodoc.text.preview.drawing_the_diagram"))
		img.SetLoading(true)
		// a page taller than a terminal shows an image (widget.MaxImagePixels), a tall diagram zoomed
		// in, is cut into strips here, off the loop: whole, it shows nothing at all
		do(h, func(ctx context.Context) answerOf[htmlview.Strips] {
			png, err := rasterize(ctx, pv.page, htmlview.Page{Width: cols * htmlview.CellPixelsW,
				MinHeight: rows * htmlview.CellPixelsH, MaxHeight: diagramMaxHeight, Scale: zooms[pv.zoom], Wide: true, Background: pv.background})
			if err != nil {
				return answerOf[htmlview.Strips]{err: err}
			}
			strips, err := htmlview.SplitPNG(png)
			return answerOf[htmlview.Strips]{v: strips, err: err}
		}, func(a answerOf[htmlview.Strips]) {
			if !tk.Current() {
				return // closed, or another preview since
			}
			img.SetLoading(false)
			if a.err != nil {
				fallback(h.trf("autodoc.text.preview.the_image_failed", a.err.Error()))
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

// zoomPreview is Zoom in (step 1) and Zoom out (-1) in the image preview: the diagram rendered
// again at the next scale, the dialog as it was.
func (h *Host) zoomPreview(step int) {
	pv := &h.imagePreview
	if pv.page == nil || !h.diagramImage {
		return
	}
	z := min(max(pv.zoom+step, 0), len(zooms)-1)
	if z == pv.zoom {
		h.say(h.tr(map[bool]string{true: "autodoc.notice.preview.zoom_is_at_its_largest", false: "autodoc.notice.preview.zoom_is_at_its_smallest"}[step > 0]))
		return
	}
	pv.zoom = z
	h.say(h.trf("autodoc.notice.fonts.zoom", int(zooms[z]*100+0.5)))
	fallback := func(why string) { h.say(h.trf("autodoc.notice.preview.zoom", why)) }
	h.renderPreview(false, fallback)
}

// withImageCells runs fn once the Image in the dialog (main.qml's id for it) has been laid out with
// a usable area (minImageCols × minImageRows), with its cells. An area still unusable at imageWait
// (a screen too small, a layout that never gave it cells) runs fallback instead, with why: a
// preview always ends as an image or as its fallback, never blank
// (docs/decisions/search.md#outline-breadcrumbs-and-previews). A screen resized to a usable size
// before then renders the image. A new preview or a closed dialog drops both.
func (h *Host) withImageCells(dialog string, fallback func(why string), fn func(tk Ticket, img *htmlview.Image, cols, rows int)) {
	tk := h.ticket(&h.previewGen, TicketLocal)
	deadline := time.Now().Add(imageWait)
	var try func()
	try = func() {
		if !tk.Current() {
			return
		}
		cols, rows := 0, 0
		img, ok := imageUnder(h.p, dialog)
		if ok {
			cols, rows = img.Cells()
			if cols >= minImageCols && rows >= minImageRows {
				fn(tk, img, cols, rows)
				return
			}
		}
		if time.Now().After(deadline) {
			if cols > 0 && rows > 0 {
				fallback(h.trf("autodoc.text.preview.the_screen_leaves_the_image_cells_too_small_to_r", cols, rows))
			} else {
				fallback(h.tr("autodoc.text.preview.the_preview_s_image_area_was_never_laid_out"))
			}
			return
		}
		h.after(20*time.Millisecond, try)
	}
	h.after(0, try)
}

// imageUnder is the Image in the subtree of the node with id: a dialog's own ids are its file's,
// out of the program's reach, so the dialog's Image is found by its type.
func imageUnder(p *tuidecl.Program, id string) (*htmlview.Image, bool) {
	return componentUnder[*htmlview.Image](p, id)
}

// componentUnder is the first T in the subtree of the node id names, breadth first: a dialog's
// content, whose ids are its own.
func componentUnder[T any](p *tuidecl.Program, id string) (T, bool) {
	var zero T
	root, ok := p.Tree().NodeByID(id)
	if !ok {
		return zero, false
	}
	queue := []decl.NodeID{root}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		if c, ok := p.Adapter().Component(n); ok {
			if t, ok := c.(T); ok {
				return t, true
			}
		}
		queue = append(queue, p.Tree().Children(n)...)
	}
	return zero, false
}

// lastDiagramImage reports whether the Mermaid preview is in image mode (not its fallback).
func (h *Host) lastDiagramImage() bool { return h.diagramImage }

// previewClosed is a preview dialog closing: what it was waiting on is dropped.
func (h *Host) previewClosed() { h.previewGen++ }
