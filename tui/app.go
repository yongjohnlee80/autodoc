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
//	files.go      the file in the editor: opening, saving, conflicts, unsaved changes
//	search.go     the workspace's files, as the pickers filter them
//	pickers.go    the search, open and new-file pickers, and their previews
//	explorer.go   the explorer: every workspace's folders and files, as a tree
//	relations.go  the Relations drawer, the jump card and back (SPC l, j, b)
//	outside.go    files outside every workspace: Open file…, New file…
//	panels.go     the panels' drawers, and moving between them and the page
//	progress.go   the daemon's work left, on the status line
//	prefs.go      the preferences, kept in the daemon's store
//	providers.go  Preferences › Embedding: the providers, their models and usage
//	rankers.go    AI models › Ranker Models: the rankers, the one in use, its window and usage
//	theme.go      View › Theme: switching the theme import at runtime
//	help.go       the help and the about text
//	plugins.go    the Plugins menu: the plugins folder, and a plugin's dialog
//	plugininstall.go  adding a plugin from a git URL, and managing the installed ones
package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/highlight"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/outline"
	"github.com/yongjohnlee80/autodoc/core/registrations"
)

// Host is the program behind qml/main.qml.
type Host struct {
	p       *tuidecl.Program
	session *Session
	editor  editorWidget       // main.qml's Editor, terminal or native (editor.go)
	core    *widget.EditorCore // its core: the text, the cursor and the modes
	gui     bool               // in a window (Options.GUI): the theme kept is gui.theme
	ctx     context.Context
	cancel  context.CancelFunc

	about string
	// layoutSrc is the layout as last loaded, dev the -dev directory ("" for none), theme the one
	// the screen wears (theme.go).
	layoutSrc []byte
	dev       string
	theme     string

	// the models the document binds
	picker, relationsModel, workspaces, managed       *tuidecl.ListModel
	outlineList, wsProviders                          *tuidecl.ListModel
	hits, newList, providers, providerModels, rankers *tuidecl.ListModel
	vectors                                           *tuidecl.ListModel // the workspace's models (vectors.go)
	explorer                                          *tuidecl.TreeListModel
	explorerPaths                                     map[string][]string // by workspace: its files, once listed (explorer.go)
	explorerTop                                       []wsInfo            // the workspaces the explorer's top level shows (explorer.go)

	// the pickers (pickers.go): what each lists, the latest answers winning, and the search's words
	hitList             []hit
	pickerRows, newRows []string
	searchSeq           uint64
	searchCancel        context.CancelFunc
	searchQuery         string
	searchOpen          bool
	lastSearchError     string
	searchWaiting       bool
	searchWaitToast     bool
	previewSeq          uint64
	marks               marks
	findMarks           marks                 // the page find's words, as the page's highlighter marks them (find.go)
	findHL              highlight.Highlighter // the page's: Markdown, the find's words marked over it
	textFindHL          highlight.Highlighter
	yamlFindHL          highlight.Highlighter
	openAt              int // where the next file opened puts the cursor, a byte offset; -1 for its start

	// find in a pane (find.go): the last find, and the cursors of the panes it moves
	find       findState
	explorerAt []string // the explorer's row under its cursor, by its keys
	linksAt    int
	// the Relations drawer (relations.go): its rows, whether it shows depth 2, and back's history
	relRows []relRow
	relFor  fileRef // the document relRows are of; the zero fileRef when they are of none
	relDeep bool
	history []fileRef

	// the preferences (prefs.go), and the panels open now (panels.go)
	prefs                          prefs
	prefWrites                     [][2]string // name and value, the first being written (prefs.go)
	connected                      bool        // to the daemon: the status line shows while not (prefs.go)
	mismatchOpen, mismatchRecovery bool
	// the registrations (registrations.go): this binary's, the daemon's, and the kinds a file is
	// read as, which are the daemon's; whether the restart was offered, a mismatch that cannot be
	// offered noted, and a restart for them, or any restart, accepted, this session
	ownTables, daemonTables                registrations.Tables
	kinds                                  kind.Registrations
	registrationOffered, registrationNoted bool
	registrationRestart, restartAccepted   bool
	previewHeld                            bool // the search preview's hit is of a held document
	panelOpen                              map[string]bool
	// the terminal (terminal.go): started once opened; the pane that had the keyboard when it
	// opened; whether its closing gives the keyboard back
	termStarted, termRestore bool
	// the agent terminal (agent.go): where agents work, and the profile running ("" for none)
	agentDir, agentRunning string
	agentConfig            string // the TUI's config, the agent's autodoc's (Options.ConfigPath)
	agentBinPath           string // the autodoc the agent calls; "" is this binary (a test sets it)
	editingAgent           string // the profile the agent form edits, "" adding one
	switchingAgent         string // the profile the switch question would start
	agentRows              *tuidecl.ListModel
	termBefore             string
	// the recent files (recent.go): those listed, in their rows' order
	recentRows  []recentDoc
	recentModel *tuidecl.ListModel

	// the embedding providers (providers.go)
	providerList                    []providerRow
	activeProvider, editingProvider string
	removingProvider                string
	formKind                        int
	formBase, formKey, formModel    string
	formMax                         int64
	formHasKey                      bool // the provider the form edits has a key sealed
	modelList                       []string
	vectorList                      []vectorRow // the workspace's models, as listed (vectors.go)
	purging                         vectorRow   // the model the purge question asks about
	providerSeq                     uint64

	// the ranker models (rankers.go): AI models' tab, the rankers listed, the one in use, the
	// build's own (its model, "" for none), and the row the shared buttons act on
	aiTab                         int
	rankerList                    []rankerRow
	activeRanker, rankerSupplied  string
	editingRanker, removingRanker string
	rankerFormKind                int
	rankerFormHasKey              bool
	rankerCursor                  int
	rankerSeq                     uint64
	hitsRanked                    string // the hits' title's note of their order (rankedTitle)
	// the search's stages (stages.go): the ranker in use, who it is and how its box names it ("" for
	// none), and the stages the boxes last asked for
	stageRankerID, stageRanker string
	stagesSent                 string

	// the workspace in use, and the epoch: moved by a switch and a reconnect, so an answer asked
	// under another workspace or connection is dropped (workspace.go)
	ws string
	// the settings dialog (settings.go): what it was opened on, and the tab it is on
	settings    settingsBase
	settingsTab int
	// dialogSeq numbers the manager dialogs' opens (withCurrent): only the latest shows
	dialogSeq uint64
	// the open file's frontmatter check (frontmatter.go): fmGen numbers the checks, the latest wins
	fmGen         uint64
	fmDiagnostics []fmDiagnostic
	// the event log's cursor (events.go), and whether the next workspace entered keeps the draft
	// a peer's removal of this one left
	evCursor  int64
	keepDraft bool
	// the image previews (preview.go): previewGen numbers them, the latest wins; htmlPreviewPath is
	// the file the HTML preview's image was made from; graphicsOverride replaces the terminal's
	// kitty graphics answer in tests, and rasterizeOverride the headless browser
	previewGen        uint64
	htmlPreviewPath   string
	diagramImage      bool          // the Mermaid preview is an image, not its fallback
	diagramHelpText   string        // what the Mermaid preview's help line says
	imagePreview      previewing    // the image preview open, to zoom it (preview.go)
	htmlPane          htmlPaneState // the native HTML preview beside the editor, under --gui (htmlpane.go)
	graphicsOverride  func() tuicore.Tri
	rasterizeOverride func(context.Context, []byte, widget.Page) ([]byte, error)
	// the editor text's outline (outline.go): outlineGen numbers the refreshes, the latest wins
	outline     *outline.Doc
	outlineGen  uint64
	outlineRows []outline.Heading
	focusSent   time.Time
	entered     bool // ws was entered on this connection's listing
	// a restart under way: the version it stops, and its daemon's process, which the reconnect
	// waits out (restart.go)
	restartFrom string
	restartPID  int64
	remember    func(name string)
	installed   func() (string, error) // Options.Installed
	browser     func(context.Context, string) error
	nativeViews func() bool   // whether the backend draws native views (--gui); tests stand in for a GUI
	docSwitch   func(on bool) // stands in for the editor's SetRenderedDocument in tests; nil: the editor's
	htmlAsk     *htmlOpen     // the HTML file the open question is about (OpenHTML.qml)
	pageDir     string        // the root and base the page's resources load from (htmldoc.go)
	// refusedNoted: the open page's refused resource was said (resourceRefused)
	refusedNoted bool
	// awaitExit waits for a stopped daemon's process to go (waitGone); a test's daemon shares the
	// test's process, so its test waits on the daemon instead
	awaitExit func(ctx context.Context, pid int64) bool
	where     string // the status line's "autodoc <version> · <workspace>", or why there is none
	wsList    []wsInfo
	epoch     uint64
	listSeq   uint64        // numbers the file list's loads; the latest wins (search.go)
	listRetry time.Duration // the delay before the file list's next retry after a failure; 0 after a success
	filesAll  []string

	// the workspace manager: the row under its cursor, the workspace a delete was started on, and
	// whether the edition offers the database settings
	managerIndex int
	removing     string
	databases    bool

	file  openedFile // the file in the editor (files.go)
	draft *draftSave // the draft being named in the new-file picker, nil when none is (files.go)
	prog  progress   // the daemon's work left (progress.go)
	// the notifications (notify.go): the toasts over the page, what was posted before they were,
	// and the history
	toasts      *widget.Toasts
	early       []widget.Toast
	notices     []notice
	noticeList  *tuidecl.ListModel
	historyOpen bool
	vimKeys     *widget.Float // the Vim keys' card (vimkeys.go)
	findChip    *widget.Float // "finding …" and its ✕, under the menu at the top right (find.go)
	findLabel   *widget.Text
	// the plugins (plugins.go): where they are, the folder's as found, the menu's rows, and the open
	// ones by key
	pluginOpt  Plugins
	pluginList []pluginEntry
	pluginRows *tuidecl.ListModel
	// feedVersion numbers the editor's text for the document feed, and feedGen its pending send
	// (pluginfeed.go)
	feedVersion, feedGen int
	// pluginKeyRows are the SPC p card's letters: each command's, as bound (plugincmds.go)
	pluginKeyRows *tuidecl.ListModel
	running       map[string]*pluginRun
	// adding and managing them (plugininstall.go): the change PluginConfirm asks about, the manager's
	// rows, and the directory PluginRemove asks about
	pendingPlugin  *pluginChange
	managedList    []managedPlugin
	managedPlugins *tuidecl.ListModel
	removingPlugin string
	pageWidth      int // the page's width as last set: the ruler, the border, the gutter

	mu   sync.Mutex
	errs []error // handler errors, returned by Run
}

