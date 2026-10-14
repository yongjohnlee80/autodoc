//go:build gui

package tui

import (
	"bytes"
	"container/list"
	"context"
	"image/png"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/gui"
	guiwidget "github.com/yongjohnlee80/golib/gui/widget"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/export"
)

// THE HTML PANE'S DIAGRAMS, DRAWN BY MERMAID.JS: each <pre class="mermaid"> of the pane's page is
// drawn as View diagram and the terminal's previews draw it, by the vendored mermaid.js in a
// headless browser (export.DiagramPage), in the export theme, and placed in the page as a picture.
// Where no browser is found, or one fails, golib's own Mermaid draws it natively.

// mermaidJS is the pane's Diagrammer: a diagram's picture by mermaid.js, drawn once per source,
// theme and scale and shared by every request for it. The page is rendered as wide as its diagram,
// whatever the pane's width, which only fits the picture: a resize draws nothing again. A drawing
// no request waits for any more is cancelled; the pictures drawn are kept, a few (pictures).
type mermaidJS struct {
	h      *Host
	native guiwidget.Diagrammer // without a browser, or when it fails
	parent context.Context      // the program's: its end cancels every drawing
	now    func() time.Time     // the clock a failure's retry is timed by; tests set it
	mu     sync.Mutex
	done   *pictures
	jobs   map[mermaidKey]*mermaidJob
}

type mermaidKey struct {
	src, theme string
	scale      float32
}

// mermaidJob is a drawing under way and the requests waiting for it, by number.
type mermaidJob struct {
	cancel  context.CancelFunc
	waiters map[int]mermaidWaiter
	next    int
}

// mermaidWaiter is a request waiting: its context, its ready, and the watch that withdraws it when
// its context ends.
type mermaidWaiter struct {
	ctx   context.Context
	ready func()
	stop  func() bool
}

// mermaidPic is a diagram drawn: its picture and size, or why there is none.
type mermaidPic struct {
	img  *gui.Image
	size gui.Size
	err  error
}

func newMermaidJS(h *Host) *mermaidJS {
	parent := h.ctx
	if parent == nil {
		parent = context.Background()
	}
	return &mermaidJS{h: h, native: nativeDiagrams(), parent: parent, now: time.Now,
		done: newPictures(), jobs: map[mermaidKey]*mermaidJob{}}
}

// Diagram answers req: Ready with mermaid.js's picture once it is drawn, Pending while it is, and
// golib's native answer without a browser or after a failure. Called on the loop.
func (m *mermaidJS) Diagram(ctx context.Context, req guiwidget.DiagramRequest, ready func()) guiwidget.DiagramAnswer {
	if req.Lang != "mermaid" || !m.h.browserFound() {
		return m.native.Diagram(ctx, req, ready)
	}
	k := mermaidKey{src: req.Src, theme: m.h.exportTheme(), scale: max(req.Scale, 1)}
	m.mu.Lock()
	if p, ok := m.done.get(k, m.now()); ok {
		m.mu.Unlock()
		if p.err != nil {
			return m.native.Diagram(ctx, req, ready)
		}
		return guiwidget.DiagramAnswer{State: guiwidget.Ready, Pic: picture(p, req.Width), Size: fitted(p.size, req.Width)}
	}
	job := m.jobs[k]
	if job == nil {
		jctx, cancel := context.WithCancel(m.parent)
		job = &mermaidJob{cancel: cancel, waiters: map[int]mermaidWaiter{}}
		m.jobs[k] = job
		go m.draw(jctx, k, job, m.h.rasterizer())
	}
	id := job.next
	job.next++
	job.waiters[id] = mermaidWaiter{ctx: ctx, ready: ready, stop: context.AfterFunc(ctx, func() { m.withdraw(k, job, id) })}
	m.mu.Unlock()
	return guiwidget.DiagramAnswer{State: guiwidget.Pending, Size: gui.Size{W: req.Width, H: req.Width * 0.5}}
}

