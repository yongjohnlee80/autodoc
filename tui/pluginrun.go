package tui

import (
	"context"
	"errors"
	"fmt"
	"maps"
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
	float *widget.Float // a modal dialog's; nil for a card, which is a panel, and a service
	box   *widget.Box
	view  *pluginView // nil for a service, which has no surface

	ready, closing bool
	// endReason is the run's end, sent in its plugin.close (protocol 7): set once, by the first stop
	// (stopFor, or stopPlugins as AutoDoc quits), never changed by a later one
	endReason string
	hidden    bool // Esc hid it (esc = "hide"), or a card's panel closed: the plugin runs, told plugin.hide
	opened    bool // plugin.open sent: at the dialog's first layout, or a service's start
	focused   bool // a card that holds the keys (plugin.focus true was sent)
	// active are the ids of the ongoing notices this run posted and has not ended (protocol 8):
	// acceptNotice's, on the loop; endNotices ends them when the run stops
	active      map[string]struct{}
	focusOnOpen bool // a card started by its command: it takes the keys at its first layout
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
	if !ok && !e.m.Service() {
		return nil, errors.New("the screen is not ready")
	}
	var card *pluginView
	if e.m.Card() {
		pl.fixCards()
		if c := pl.cards[e.m.Name]; c != nil {
			card = c.view
		}
		if card == nil {
			return nil, errors.New("it is a card, and its panel comes with AutoDoc's next start")
		}
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
	if e.m.Service() {
		r.openAt(0, 0) // no surface to lay out: open it now
		return r, nil
	}

	if card != nil { // its panel is opened by what started it, once it is running (showCard)
		card.attach(r)
		return r, nil
	}
	r.view = &pluginView{run: r, pl: pl, w: e.m.Dialog.Width, h: e.m.Dialog.Height.Rows, fill: e.m.Dialog.Height.Pct > 0}
	r.box = widget.NewBox(r.view, widget.WithTitle(r.title(e.label())), widget.WithBorder(style.BorderRounded),
		widget.WithStyle(style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)))
	opts := []widget.FloatOption{widget.WithModal(e.m.Modal()), widget.WithAnchor(pluginAnchors[pl.placement(e)])}
	if pct := e.m.Dialog.Height.Pct; pct > 0 {
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
	o := plugin.Open{Protocol: r.e.m.Protocol, Width: w, Height: h, Theme: r.pl.pluginTheme()}
	if r.e.m.Protocol >= 7 {
		// a copy: the slot is the loop's, and the run's link must never hold it
		o.Session = maps.Clone(r.pl.sessions[r.e.m.Name])
	}
	r.send(plugin.MethodOpen, plugin.OpenParams(o))
	early := r.early
	r.early = nil
	for _, n := range early {
		r.send(n.method, n.params)
	}
	time.AfterFunc(r.handshake, func() {
		r.pl.c.Post(func() {
			if !r.ready && !r.closing {
				r.close(r.pl.c.Trf("autodoc.notice.pluginrun.did_not_answer_in", r.e.m.Name, r.handshake))
			}
		})
	})
}

// title is a modal dialog's title, saying what Esc does. A card's panel is headed by its title alone.
func (r *pluginRun) title(t string) string {
	if r.e.m.Dialog.Esc == "hide" {
		return r.pl.c.Trf("autodoc.text.pluginrun.esc_hides", t)
	}
	return r.pl.c.Trf("autodoc.text.pluginrun.esc_closes", t)
}

// hide is Esc under esc = "hide": the dialog goes, the plugin runs on, told so. A card's panel
// closes, and its plugin is told so as the panel goes (cardClosed).
func (r *pluginRun) hide() {
	if r.e.m.Card() {
		r.pl.hideCard(r)
		return
	}
	if r.hidden || r.closing {
		return
	}
	r.hidden = true
	r.float.Hide()
	r.send(plugin.MethodHide, plugin.EmptyParams())
}

// show brings the dialog back: its Plugins menu entry, chosen again. A card's panel opens, and its
// plugin is told so as it does (cardOpened).
func (r *pluginRun) show() {
	if r.e.m.Card() {
		r.pl.showCard(r, false)
		return
	}
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
		r.close(r.pl.c.Trf("autodoc.notice.pluginrun.is_not_reading_what_it_is_sent", r.e.m.Name))
	}
}