// Options are what New needs from the program around it.
type Options struct {
	// About is the build and location text of Help › About.
	About string
	// GUI is set when the UI runs in a window (--gui): its theme is kept apart from the
	// terminal's (gui.theme), sepia until one is chosen.
	GUI bool
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
	// Installed, when set, is the version of the autodoc a restart would start (the binary on
	// disk, which an update replaces while the daemon runs).
	Installed func() (string, error)
	// Plugins is where the Plugins menu finds plugins (plugins.go); its zero value finds none.
	Plugins Plugins
	// Registrations are this binary's own (ADR 0216): a daemon reporting a strict subset of them is
	// offered a restart as this build (registrations.go). The zero value is the community build's.
	Registrations registrations.Tables
	// AgentDir is where the agent terminal's agents work, a folder a workspace (agent.go); "" is
	// one under the system's temporary folder.
	AgentDir string
	// ConfigPath is the config file the TUI runs on: the agent's autodoc runs on it too, so it
	// reaches the TUI's store (agent.go). "" is the default config.
	ConfigPath string
	// ProgramOptions come after the program's own options (options): a build's choice of how
	// the document runs, such as tuidecl.WithStyle for the window's native widgets. nil: none.
	ProgramOptions []tuidecl.ProgramOption
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
		ws: opt.Workspace, remember: opt.Remember, installed: opt.Installed, ownTables: opt.Registrations, agentDir: opt.AgentDir, agentConfig: opt.ConfigPath,
		awaitExit:      awaitExit,
		browser:        openDefaultApp,
		picker:         tuidecl.NewListModel("key", "path"),
		outlineList:    tuidecl.NewListModel("key", "heading", "line"),
		wsProviders:    tuidecl.NewListModel("key", "label"),
		hits:           tuidecl.NewListModel("key", "hit", "path", "section"),
		newList:        tuidecl.NewListModel("key", "path"),
		providers:      tuidecl.NewListModel("key", "use", "name", "kind", "model", "context", "apiKey"),
		providerModels: tuidecl.NewListModel("key", "name"),
		agentRows:      tuidecl.NewListModel("key", "default", "name", "command"),
		recentModel:    tuidecl.NewListModel("key", "workspace", "path"),
		rankers:        tuidecl.NewListModel("key", "use", "name", "kind", "model", "apiKey"),
		vectors:        tuidecl.NewListModel("key", "state", "model", "dims", "vectors", "f32", "bits", "keys", "total"),
		explorer:       tuidecl.NewTreeListModel("key", "label"),
		explorerPaths:  map[string][]string{},
		openAt:         -1,
		prefs:          defaultPrefsFor(opt.GUI),
		gui:            opt.GUI,
		panelOpen:      map[string]bool{},
		termRestore:    true,
		relationsModel: tuidecl.NewListModel("key", "label"),
		noticeList:     tuidecl.NewListModel("key", "when", "text"),
		workspaces:     tuidecl.NewListModel("key", "label"),
		managed:        tuidecl.NewListModel("key", "name", "state", "root"),
		pluginOpt:      opt.Plugins,
		pluginRows:     tuidecl.NewListModel("key", "kind", "label", "enabled", "target", "rows"),
		pluginKeyRows:  tuidecl.NewListModel("key", "target"),
		managedPlugins: tuidecl.NewListModel("key", "name", "place", "commit", "source"),
		running:        map[string]*pluginRun{}}
	h.explorer.OnFetch = h.fetchExplorer
	h.loadPlugins()
	return h
}

