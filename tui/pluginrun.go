package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
	"github.com/yongjohnlee80/golib/tui/style"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// A RUNNING PLUGIN — one started plugin's process: its dialog or service, the protocol both ways,
// and its closing (plugins.go).

// The deadlines of a plugin's life (variables, so a test can shorten them before it starts one).
var (
	pluginHandshake = 5 * time.Second // plugin.open to host.ready
	pluginGrace     = 2 * time.Second // plugin.close to its exit, before SIGTERM
	pluginTermGrace = 1 * time.Second // SIGTERM to its exit, before SIGKILL
)

// pluginRun is one plugin's process, its link and its dialog. Loop-owned unless marked.
type pluginRun struct {
	pl    *plugins
	e     pluginEntry
	cmd   *exec.Cmd
	link  *plugin.Link
	ctx   context.Context
	stop  context.CancelFunc
	out   *outQueue // to the sender goroutine
	log   *pluginLog
	float *widget.Float // nil for a service, which has no surface
	box   *widget.Box
	view  *pluginView

	ready, closing bool
	hidden         bool // Esc hid it (esc = "hide"): the plugin runs, told plugin.hide
	opened         bool // plugin.open sent: at the dialog's first layout, or a service's start
	focused        bool // a card that holds the keys (plugin.focus true was sent)
	focusOnOpen    bool // a card started by its command: it takes the keys at its first layout
	// early is what was sent before plugin.open: a command or the feed, for a plugin just started.
	// plugin.open goes first, then these, in order.
	early []outNote
	// the deadlines, as they were when it started
	handshake, grace, termGrace time.Duration

	// the reader's side, under mu: the latest frame, resolved, and whether host.ready came
	mu      sync.Mutex
	frame   [][]pluginCell
	readyIn atomic.Bool
	posted  atomic.Bool // a repaint is posted and not yet run
	// the host.calls waiting for their answers, by number, and how many were refused unanswered
	// (a number reused) or answered busy, under callMu (plugincall.go)
	callMu      sync.Mutex
	calls       map[int64]bool
	dupes, busy int

	exited   chan struct{} // closed when the process has exited
	exitErr  error
	shutOnce sync.Once
	shutDone chan struct{}
}

// startPlugin starts e's process, its link and its surface, and sends plugin.open: a dialog's at
// its first layout, a service's at once.
func (pl *plugins) startPlugin(e pluginEntry) (*pluginRun, error) {
	host, ok := pl.c.Overlay()
	if !ok && !e.m.service() {
		return nil, errors.New("the screen is not ready")
	}
	toPlugin, hostOut, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	hostIn, fromPlugin, err := os.Pipe()
	if err != nil {
		toPlugin.Close()
		hostOut.Close()
		return nil, err
	}
	log := openPluginLog(pl.pluginOpt.LogDir, e.m.Name)
	cmd := exec.Command(e.m.Command[0], e.m.Command[1:]...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("AUTODOC_PLUGIN_PROTOCOL=%d", e.m.Protocol),
		"AUTODOC_PLUGIN_DIR="+e.dir, "AUTODOC_SOCKET="+pl.pluginOpt.Socket)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = toPlugin, fromPlugin, log
	cmd.SysProcAttr = pluginProcAttr()
	err = cmd.Start()
	toPlugin.Close() // the child's ends are the child's now
	fromPlugin.Close()
	if err != nil {
		hostIn.Close()
		hostOut.Close()
		log.Close()
		return nil, err
	}

	r := &pluginRun{pl: pl, e: e, cmd: cmd, log: log, out: newOutQueue(),
		exited: make(chan struct{}), shutDone: make(chan struct{}),
		handshake: pluginHandshake, grace: pluginGrace, termGrace: pluginTermGrace}
	r.ctx, r.stop = context.WithCancel(pl.c.Context())
	go func() {
		r.exitErr = cmd.Wait()
		log.Close()
		close(r.exited)
		pl.c.Post(r.exitedUnasked)
	}()
	r.link, err = plugin.NewLink(r.ctx, plugin.FileConn(hostIn, hostOut), r.onNote)
	if err != nil {
		r.stop()
		go r.shutdown()
		return nil, err
	}
	go r.sender()
	if e.m.Feed.Document {
		r.sendDocument(pl.document()) // after plugin.open: the note as it stands when it starts
	}
	if e.m.service() {
		r.openAt(0, 0) // no surface to lay out: open it now
		return r, nil
	}

	r.view = &pluginView{run: r, w: e.m.Dialog.Width, h: e.m.Dialog.Height.rows, fill: e.m.Dialog.Height.pct > 0}
	r.box = widget.NewBox(r.view, widget.WithTitle(r.title(e.label())), widget.WithBorder(style.BorderRounded),
		widget.WithStyle(style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)))
	opts := []widget.FloatOption{widget.WithModal(e.m.modal()), widget.WithAnchor(pluginAnchors[pl.placement(e)])}
	if pct := e.m.Dialog.Height.pct; pct > 0 {
		opts = append(opts, widget.WithSizeFraction(0, pct))
	}
	r.float = widget.NewFloat(r.box, opts...)
	host.Attach(r.float)
	r.float.Show()
	return r, nil
}

