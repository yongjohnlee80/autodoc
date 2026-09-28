// Package tui is AutoDoc's terminal UI (`autodoc --ui`): its screen written in QML, over the daemon's
// RPC API. It follows the auto family's TUI/QML structure (the KB convention
// tui-qml-program-structure): the document owns structure, the host owns behaviour.
//
// The host is spread over one file per concern:
//
//	app.go        the Host, and New, which assembles it
//	modules.go    the QML the program ships, and the modules that offer it
//	state.go      the App singleton's state: what the document reads
//	commands.go   the App singleton's commands: what the document invokes
//	work.go       background work, and bringing its result back
//	client.go     the session: the only path to the daemon
//	session.go    connecting, and reconnecting when the connection ends
//	workspace.go  the workspaces, and switching between them
//	notes.go      the note in the editor: opening, saving, conflicts, unsaved changes
//	search.go     the notes pane: every note, or a search's hits
//	links.go      the backlinks pane
//	progress.go   the daemon's work left, on the status line
//	theme.go      View › Theme: switching the theme import at runtime
//	help.go       the help and the about text
package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// Host is the program behind qml/main.qml.
type Host struct {
	p       *tuidecl.Program
	session *Session
	editor  *widget.Editor
	ctx     context.Context
	cancel  context.CancelFunc

	about string
	// layoutSrc is the layout as last loaded, dev the -dev directory ("" for none), theme the one
	// the screen wears (theme.go).
	layoutSrc []byte
	dev       string
	theme     string

	// the models the document binds
	results, picker, backlinks, workspaces *tuidecl.ListModel

	// the workspace in use, and the epoch: moved by a switch and a reconnect, so an answer asked
	// under another workspace or connection is dropped (workspace.go)
	ws       string
	where    string // the status line's "autodoc <version> · <workspace>", or why there is none
	wsList   []wsInfo
	epoch    uint64
	listSeq  uint64 // numbers the notes pane's loads; the latest wins (search.go)
	notesAll []string

	note    note     // the note in the editor (notes.go)
	prog    progress // the daemon's work left (progress.go)
	message string   // the last message, shown beside the progress

	mu   sync.Mutex
	errs []error // handler errors, returned by Run
}

// Options are what New needs from the program around it.
type Options struct {
	// About is the build and location text of Help › About.
	About string
	// App are options for the tui.App: the backend, above all.
	App []tuicore.AppOption
	// Dev is a directory holding main.qml, dialogs/ and views/ (this package's qml/). Set, the QML is
	// read from there and followed as it is edited.
	Dev string
	// Layout replaces main.qml; nil means the embedded one. A test runs another theme's import.
	Layout []byte
}

// New builds the program over session. Nothing runs, and nothing dials, until Run.
func New(session *Session, opt Options) (*Host, error) {
	h := newHost(session, opt)
	p, err := tuidecl.NewProgram(h.options(opt)...)
	if err != nil {
		return nil, err
	}
	if err := h.attach(p); err != nil {
		return nil, errors.Join(err, p.Tree().Destroy())
	}
	return h, nil
}

func newHost(session *Session, opt Options) *Host {
	ctx, cancel := context.WithCancel(context.Background())
	return &Host{session: session, ctx: ctx, cancel: cancel, about: opt.About, dev: opt.Dev,
		results:    tuidecl.NewListModel("key", "path"),
		picker:     tuidecl.NewListModel("key", "path"),
		backlinks:  tuidecl.NewListModel("key", "label"),
		workspaces: tuidecl.NewListModel("key", "label")}
}

// attach binds the host to the program built from its options (by New, or by a test running the
// same options), finds the one widget it reaches into, and connects once the program runs.
func (h *Host) attach(p *tuidecl.Program) error {
	h.p = p
	var ok bool
	if h.editor, ok = tuidecl.FindAs[*widget.Editor](p, "editor"); !ok {
		return errors.New("main.qml declares no Editor with id: editor")
	}
	p.Post(h.start)
	return nil
}

// options are everything the program is. New builds from them, the QML check lints them, and every
// test runs them: a program assembled twice is two programs.
func (h *Host) options(opt Options) []tuidecl.ProgramOption {
	var opts []tuidecl.ProgramOption
	var src []byte
	if opt.Dev != "" {
		src, _ = os.ReadFile(filepath.Join(opt.Dev, "main.qml"))
		files := os.DirFS(opt.Dev)
		opts = append(modulesFrom(files), tuidecl.Layout(files, "main.qml"),
			tuidecl.HotReload(tuidecl.OnReloadError(func(err error) { h.setStatus(err.Error()) })))
	} else {
		src = opt.Layout
		if src == nil {
			src = layout
		}
		opts = append(modulesFrom(qmlFiles), tuidecl.LayoutSource("main.qml", src))
	}
	h.layoutSrc = src
	h.theme = themeOf(src)
	return append(opts,
		tuidecl.Sources(h.state()),
		tuidecl.Handlers(h.commands()),
		tuidecl.ErrorSink(h.keep),
		tuidecl.AppOptions(opt.App...))
}

// Run runs the program until it quits; the session and every background call end with it.
func (h *Host) Run(ctx context.Context) error {
	defer h.cancel()
	defer h.session.Close()
	err := h.p.Run(ctx)
	h.mu.Lock()
	defer h.mu.Unlock()
	return errors.Join(append([]error{err}, h.errs...)...)
}

// keep records a handler's error for Run, and shows it.
func (h *Host) keep(err error) {
	if err == nil {
		return
	}
	h.mu.Lock()
	h.errs = append(h.errs, err)
	h.mu.Unlock()
}