// attach binds the host to the program built from its options (by New, or by a test running the
// same options), finds the one widget it reaches into, and connects once the program runs.
func (h *Host) attach(p *tuidecl.Program) error {
	h.p = p
	var ok bool
	if h.editor, ok = tuidecl.FindAs[editorWidget](p, "editor"); !ok {
		return errors.New("main.qml declares no Editor with id: editor")
	}
	h.core = h.editor.Core()
	h.installEditorMenu() // the stock rows, then related documents and back
	h.linkPage()          // a raw HTML file's page opens its links as the preview pane does
	p.Post(h.attachToasts)
	p.Post(h.attachVimKeys)
	p.Post(h.attachFindChip)
	p.Post(h.start)
	p.Post(h.launchServices)
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
			tuidecl.HotReload(tuidecl.OnReloadError(func(err error) { h.notify(err.Error()) })))
	} else {
		src = opt.Layout
		if src == nil {
			src = layout
			if opt.GUI { // the window starts in its own default, not the terminal's
				src = themeImport.ReplaceAll(src, []byte("import autodoc.theme."+defaultGUITheme+" "+moduleVersion))
			}
		}
		opts = append(modulesFrom(qmlFiles), tuidecl.LayoutSource("main.qml", src))
	}
	h.layoutSrc = src
	h.theme = themeOf(src)
	opts = append(opts,
		tuidecl.Highlighters(highlight.Definition{Name: "Markdown (search)", Highlighter: h.searchHighlighter()},
			highlight.Definition{Name: "Markdown (find)", Highlighter: h.pageHighlighter()},
			highlight.Definition{Name: "Plain text (find)", Highlighter: h.textHighlighter()},
			highlight.Definition{Name: "YAML (find)", Highlighter: h.yamlHighlighter()}),
		tuidecl.Sources(h.state()),
		tuidecl.Handlers(h.commands()),
		tuidecl.ErrorSink(h.keep),
		// The too-small screen's Quit is AutoDoc's own, which asks before unsaved changes are lost;
		// the caller's options come after, and may set another.
		tuidecl.AppOptions(append([]tuicore.AppOption{tuicore.WithQuit(h.quit)}, opt.App...)...),
	)
	return append(opts, opt.ProgramOptions...)
}

// Run runs the program until it quits; the session and every background call end with it.
func (h *Host) Run(ctx context.Context) error {
	defer h.cancel()
	defer h.session.Close()
	defer h.stopPlugins() // after the loop: each stopped, bounded, before the session ends
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
