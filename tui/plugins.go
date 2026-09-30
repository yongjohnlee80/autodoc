package tui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/BurntSushi/toml"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/decl/themes"
	"github.com/yongjohnlee80/golib/tui/style"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// PLUGINS (ADR 0209) — a plugin is a directory in the plugins folder with a plugin.toml. A dialog
// plugin is a program the Plugins menu starts: the TUI draws a dialog over the page holding the
// plugin's latest frame, and passes it every key but Esc, which closes it. The two speak package
// plugin's protocol over the plugin's stdin and stdout.
//
// The dialog is the host's: the plugin only draws into it. A plugin that exits, fails to answer, or
// speaks another protocol costs its dialog and a toast, and nothing else.

// Plugins is where the TUI finds plugins, and what it tells them.
type Plugins struct {
	Dir    string // the plugins folder: one directory per plugin ("" for none)
	LogDir string // each plugin's stderr goes to <LogDir>/<name>.log
	Socket string // the daemon's socket, told to a plugin as AUTODOC_SOCKET
}

// The deadlines of a plugin's life (variables, so a test can shorten them before it starts one).
var (
	pluginHandshake = 5 * time.Second // plugin.open to host.ready
	pluginGrace     = 2 * time.Second // plugin.close to its exit, before SIGTERM
	pluginTermGrace = 1 * time.Second // SIGTERM to its exit, before SIGKILL
)

const (
	pluginLogCap = 1 << 20 // a plugin's log, per start
	pluginQueue  = 256     // what the host sends, waiting for the plugin to read it
)

// The dialog's size when the manifest names none, and the bounds on one it names.
const (
	pluginWidth, pluginHeight = 40, 20
	pluginMinW, pluginMinH    = 10, 4
	pluginMaxW, pluginMaxH    = 300, 100
)

var pluginName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// manifest is a plugin.toml.
type manifest struct {
	Name     string   `toml:"name"`
	Title    string   `toml:"title"`
	Kind     string   `toml:"kind"`
	Protocol int      `toml:"protocol"`
	Command  []string `toml:"command"`
	Dialog   struct {
		Width  int `toml:"width"`
		Height int `toml:"height"`
	} `toml:"dialog"`
}

// pluginEntry is a plugin found in the folder: its directory, its manifest, and why it cannot run
// ("" when it can).
type pluginEntry struct {
	dir    string
	m      manifest
	reason string
}

func (e pluginEntry) key() string {
	if e.reason == "" {
		return e.m.Name
	}
	return "dir:" + filepath.Base(e.dir)
}

func (e pluginEntry) label() string {
	title := e.m.Title
	if title == "" {
		title = e.m.Name
	}
	if title == "" {
		title = filepath.Base(e.dir)
	}
	if e.reason != "" {
		return title + " — " + e.reason
	}
	return title
}

// discoverPlugins reads every <dir>/*/plugin.toml, in the order of the directories' names. One
// that cannot run is listed with its reason, never left out; a directory with no plugin.toml is not
// a plugin.
func discoverPlugins(dir string) []pluginEntry {
	if dir == "" {
		return nil
	}
	ds, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []pluginEntry
	seen := map[string]string{}
	for _, d := range ds {
		if !d.IsDir() {
			continue
		}
		e := readPlugin(filepath.Join(dir, d.Name()))
		if e == nil {
			continue
		}
		if e.reason == "" {
			if first, dup := seen[e.m.Name]; dup {
				e.reason = fmt.Sprintf("the name %q is %s's already", e.m.Name, first)
			} else {
				seen[e.m.Name] = d.Name()
			}
		}
		out = append(out, *e)
	}
	return out
}

