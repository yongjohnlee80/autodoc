//go:build gui

package tui

import (
	"bytes"
	"context"
	"image/png"
	"sync"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// THE HTML PANE'S DIAGRAMS, DRAWN BY MERMAID.JS: each <pre class="mermaid"> of the pane's page is
// drawn as View diagram and the terminal's previews draw it, by the vendored mermaid.js in a
// headless browser (export.DiagramPage), in the export theme, and placed in the page as a picture.
// Where no browser is found, or one fails, golib's own Mermaid draws it natively.

// mermaidJS is the pane's Diagrammer: a diagram's picture by mermaid.js, laid out once per source,
// width, scale and theme, and shared by every request for it.
type mermaidJS struct {
	h      *Host
	native guiwidget.Diagrammer // without a browser, or when it fails
	mu     sync.Mutex
	done   map[mermaidKey]mermaidPic
	jobs   map[mermaidKey][]func()
}

type mermaidKey struct {
	src, theme string
	width      int
	scale      float32
}

// mermaidPic is a diagram drawn: its picture and size, or why there is none.
type mermaidPic struct {
	img  *gui.Image
	size gui.Size
	err  error
}

func newMermaidJS(h *Host) *mermaidJS {
	return &mermaidJS{h: h, native: guiwidget.Diagrams(nil), done: map[mermaidKey]mermaidPic{}, jobs: map[mermaidKey][]func(){}}
}

// Diagram answers req: Ready with mermaid.js's picture once it is drawn, Pending while it is, and
// golib's native answer without a browser or after a failure. Called on the loop.
func (m *mermaidJS) Diagram(ctx context.Context, req guiwidget.DiagramRequest, ready func()) guiwidget.DiagramAnswer {
	if req.Lang != "mermaid" || !m.h.browserFound() {
		return m.native.Diagram(ctx, req, ready)
	}
	k := mermaidKey{src: req.Src, theme: m.h.exportTheme(), width: int(req.Width), scale: max(req.Scale, 1)}
	m.mu.Lock()
	if p, ok := m.done[k]; ok {
		m.mu.Unlock()
		if p.err != nil {
			return m.native.Diagram(ctx, req, ready)
		}
		return guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: picture(p, req.Width), Size: fitted(p.size, req.Width)}
	}
	waiting := len(m.jobs[k]) > 0
	m.jobs[k] = append(m.jobs[k], func() {
		if ctx.Err() == nil {
			ready()
		}
	})
	m.mu.Unlock()
	if !waiting {
		go m.draw(k, m.h.rasterizer())
	}
	return guiwidget.DiagramAnswer{State: guiwidget.Pending, Size: gui.Size{W: req.Width, H: req.Width * 0.5}}
}

// draw lays k's diagram out by mermaid.js in the browser, off the loop, and wakes its waiters.
func (m *mermaidJS) draw(k mermaidKey, rasterize func(context.Context, []byte, widget.Page) ([]byte, error)) {
	p := mermaidPic{}
	page, err := export.DiagramPage(k.src, k.theme)
	if err == nil {
		var data []byte
		data, err = rasterize(context.Background(), page, widget.Page{Width: 1, MaxHeight: diagramMaxHeight,
			Scale: float64(k.scale), Wide: true, Background: export.Background(k.theme)})
		if err == nil {
			img, derr := png.Decode(bytes.NewReader(data))
			if err = derr; err == nil {
				b := img.Bounds().Size()
				p.img, p.size = gui.NewImage(img), gui.Size{W: float32(b.X) / k.scale, H: float32(b.Y) / k.scale}
			}
		}
	}
	p.err = err
	m.mu.Lock()
	m.done[k] = p
	waiters := m.jobs[k]
	delete(m.jobs, k)
	m.mu.Unlock()
	for _, w := range waiters {
		w()
	}
}

// fitted is size no wider than width, in proportion.
func fitted(size gui.Size, width float32) gui.Size {
	if width <= 0 || size.W <= width {
		return size
	}
	return gui.Size{W: width, H: size.H * width / size.W}
}

// picture draws p's image at its size fitted to width.
func picture(p mermaidPic, width float32) gui.View {
	size, px := fitted(p.size, width), p.img.Size()
	return gui.ViewFunc(func(c gui.Canvas) {
		c.DrawImage(p.img, gui.Rect{W: size.W, H: size.H}, gui.Rect{W: px.W, H: px.H})
	})
}

// paneDiagrams has the pane's page draw its diagrams by mermaid.js (mermaidJS), once.
func (h *Host) paneDiagrams(v *widget.HTMLView) {
	m, ok := h.paneMermaid.(*mermaidJS)
	if !ok {
		m = newMermaidJS(h)
		h.paneMermaid = m
	}
	if d, _ := v.Diagrams().(*mermaidJS); d != m {
		widget.WithDiagrams(m)(v)
	}
}