// sendDocument sends d, as it can be sent: whole, or too large for the link (plugin.FitDocumentAt),
// in the form the plugin's protocol has (byte offsets from 4 on).
func (r *pluginRun) sendDocument(d plugin.Document) {
	p := r.e.m.Protocol
	r.send(plugin.MethodDocument, plugin.DocumentParamsAt(p, plugin.FitDocumentAt(p, d)))
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
				r.pl.c.Post(func() {
					r.close(r.pl.c.Trf("autodoc.notice.pluginrun.stopped_answering", r.e.m.Name, firstLine(err.Error())))
				})
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
		if !r.readyIn.Load() || r.e.m.Service() { // a service has nothing to draw into
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
		if t, err := plugin.ReadTitle(params); err == nil && !r.e.m.Service() {
			r.pl.c.Post(func() { r.setTitle(t) })
		}
	case plugin.MethodSession:
		// decoded and bounded here, on the link's goroutine; the slot is written on the loop, by
		// acceptSession, for this run only if it is still the plugin's live run
		values, err := plugin.ReadSession(params)
		if err != nil || r.e.m.Protocol < 7 {
			r.log.note("host.session refused: " + sessionRefusal(err)) // never a value: a slot holds secrets
			return
		}
		r.pl.c.Post(func() { r.pl.acceptSession(r, values) })
	case plugin.MethodNotify:
		// decoded and bounded here, on the link's goroutine; posted on the loop by acceptNotice, for
		// this run only if it is still the plugin's live run
		n, err := plugin.ReadNotice(params)
		if err != nil || r.e.m.Protocol < 8 {
			r.log.note("host.notify refused: " + noticeRefusal(err, r.e.m.Protocol)) // the rule, never the message
			return
		}
		r.pl.c.Post(func() { r.pl.acceptNotice(r, n) })
	case plugin.MethodHostClose:
		// a card asks its panel to close, as Esc closes it: it hides, and runs on
		r.pl.c.Post(func() {
			if r.e.m.Card() {
				r.hide()
			} else {
				r.close("")
			}
		})
	case plugin.MethodCall:
		r.hostCall(params)
	case plugin.MethodTable, plugin.MethodRows, plugin.MethodTableEnd:
		r.onTable(method, params)
	}
}

// setTitle is host.title: a modal dialog's title, with what Esc does, or a card panel's heading.
func (r *pluginRun) setTitle(t string) {
	if r.closing {
		return
	}
	if r.e.m.Card() {
		r.pl.c.Set("App."+cardPanelID(r.e.m.Name)+"Title", t)
		return
	}
	r.box.SetTitle(r.title(t))
}

// readied is host.ready: the plugin's protocol, which must be the one its manifest says, and
// plugin.open told it.
func (r *pluginRun) readied(p int, err error) {
	switch {
	case r.closing || r.ready:
	case err != nil:
		r.close(r.pl.c.Trf("autodoc.notice.pluginrun.answered_wrongly", r.e.m.Name, err.Error()))
	case p != r.e.m.Protocol:
		r.close(r.pl.c.Trf("autodoc.notice.pluginrun.speaks_protocol_its_manifest_says", r.e.m.Name, p, r.e.m.Protocol))
	default:
		r.ready = true
	}
}

// exitedUnasked is the process gone: when nothing asked it to go, its dialog closes, saying why.
func (r *pluginRun) exitedUnasked() {
	if r.closing {
		return
	}
	why := r.pl.c.Trf("autodoc.notice.pluginrun.exited", r.e.m.Name)
	var ee *exec.ExitError
	if errors.As(r.exitErr, &ee) {
		why = r.pl.c.Trf("autodoc.notice.pluginrun.exited_2", r.e.m.Name, ee.ProcessState)
	}
	r.close(why)
}

// close closes the dialog at once and stops the plugin behind it: plugin.close, then SIGTERM after
// pluginGrace, then SIGKILL after pluginTermGrace, to its process group. why, when set, is a
// failure (CloseFailure), a sentence naming the plugin in the UI's language: a toast says it, with the last line the plugin wrote to its log; without
// one, it is a close as a dialog's (CloseClosed: a modal Esc, its own host.close).
func (r *pluginRun) close(why string) {
	reason := plugin.CloseClosed
	if why != "" {
		reason = plugin.CloseFailure
	}
	r.stopFor(reason, why)
}

