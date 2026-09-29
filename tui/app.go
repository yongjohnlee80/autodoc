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
//	search.go     the workspace's notes, as the pickers filter them
//	pickers.go    the search, open and new-note pickers, and their previews
//	explorer.go   the explorer: every workspace's folders and notes, as a tree
//	links.go      the backlinks panel
//	panels.go     the panels' drawers, and moving between them and the page
//	progress.go   the daemon's work left, on the status line
//	prefs.go      the preferences, kept in the daemon's store
//	providers.go  Preferences › Embedding: the providers, their models and usage
//	theme.go      View › Theme: switching the theme import at runtime
//	help.go       the help and the about text
package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"github.com/yongjohnlee80/golib/highlight"
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
	picker, backlinks, workspaces, managed   *tuidecl.ListModel
	hits, newList, providers, providerModels *tuidecl.ListModel
	explorer                                 *tuidecl.TreeListModel
	explorerPaths                            map[string][]string // by workspace: its notes, once listed (explorer.go)

	// the pickers (pickers.go): what each lists, the latest answers winning, and the search's words
	hitList             []hit
	pickerRows, newRows []string
	searchSeq           uint64
	previewSeq          uint64
	marks               marks
	openAt              int // where the next note opened puts the cursor, a byte offset; -1 for its start

	// the preferences (prefs.go), and the panels open now (panels.go)
	prefs     prefs
	connected bool // to the daemon: the status line shows while not (prefs.go)
	panelOpen map[string]bool

	// the embedding providers (providers.go)
	providerList                    []providerRow
	activeProvider, editingProvider string
	removingProvider                string
	formKind                        int
	formHasKey                      bool // the provider the form edits has a key sealed
	modelList                       []string
	providerSeq                     uint64

	// the workspace in use, and the epoch: moved by a switch and a reconnect, so an answer asked
	// under another workspace or connection is dropped (workspace.go)
	ws       string
	entered  bool // ws was entered on this connection's listing
	remember func(name string)
	where    string // the status line's "autodoc <version> · <workspace>", or why there is none
	wsList   []wsInfo
	epoch    uint64
	listSeq  uint64 // numbers the note list's loads; the latest wins (search.go)
	notesAll []string

	// the workspace manager: the workspace a rename or a delete was started on
	renaming, removing string

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
	// Workspace is the one to open (autodoc --ui <name>); "" opens the first served.
	Workspace string
	// Remember, when set, is told each workspace the TUI enters, so the next start can open it.
	Remember func(name string)
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
	h := &Host{session: session, ctx: ctx, cancel: cancel, about: opt.About, dev: opt.Dev,
		ws: opt.Workspace, remember: opt.Remember,
		picker:         tuidecl.NewListModel("key", "path"),
		hits:           tuidecl.NewListModel("key", "path", "section"),
		newList:        tuidecl.NewListModel("key", "path"),
		providers:      tuidecl.NewListModel("key", "use", "name", "kind", "model", "apiKey"),
		providerModels: tuidecl.NewListModel("key", "name"),
		explorer:       tuidecl.NewTreeListModel("key", "label"),
		explorerPaths:  map[string][]string{},
		openAt:         -1,
		prefs:          defaultPrefs(),
		panelOpen:      map[string]bool{},
		backlinks:      tuidecl.NewListModel("key", "label"),
		workspaces:     tuidecl.NewListModel("key", "label"),
		managed:        tuidecl.NewListModel("key", "name", "state", "root")}
	h.explorer.OnFetch = h.fetchExplorer
	return h
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
		tuidecl.Highlighters(highlight.Definition{Name: "Markdown (search)", Highlighter: h.searchHighlighter()}),
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