// withdraw takes a request whose context ended off its drawing; the last one leaving cancels it,
// in the browser or waiting for one.
func (m *mermaidJS) withdraw(k mermaidKey, job *mermaidJob, id int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.jobs[k] != job {
		return // drawn already
	}
	delete(job.waiters, id)
	if len(job.waiters) == 0 {
		job.cancel()
		delete(m.jobs, k)
	}
}

// draw lays k's diagram out by mermaid.js in the browser, off the loop, keeps the picture and
// wakes its waiters. A drawing withdrawn, or the program's end, keeps nothing.
func (m *mermaidJS) draw(ctx context.Context, k mermaidKey, job *mermaidJob, rasterize func(context.Context, []byte, widget.Page) ([]byte, error)) {
	p := mermaidPic{}
	page, err := export.DiagramPage(k.src, k.theme)
	if err == nil {
		var data []byte
		data, err = rasterize(ctx, page, widget.Page{Width: 1, MaxHeight: diagramMaxHeight,
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
	if m.jobs[k] != job || ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	delete(m.jobs, k)
	m.done.put(k, p, m.now())
	waiters := job.waiters
	m.mu.Unlock()
	job.cancel()
	// a request that ended once the drawing was done, before or while the others were woken, is
	// not woken: its watch already ran (stop false), or its context ends just now
	for _, w := range waiters {
		if w.stop() && w.ctx.Err() == nil {
			w.ready()
		}
	}
}

// The pictures kept: the most recently used, up to maxPictures of them and maxPictureBytes of
// pixels. A failure is kept failureFor, so a page painted again does not start a browser each
// time, then drawn again.
const (
	maxPictures     = 32
	maxPictureBytes = 64 << 20
	failureFor      = time.Minute
)

// pictures are the diagrams drawn, the least recently used first to go.
type pictures struct {
	order *list.List // of *pictureEntry, the most recently used at the front
	by    map[mermaidKey]*list.Element
	bytes int
}

type pictureEntry struct {
	k     mermaidKey
	p     mermaidPic
	at    time.Time
	bytes int
}

func newPictures() *pictures { return &pictures{order: list.New(), by: map[mermaidKey]*list.Element{}} }

// get is k's picture, or its failure while it is kept; ok false when there is neither.
func (c *pictures) get(k mermaidKey, now time.Time) (mermaidPic, bool) {
	e, ok := c.by[k]
	if !ok {
		return mermaidPic{}, false
	}
	en := e.Value.(*pictureEntry)
	if en.p.err != nil && now.Sub(en.at) >= failureFor {
		c.remove(e)
		return mermaidPic{}, false
	}
	c.order.MoveToFront(e)
	return en.p, true
}

// put keeps p as k's, dropping the least recently used past the bounds; the newest stays.
func (c *pictures) put(k mermaidKey, p mermaidPic, now time.Time) {
	if e, ok := c.by[k]; ok {
		c.remove(e)
	}
	n := 0
	if p.img != nil {
		px := p.img.Size()
		n = int(px.W) * int(px.H) * 4
	}
	c.by[k] = c.order.PushFront(&pictureEntry{k: k, p: p, at: now, bytes: n})
	c.bytes += n
	for c.order.Len() > maxPictures || c.bytes > maxPictureBytes && c.order.Len() > 1 {
		c.remove(c.order.Back())
	}
}

func (c *pictures) remove(e *list.Element) {
	en := c.order.Remove(e).(*pictureEntry)
	delete(c.by, en.k)
	c.bytes -= en.bytes
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

// paneDiagrams has the pane's page draw its diagrams by the engine File › Preview option chose:
// mermaid.js (mermaidJS), or golib's Mermaid natively. A view given the other one asks it again.
func (h *Host) paneDiagrams(v *widget.HTMLView) {
	m, ok := h.paneMermaid.(*mermaidJS)
	if !ok {
		m = newMermaidJS(h)
		h.paneMermaid = m
	}
	var want guiwidget.Diagrammer = m
	if h.mermaidNative() {
		want = m.native
	}
	if d, _ := v.Diagrams().(guiwidget.Diagrammer); d != want {
		widget.WithDiagrams(want)(v)
	}
}