// opened is the dialog's first layout: plugin.open, with the size it was laid out at, and the
// handshake's deadline from then.
func (r *pluginRun) openAt(w, h int) {
	r.opened = true
	r.send(plugin.MethodOpen, plugin.OpenParams(plugin.Open{Protocol: r.e.m.Protocol,
		Width: w, Height: h, Theme: r.pl.pluginTheme()}))
	early := r.early
	r.early = nil
	for _, n := range early {
		r.send(n.method, n.params)
	}
	time.AfterFunc(r.handshake, func() {
		r.pl.c.Post(func() {
			if !r.ready && !r.closing {
				r.close(fmt.Sprintf("did not answer in %s", r.handshake))
			}
		})
	})
}

// title is the dialog's title, saying what Esc does.
func (r *pluginRun) title(t string) string {
	if !r.e.m.modal() {
		return t + " · Esc returns the keys"
	}
	if r.e.m.Dialog.Esc == "hide" {
		return t + " · Esc hides"
	}
	return t + " · Esc closes"
}

// hide is Esc under esc = "hide": the dialog goes, the plugin runs on, told so.
func (r *pluginRun) hide() {
	if r.hidden || r.closing {
		return
	}
	r.hidden = true
	r.float.Hide()
	r.send(plugin.MethodHide, plugin.EmptyParams())
}

// show brings the dialog back: its Plugins menu entry, chosen again.
func (r *pluginRun) show() {
	if r.float == nil {
		return
	}
	r.float.Show()
	if r.hidden {
		r.hidden = false
		r.send(plugin.MethodShow, plugin.EmptyParams())
	}
}

// placement is where e's dialog sits: the user's choice among its placements, or its first.
func (pl *plugins) placement(e pluginEntry) string {
	if p := pl.c.Prefs().pluginPlace[e.m.Name]; slices.Contains(e.m.Dialog.Placements, p) {
		return p
	}
	return e.m.Dialog.Placements[0]
}

// send queues a notification for the plugin; before plugin.open it waits for it (early). A plugin
// that has not read pluginQueue of them is not answering: its dialog closes.
func (r *pluginRun) send(method string, params []any) {
	if r.closing {
		return
	}
	if !r.opened && method != plugin.MethodOpen {
		r.early = append(slices.DeleteFunc(r.early, func(n outNote) bool {
			return method == plugin.MethodDocument && n.method == plugin.MethodDocument
		}), outNote{method, params})
		return
	}
	if !r.out.push(outNote{method, params}) {
		r.close("is not reading what it is sent")
	}
}

// sendDocument sends d, as it can be sent: whole, or too large for the link (plugin.FitDocument).
func (r *pluginRun) sendDocument(d plugin.Document) {
	r.send(plugin.MethodDocument, plugin.DocumentParams(plugin.FitDocument(d)))
}

// sender writes the queue to the plugin, off the loop; a write the plugin does not take within the
// link's timeout closes its dialog. It ends when the queue is closed, after sending what it holds.
func (r *pluginRun) sender() {
	for {
		n, ok := r.out.pop()
		if !ok {
			return
		}
		if err := r.link.Notify(r.ctx, n.method, n.params); err != nil {
			if r.ctx.Err() == nil {
				r.pl.c.Post(func() { r.close("stopped answering: " + firstLine(err.Error())) })
			}
			r.out.drop()
			return
		}
	}
}