// readPlugin reads one plugin's directory; nil when it holds no plugin.toml.
func readPlugin(dir string) *pluginEntry {
	path := filepath.Join(dir, "plugin.toml")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	e := &pluginEntry{dir: dir}
	if _, err := toml.DecodeFile(path, &e.m); err != nil {
		e.reason = "plugin.toml: " + firstLine(err.Error())
		return e
	}
	m := &e.m
	switch {
	case !pluginName.MatchString(m.Name):
		e.reason = fmt.Sprintf("name %q: want lower-case letters, digits and -", m.Name)
	case m.Kind != "dialog":
		e.reason = fmt.Sprintf("kind %q: this AutoDoc runs dialog plugins", m.Kind)
	case m.Protocol != plugin.Protocol:
		e.reason = fmt.Sprintf("protocol %d; this AutoDoc speaks %d", m.Protocol, plugin.Protocol)
	case len(m.Command) == 0 || m.Command[0] == "":
		e.reason = "no command"
	}
	if m.Dialog.Width == 0 {
		m.Dialog.Width = pluginWidth
	}
	if m.Dialog.Height == 0 {
		m.Dialog.Height = pluginHeight
	}
	m.Dialog.Width = min(max(m.Dialog.Width, pluginMinW), pluginMaxW)
	m.Dialog.Height = min(max(m.Dialog.Height, pluginMinH), pluginMaxH)
	return e
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// loadPlugins lists the folder's plugins in the Plugins menu.
func (h *Host) loadPlugins() {
	h.pluginList = discoverPlugins(h.pluginOpt.Dir)
	rows := make([]tuidecl.Row, len(h.pluginList))
	for i, e := range h.pluginList {
		rows[i] = tuidecl.Row{"key": e.key(), "label": e.label(), "enabled": e.reason == ""}
	}
	h.pluginRows.Reset(rows)
}

// openPlugin is a Plugins menu entry: the plugin's dialog, started, or brought forward when open.
func (h *Host) openPlugin(key string) {
	if r := h.running[key]; r != nil {
		r.float.Show()
		return
	}
	i := slices.IndexFunc(h.pluginList, func(e pluginEntry) bool { return e.key() == key })
	if i < 0 {
		h.notify("no plugin " + key)
		return
	}
	e := h.pluginList[i]
	if e.reason != "" {
		h.notify(e.label())
		return
	}
	r, err := h.startPlugin(e)
	if err != nil {
		h.notify(fmt.Sprintf("%s did not start: %v", e.m.Name, err))
		return
	}
	h.running[key] = r
}

// pluginTheme is the theme a plugin is sent: the one the screen wears.
func (h *Host) pluginTheme() plugin.Theme {
	colors, err := themes.Values(h.theme)
	if err != nil {
		colors = map[string]string{}
	}
	return plugin.Theme{Name: h.theme, Colors: colors}
}

// themePlugins tells the open plugins the theme changed.
func (h *Host) themePlugins() {
	t := h.pluginTheme()
	for _, r := range h.running {
		r.send(plugin.MethodTheme, plugin.ThemeParams(t))
	}
}

// ---------------------------------------------------------------- one plugin, running

// pluginRun is one plugin's process, its link and its dialog. Loop-owned unless marked.
type pluginRun struct {
	h     *Host
	e     pluginEntry
	cmd   *exec.Cmd
	link  *plugin.Link
	ctx   context.Context
	stop  context.CancelFunc
	out   chan outNote // to the sender goroutine
	log   *pluginLog
	float *widget.Float
	box   *widget.Box
	view  *pluginView

	ready, closing bool
	// the deadlines, as they were when it started
	handshake, grace, termGrace time.Duration

	// the reader's side, under mu: the latest frame, resolved, and whether host.ready came
	mu      sync.Mutex
	frame   [][]pluginCell
	readyIn atomic.Bool
	posted  atomic.Bool // a repaint is posted and not yet run

	exited   chan struct{} // closed when the process has exited
	exitErr  error
	shutOnce sync.Once
	shutDone chan struct{}
}

type outNote struct {
	method string
	params []any
}

type pluginCell struct {
	text string
	st   style.Style
}

// startPlugin starts e's process, its link and its dialog, and sends plugin.open.
func (h *Host) startPlugin(e pluginEntry) (*pluginRun, error) {
	host, ok := h.p.Overlay()
	if !ok {
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
	log := openPluginLog(h.pluginOpt.LogDir, e.m.Name)
	cmd := exec.Command(e.m.Command[0], e.m.Command[1:]...)
	cmd.Dir = e.dir
	cmd.Env = append(os.Environ(), fmt.Sprintf("AUTODOC_PLUGIN_PROTOCOL=%d", plugin.Protocol),
		"AUTODOC_PLUGIN_DIR="+e.dir, "AUTODOC_SOCKET="+h.pluginOpt.Socket)
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

	r := &pluginRun{h: h, e: e, cmd: cmd, log: log, out: make(chan outNote, pluginQueue),
		exited: make(chan struct{}), shutDone: make(chan struct{}),
		handshake: pluginHandshake, grace: pluginGrace, termGrace: pluginTermGrace}
	r.ctx, r.stop = context.WithCancel(h.ctx)
	go func() {
		r.exitErr = cmd.Wait()
		log.Close()
		close(r.exited)
		h.p.Post(r.exitedUnasked)
	}()
	r.link, err = plugin.NewLink(r.ctx, plugin.FileConn(hostIn, hostOut), r.onNote)
	if err != nil {
		r.stop()
		go r.shutdown()
		return nil, err
	}
	go r.sender()

	r.view = &pluginView{run: r, w: e.m.Dialog.Width, h: e.m.Dialog.Height, sentW: e.m.Dialog.Width, sentH: e.m.Dialog.Height}
	r.box = widget.NewBox(r.view, widget.WithTitle(e.label()+" · Esc closes"), widget.WithBorder(style.BorderRounded),
		widget.WithStyle(style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)))
	r.float = widget.NewFloat(r.box, widget.WithModal(true), widget.WithAnchor(widget.Center))
	host.Attach(r.float)
	r.float.Show()

	r.send(plugin.MethodOpen, plugin.OpenParams(plugin.Open{Protocol: plugin.Protocol,
		Width: e.m.Dialog.Width, Height: e.m.Dialog.Height, Theme: h.pluginTheme()}))
	time.AfterFunc(r.handshake, func() {
		h.p.Post(func() {
			if !r.ready && !r.closing {
				r.close(fmt.Sprintf("did not answer in %s", r.handshake))
			}
		})
	})
	return r, nil
}

// send queues a notification for the plugin. A plugin that has not read pluginQueue of them is not
// answering: its dialog closes.
func (r *pluginRun) send(method string, params []any) {
	if r.closing {
		return
	}
	select {
	case r.out <- outNote{method, params}:
	default:
		r.close("is not reading what it is sent")
	}
}

// sender writes the queue to the plugin, off the loop; a write the plugin does not take within the
// link's timeout closes its dialog. It ends when the queue is closed, after sending what it holds.
func (r *pluginRun) sender() {
	for n := range r.out {
		if err := r.link.Notify(r.ctx, n.method, n.params); err != nil {
			if r.ctx.Err() == nil {
				r.h.p.Post(func() { r.close("stopped answering: " + firstLine(err.Error())) })
			}
			for range r.out { // drain, so a close's send never blocks
			}
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
		r.h.p.Post(func() { r.readied(p, err) })
	case plugin.MethodFrame:
		if !r.readyIn.Load() {
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
			r.h.p.Post(func() {
				r.posted.Store(false)
				r.view.MarkDirty()
			})
		}
	case plugin.MethodTitle:
		if t, err := plugin.ReadTitle(params); err == nil {
			r.h.p.Post(func() { r.box.SetTitle(t + " · Esc closes") })
		}
	case plugin.MethodHostClose:
		r.h.p.Post(func() { r.close("") })
	}
}

// readied is host.ready: the plugin's protocol, which must be this AutoDoc's.
func (r *pluginRun) readied(p int, err error) {
	switch {
	case r.closing || r.ready:
	case err != nil:
		r.close("answered wrongly: " + err.Error())
	case p != plugin.Protocol:
		r.close(fmt.Sprintf("speaks protocol %d; this AutoDoc speaks %d", p, plugin.Protocol))
	default:
		r.ready = true
		r.readyIn.Store(true)
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
	if r.float.Shown() {
		r.float.Hide()
	}
	if host, ok := r.h.p.Overlay(); ok {
		host.Detach(r.float)
	}
	if r.h.running[r.e.key()] == r {
		delete(r.h.running, r.e.key())
	}
	go r.shutdown()
	if why != "" {
		msg := r.e.m.Name + " " + why
		if last := r.log.last(); last != "" {
			msg += ": " + last
		}
		r.h.notify(msg)
	}
}

// shutdown stops the process, bounded; it is safe from any goroutine, and once.
func (r *pluginRun) shutdown() {
	r.shutOnce.Do(func() {
		defer close(r.shutDone)
		select {
		case r.out <- outNote{plugin.MethodClose, plugin.EmptyParams()}:
		default:
		}
		close(r.out)
		select {
		case <-r.exited:
		case <-time.After(r.grace):
			signalGroup(r.cmd, false)
			select {
			case <-r.exited:
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
func (h *Host) stopPlugins() {
	for _, r := range h.running {
		go r.shutdown()
	}
	for _, r := range h.running {
		<-r.shutDone
	}
	h.running = map[string]*pluginRun{}
}

// resolveFrame reads a frame's colours: an unknown one is the default, and counted.
func resolveFrame(rows []plugin.Row) (out [][]pluginCell, bad int) {
	out = make([][]pluginCell, len(rows))
	for i, row := range rows {
		cells := make([]pluginCell, 0, len(row))
		for _, run := range row {
			st, n := runStyle(run.Style)
			bad += n
			cells = append(cells, pluginCell{text: run.Text, st: st})
		}
		out[i] = cells
	}
	return out, bad
}

// runStyle is a run's style over the dialog's own: an empty colour, or "default", leaves the
// dialog's.
func runStyle(s plugin.Style) (style.Style, int) {
	st := style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)
	bad := 0
	for _, c := range []struct {
		v  string
		fg bool
	}{{s.FG, true}, {s.BG, false}} {
		if c.v == "" || strings.EqualFold(strings.TrimSpace(c.v), "default") {
			continue
		}
		col, err := style.ParseColor(c.v)
		if err != nil {
			bad++
			continue
		}
		if c.fg {
			st = st.Foreground(col)
		} else {
			st = st.Background(col)
		}
	}
	return st.Bold(s.Bold).Italic(s.Italic).Underline(s.Underline), bad
}

// ---------------------------------------------------------------- the dialog's body

// pluginView is the dialog's inside: the plugin's latest frame, and its keys.
type pluginView struct {
	widget.Base
	run          *pluginRun
	w, h         int // the size the manifest asks for
	sentW, sentH int // the size last told to the plugin: plugin.open's, then each resize's
	laidW, laidH int
}

var _ tuicore.Focusable = (*pluginView)(nil)

func (v *pluginView) AcceptsFocus() bool { return true }

// Layout is the manifest's size, within the screen's; the plugin is told a size it did not ask for.
func (v *pluginView) Layout(c tuicore.Constraints) tuicore.Size {
	w := min(max(v.w, c.MinW), c.MaxW)
	h := min(max(v.h, c.MinH), c.MaxH)
	v.laidW, v.laidH = w, h
	if ctx := v.Context(); ctx != nil && (w != v.sentW || h != v.sentH) {
		ctx.AfterLayout("plugin-resize", v.resized) // Layout itself sends nothing
	}
	return tuicore.Size{W: w, H: h}
}

func (v *pluginView) resized() {
	if v.laidW == v.sentW && v.laidH == v.sentH {
		return
	}
	v.sentW, v.sentH = v.laidW, v.laidH
	v.run.send(plugin.MethodResize, plugin.ResizeParams(v.laidW, v.laidH))
}

func (v *pluginView) Render(s tuicore.Surface) {
	sz := s.Size()
	base := style.New().Background(style.TokenPanel).Foreground(style.TokenForeground)
	s.Fill(tuicore.Rect{W: sz.W, H: sz.H}, " ", base)
	v.run.mu.Lock()
	frame := v.run.frame
	v.run.mu.Unlock()
	for y, row := range frame {
		if y >= sz.H {
			break
		}
		x := 0
		for _, c := range row {
			for _, ch := range c.text {
				if x >= sz.W {
					break
				}
				g := string(ch)
				s.SetCell(x, y, g, c.st)
				x += max(s.StringWidth(g), 1)
			}
		}
	}
}

// HandleEvent: Esc closes; every other key is the plugin's.
func (v *pluginView) HandleEvent(ev tuicore.Event) bool {
	e, ok := ev.(tuicore.KeyEvent)
	if !ok || e.Kind == tuicore.KeyRelease {
		return false
	}
	if e.Code == tuicore.KeyEscape && e.Mods.Chord() == 0 {
		v.run.close("")
		return true
	}
	if k, ok := pluginKey(e); ok {
		v.run.send(plugin.MethodKey, plugin.KeyParams(k))
	}
	return true
}

var pluginKeyNames = map[rune]string{
	tuicore.KeyUp: "Up", tuicore.KeyDown: "Down", tuicore.KeyLeft: "Left", tuicore.KeyRight: "Right",
	tuicore.KeyEnter: "Enter", tuicore.KeyTab: "Tab", tuicore.KeyBackspace: "Backspace",
	tuicore.KeyDelete: "Delete", tuicore.KeyInsert: "Insert", tuicore.KeyHome: "Home", tuicore.KeyEnd: "End",
	tuicore.KeyPageUp: "PageUp", tuicore.KeyPageDown: "PageDown",
	tuicore.KeyF1: "F1", tuicore.KeyF2: "F2", tuicore.KeyF3: "F3", tuicore.KeyF4: "F4", tuicore.KeyF5: "F5",
	tuicore.KeyF6: "F6", tuicore.KeyF7: "F7", tuicore.KeyF8: "F8", tuicore.KeyF9: "F9", tuicore.KeyF10: "F10",
	tuicore.KeyF11: "F11", tuicore.KeyF12: "F12",
}

// pluginKey is a key as the protocol names it.
func pluginKey(e tuicore.KeyEvent) (plugin.Key, bool) {
	k := plugin.Key{Ctrl: e.Mods&tuicore.ModCtrl != 0, Alt: e.Mods&tuicore.ModAlt != 0, Shift: e.Mods&tuicore.ModShift != 0}
	switch {
	case pluginKeyNames[e.Code] != "":
		k.Key = pluginKeyNames[e.Code]
	case e.Text != "":
		k.Key, k.Text = e.Text, e.Text
	case e.Code >= ' ' && e.Code < tuicore.KeyInsert && utf8.ValidRune(e.Code):
		k.Key = string(e.Code) // a chord, Ctrl+a: no text
	default:
		return plugin.Key{}, false
	}
	return k, true
}

// ---------------------------------------------------------------- a plugin's log

// pluginLog is a plugin's stderr, kept in <LogDir>/<name>.log (truncated at each start, capped at
// pluginLogCap), with its last line for the toast of a failure. No LogDir keeps the last line only.
type pluginLog struct {
	mu       sync.Mutex
	f        *os.File
	written  int
	lastLine string
	partial  []byte
}

func openPluginLog(dir, name string) *pluginLog {
	l := &pluginLog{}
	if dir != "" {
		if err := os.MkdirAll(dir, 0o700); err == nil {
			l.f, _ = os.OpenFile(filepath.Join(dir, name+".log"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
		}
	}
	return l
}

func (l *pluginLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil && l.written < pluginLogCap {
		n, _ := l.f.Write(b[:min(len(b), pluginLogCap-l.written)])
		l.written += n
	}
	l.partial = append(l.partial, b...)
	for {
		i := strings.IndexByte(string(l.partial), '\n')
		if i < 0 {
			break
		}
		if line := strings.TrimSpace(string(l.partial[:i])); line != "" {
			l.lastLine = line
		}
		l.partial = l.partial[i+1:]
	}
	if len(l.partial) > 4096 {
		l.partial = l.partial[len(l.partial)-4096:]
	}
	return len(b), nil
}

// note writes the host's own line into the plugin's log; the last line stays the plugin's.
func (l *pluginLog) note(s string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f != nil && l.written < pluginLogCap {
		n, _ := io.WriteString(l.f, "autodoc: "+s+"\n")
		l.written += n
	}
}

// last is the last line the plugin wrote (or an unfinished one), "" for none.
func (l *pluginLog) last() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if p := strings.TrimSpace(string(l.partial)); p != "" {
		return p
	}
	return l.lastLine
}

func (l *pluginLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.f == nil {
		return nil
	}
	err := l.f.Close()
	l.f = nil
	return err
}
