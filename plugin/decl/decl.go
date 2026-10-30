// Package decl runs a golib QML program (tui/decl) as an AutoDoc card or dialog
// (docs/decisions/plugins.md#qml-cards-run-in-the-plugin): the program runs inside the plugin on a backend of its own, and every frame it draws
// is sent to AutoDoc as host.frame. The host never sees QML; the wire is unchanged.
//
//	c := decl.New(decl.Options{
//		Program: []tuidecl.ProgramOption{
//			tuidecl.LayoutSource("card.qml", layout),
//			tuidecl.Singleton("hello", "1.0", "App"),
//			tuidecl.Sources(map[string]any{"App.status": "ready"}),
//		},
//	})
//	_ = plugin.Serve(ctx, c)
//
// The layout binds its palette to Theme, which this package declares (module autodoc.plugin 1.0)
// and keeps to AutoDoc's theme:
//
//	import tui 1.0
//	import autodoc.plugin 1.0
//	Window { palette.window: Theme.app.window; palette.windowText: Theme.app.windowText … }
//
// The program's state changes on its loop only: Options.Setup before the first frame, then
// Card.Post. Esc never reaches the program: AutoDoc keeps it to close or hide the card.
package decl

import (
	"context"
	"errors"
	"log"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// Options are a Card's program and its start.
type Options struct {
	Program []tuidecl.ProgramOption // the layout, singletons, sources, commands, handlers
	// Setup runs once, on the Handler goroutine inside Open, after the program is built and before
	// it runs: no loop exists yet, so it may Set sources and reach widgets directly. The first
	// frame shows what it set. An error is a failed Open. nil: no setup.
	Setup func(p *tuidecl.Program, host *plugin.Peer) error
	Modal bool // a modal dialog: always focused, since AutoDoc sends it no plugin.focus
}

// The Card's states. Only the Handler goroutine moves idle → failed | running; the Run goroutine
// moves running → stopped.
const (
	idle    = iota // before Open
	failed         // the program could not be built or set up: nothing runs
	running        // Run's goroutine launched
	stopped        // Run returned
)

// closeWait bounds Close's wait for Run to return, under the two seconds AutoDoc gives a plugin.
const closeWait = 1500 * time.Millisecond

// Card is a QML program as a plugin.Handler. It is also a Focuser, a PointerHandler and a Hider;
// a plugin wanting commands, the document feed or tables embeds *Card and adds them.
type Card struct {
	o Options

	mu     sync.Mutex
	st     int
	prog   *tuidecl.Program
	runErr error

	ctx     context.Context // made in Open, before Run: Close can always cancel it
	cancel  context.CancelFunc
	done    chan struct{} // closed when Run returns: any number of waiters, none needed
	closing atomic.Bool
	be      *frameBackend
	known   []string // the Theme.* sources declared at the start: the only ones a theme can set
}

// New is a Card that runs the program o describes when AutoDoc opens it.
func New(o Options) *Card { return &Card{o: o} }

func (c *Card) state() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.st
}

// Program is the running program; nil before Open and after a failed one.
func (c *Card) Program() *tuidecl.Program {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prog
}

// Err is what Run returned; nil while it runs.
func (c *Card) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.runErr
}

// Open is plugin.open: the program is built at the size and in the theme AutoDoc gives, set up,
// and run on a goroutine of its own.
func (c *Card) Open(p *plugin.Peer, o plugin.Open) { c.open(p, p, o) }

// open is Open with any host; peer is what Setup is handed (nil in a test without a link).
func (c *Card) open(to host, peer *plugin.Peer, o plugin.Open) {
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.done = make(chan struct{})
	sources, known := themeSources(o.Theme)
	c.known = known
	c.be = newFrameBackend(to, o.Width, o.Height, c.ctx, colorsOf(o.Theme))
	opts := append(slices.Clone(c.o.Program),
		tuidecl.Singleton(themeModule, themeVersion, "Theme"),
		tuidecl.Sources(sources),
		tuidecl.AppOptions(tui.WithBackend(c.be)))
	prog, err := tuidecl.NewProgram(opts...)
	if err == nil && c.o.Setup != nil {
		if err = c.o.Setup(prog, peer); err != nil {
			err = errors.Join(err, prog.Tree().Destroy()) // built, never to run: release its providers
		}
	}
	if err != nil {
		c.mu.Lock()
		c.st, c.runErr = failed, err
		c.mu.Unlock()
		c.cancel()
		close(c.done)
		c.be.failure(err)
		_ = to.Close() // AutoDoc answers with plugin.close; Close returns at once
		return
	}
	c.mu.Lock()
	c.prog, c.st = prog, running
	c.mu.Unlock()
	if !c.o.Modal {
		c.be.post(tui.FocusEvent{Gained: false, Terminal: true}) // a card opens without the keys
	}
	go func() {
		err := prog.Run(c.ctx)
		c.mu.Lock()
		c.st, c.runErr = stopped, err
		c.mu.Unlock()
		c.cancel()
		close(c.done)
		if !c.closing.Load() { // the program ended itself: a quit in the layout, or an error
			if err != nil && !errors.Is(err, context.Canceled) {
				c.be.failure(err)
			}
			_ = to.Close()
		}
	}()
}