// onNote is the link's callback, on the link's goroutine: it must not block. A frame is resolved
// here and kept; the loop only paints the latest.
func (r *pluginRun) onNote(method string, params []any) {
	switch method {
	case plugin.MethodReady:
		p, err := plugin.ReadReady(params)
		if err == nil && p == r.e.m.Protocol {
			// here, not on the loop: the plugin's first frame follows at once, on this goroutine
			r.readyIn.Store(true)
		}
		r.pl.c.Post(func() { r.readied(p, err) })
	case plugin.MethodFrame:
		if !r.readyIn.Load() || r.e.m.service() { // a service has nothing to draw into
			return
		}
		rows, dropped, err := plugin.ReadFrame(params)
		if err != nil {
			r.log.note("a frame refused: " + err.Error())
			return
		}
		frame, bad := resolveFrame(rows)
		if dropped+bad > 0 {
			r.log.note(fmt.Sprintf("a frame had %d malformed runs and %d unknown colours", dropped, bad))
		}
		r.mu.Lock()
		r.frame = frame
		r.mu.Unlock()
		if !r.posted.Swap(true) {
			r.pl.c.Post(func() {
				r.posted.Store(false)
				r.view.MarkDirty()
			})
		}
	case plugin.MethodTitle:
		if t, err := plugin.ReadTitle(params); err == nil && !r.e.m.service() {
			r.pl.c.Post(func() { r.box.SetTitle(r.title(t)) })
		}
	case plugin.MethodHostClose:
		r.pl.c.Post(func() { r.close("") })
	case plugin.MethodCall:
		r.hostCall(params)
	}
}

// readied is host.ready: the plugin's protocol, which must be the one its manifest says, and
// plugin.open told it.
func (r *pluginRun) readied(p int, err error) {
	switch {
	case r.closing || r.ready:
	case err != nil:
		r.close("answered wrongly: " + err.Error())
	case p != r.e.m.Protocol:
		r.close(fmt.Sprintf("speaks protocol %d; its manifest says %d", p, r.e.m.Protocol))
	default:
		r.ready = true
	}
}

// exitedUnasked is the process gone: when nothing asked it to go, its dialog closes, saying why.
func (r *pluginRun) exitedUnasked() {
	if r.closing {
		return
	}
	why := "exited"
	var ee *exec.ExitError
	if errors.As(r.exitErr, &ee) {
		why = fmt.Sprintf("exited (%s)", ee.ProcessState)
	}
	r.close(why)
}

// close closes the dialog at once and stops the plugin behind it: plugin.close, then SIGTERM after
// pluginGrace, then SIGKILL after pluginTermGrace, to its process group. why, when set, is a
// failure: a toast says it, with the last line the plugin wrote to its log.
func (r *pluginRun) close(why string) {
	if r.closing {
		return
	}
	r.closing = true
	if r.float != nil {
		if r.focused {
			r.pl.c.Keep(r.pl.c.View("editor", "forceActiveFocus"))
		}
		if r.float.Shown() {
			r.float.Hide()
		}
		if host, ok := r.pl.c.Overlay(); ok {
			host.Detach(r.float)
		}
	}
	if r.pl.running[r.e.key()] == r {
		delete(r.pl.running, r.e.key())
		r.pl.refreshPlugins()
	}
	go r.shutdown()
	if why != "" {
		msg := r.e.m.Name + " " + why
		if last := r.log.last(); last != "" {
			msg += ": " + last
		}
		r.pl.c.Notify(msg)
	}
}

// shutdown stops the process, bounded; it is safe from any goroutine, and once.
func (r *pluginRun) shutdown() {
	r.shutOnce.Do(func() {
		defer close(r.shutDone)
		r.out.push(outNote{plugin.MethodClose, plugin.EmptyParams()})
		r.out.close()
		select {
		case <-r.exited:
			// NOTE: a plugin is asked to close, then given grace to exit before SIGTERM, and termGrace
			// more before SIGKILL: a graceful stop, bounded so a stuck plugin cannot hold the TUI open.
		case <-time.After(r.grace):
			signalGroup(r.cmd, false)
			select {
			case <-r.exited:
			// NOTE: SIGTERM's grace, before SIGKILL ends a plugin that ignores it.
			case <-time.After(r.termGrace):
				signalGroup(r.cmd, true)
				<-r.exited
			}
		}
		r.stop()
		if r.link != nil {
			_ = r.link.Close()
		}
	})
}

// stopPlugins stops every open plugin and waits for them, as the TUI quits: bounded by the close's
// deadlines, since they run at once.
func (pl *plugins) stopPlugins() {
	for _, r := range pl.running {
		go r.shutdown()
	}
	for _, r := range pl.running {
		<-r.shutDone
	}
	pl.running = map[string]*pluginRun{}
}