// stopFor is the run's one stop transition, with its reason: the first stop sets closing (so every
// session write the run sends from now is dropped, acceptSession) and freezes the reason, then
// starts the shutdown; a later stop does nothing.
func (r *pluginRun) stopFor(reason, why string) {
	if r.closing {
		return
	}
	r.closing = true
	r.endReason = reason
	r.pl.endNotices(r) // before the run leaves pl.running: a replacement's notices are its own
	if r.focused {
		r.pl.c.Keep(r.pl.c.View("editor", "forceActiveFocus"))
	}
	if r.e.m.Card() && r.view != nil {
		r.pl.hideCard(r)
		r.view.detach(r)
		r.pl.c.Set("App."+cardPanelID(r.e.m.Name)+"Title", r.e.label())
	}
	if r.float != nil {
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
	r.pl.tableOwnerGone(r)
	go r.shutdown()
	if why != "" {
		msg := why // the plugin's name and what became of it, a sentence
		if last := r.log.last(); last != "" {
			msg = r.pl.c.Trf("autodoc.notice.pluginrun.text", why, last)
		}
		r.pl.c.Notify(msg)
	}
}

// shutdown stops the process, bounded; it is safe from any goroutine, and once.
func (r *pluginRun) shutdown() {
	r.shutOnce.Do(func() {
		defer close(r.shutDone)
		params := plugin.EmptyParams()
		if r.e.m.Protocol >= 7 {
			params = plugin.CloseParams(r.endReason)
		}
		r.out.push(outNote{plugin.MethodClose, params})
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
		// the quit's stop transition, as stopFor's: fenced and its reason frozen before the
		// shutdown, so no session write is accepted after, and the slots can go
		if !r.closing {
			r.closing, r.endReason = true, plugin.CloseQuit
			pl.endNotices(r)
		}
		go r.shutdown()
	}
	pl.sessions = nil
	for _, r := range pl.running {
		<-r.shutDone
	}
	pl.running = map[string]*pluginRun{}
}

// acceptSession is a session slot's one writer, on the loop: a write from a run that is stopping, or
// is no longer the plugin's live run (replaced by an update, removed), is dropped, so an old process
// cannot overwrite its replacement's slot or bring back a removed plugin's.
func (pl *plugins) acceptSession(r *pluginRun, values map[string]string) {
	if r.closing || pl.running[r.e.key()] != r {
		return
	}
	if pl.sessions == nil {
		pl.sessions = map[string]map[string]string{}
	}
	pl.sessions[r.e.m.Name] = values
}

// acceptNotice is a run's notices' one writer, on the loop, behind acceptSession's fence: a run
// stopping, or no longer the plugin's live run, posts nothing, so a notice queued before a stop
// cannot bring back a toast or touch its replacement's. Only active ids are kept: one ended is
// forgotten, and an ongoing one past MaxActiveNotices is refused.
func (pl *plugins) acceptNotice(r *pluginRun, n plugin.Notice) {
	if r.closing || pl.running[r.e.key()] != r {
		return
	}
	switch {
	case n.Ongoing:
		if _, ok := r.active[n.ID]; !ok && len(r.active) >= plugin.MaxActiveNotices {
			r.log.note(fmt.Sprintf("host.notify refused: more than %d ongoing notices", plugin.MaxActiveNotices))
			return
		}
		if r.active == nil {
			r.active = map[string]struct{}{}
		}
		r.active[n.ID] = struct{}{}
	case n.ID != "":
		delete(r.active, n.ID)
	}
	pl.c.NotifyAs(pluginNoticeNS(r.e.m.Name), n.ID, n.Message, n.Ongoing)
}

// endNotices ends each ongoing notice the run left, "<plugin>: stopped", once: it empties active.
// The stop transitions call it as they set closing (stopFor, stopPlugins), on the loop.
func (pl *plugins) endNotices(r *pluginRun) {
	for id := range r.active {
		pl.c.NotifyAs(pluginNoticeNS(r.e.m.Name), id, pl.c.Trf("autodoc.notice.pluginrun.stopped", r.e.label()), false)
	}
	r.active = nil
}

// pluginNoticeNS is a plugin's notices' namespace: a plugin's name has no ':' (pluginmanifest.go),
// so no plugin's ids reach another's or the host's.
func pluginNoticeNS(name string) string { return "plugin:" + name }

// noticeRefusal is why a host.notify was refused, without its message.
func noticeRefusal(err error, protocol int) string {
	if err != nil {
		return err.Error()
	}
	return fmt.Sprintf("the manifest's protocol %d is before 8", protocol)
}

// sessionRefusal is why a host.session was refused, without any of its values.
func sessionRefusal(err error) string {
	switch {
	case err == nil:
		return "the plugin's manifest says a protocol before 7"
	case errors.Is(err, plugin.ErrSessionBounds):
		return "over the slot's bounds"
	}
	return "malformed"
}