// Close is plugin.close, or the host gone: the program is stopped, and Close returns once Run has
// (cancelling is not finishing: only done says Run returned). A teardown that hangs is cut off at
// closeWait and logged, below the two seconds AutoDoc waits before it stops the process.
func (c *Card) Close() {
	c.closing.Store(true)
	switch c.state() {
	case idle:
		return
	case failed:
		_ = c.be.Stop() // the backend was made; no App will stop it
		return
	}
	c.cancel()
	// NOTE: a deadline the code can name: closeWait bounds the wait for Run to return after its
	// context is cancelled, so a user's teardown that hangs cannot hold the plugin past the two
	// seconds AutoDoc gives it. Run's return itself is waited on (done), not polled.
	t := time.NewTimer(closeWait)
	defer t.Stop()
	select {
	case <-c.done:
	case <-t.C:
		log.Printf("plugin/decl: Run did not return within %s of Close", closeWait)
	}
}

// Post queues fn for the program's loop and returns true while it runs; before Open, after a failed
// Open and once stopped it drops fn and returns false. A queued fn runs only if the Card is still
// live when the loop reaches it: one queued before Close is skipped after it.
func (c *Card) Post(fn func()) bool {
	c.mu.Lock()
	prog, st := c.prog, c.st
	c.mu.Unlock()
	if st != running {
		return false
	}
	prog.Post(whileLive(c.ctx, fn))
	return true
}

// whileLive is fn, run only if ctx is not done when it runs: checked then, not when it was queued.
func whileLive(ctx context.Context, fn func()) func() {
	return func() {
		if ctx.Err() == nil {
			fn()
		}
	}
}

// live is whether the Card has a backend that takes events: opened, and not failed.
func (c *Card) live() bool { st := c.state(); return st == running }

// Key is plugin.key, as the terminal would have sent it.
func (c *Card) Key(k plugin.Key) {
	if c.live() {
		c.be.post(keyEvent(k))
	}
}

// Resize is plugin.resize: the size Size answers moves, and the App takes it on its loop; the grid
// follows in FlushSized (§4.8).
func (c *Card) Resize(w, h int) {
	if c.live() {
		c.be.setWant(w, h)
		c.be.post(tui.ResizeEvent{W: max(w, 0), H: max(h, 0)})
	}
}

// Pointer is plugin.pointer: a left press (a press and its release), or wheel steps.
func (c *Card) Pointer(at plugin.Pointer) {
	if c.live() {
		for _, e := range mouseEvents(at) {
			c.be.post(e)
		}
	}
}

// Focus is plugin.focus: the card took AutoDoc's keys, or gave them back, as a terminal's focus.
func (c *Card) Focus(focused bool) {
	if c.live() && !c.o.Modal {
		c.be.post(tui.FocusEvent{Gained: focused, Terminal: true})
	}
}

// Theme is plugin.theme: the Theme.* sources move, and every palette bound to them follows.
func (c *Card) Theme(t plugin.Theme) {
	if !c.live() {
		return
	}
	c.be.setColors(colorsOf(t))
	values := themeValues(t, c.known)
	c.Post(func() {
		if err := c.Program().SetMany(values); err != nil {
			log.Printf("plugin/decl: theme %q: %v", t.Name, err)
		}
	})
}

// Hide and Show change nothing: the program runs on, unseen, as a hidden card does.
func (c *Card) Hide() {}
func (c *Card) Show() {}

var (
	_ plugin.Handler        = (*Card)(nil)
	_ plugin.Focuser        = (*Card)(nil)
	_ plugin.PointerHandler = (*Card)(nil)
	_ plugin.Hider          = (*Card)(nil)
)
