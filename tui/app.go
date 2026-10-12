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
//	pick.go       the one picker core, pick[T], behind Picker.qml and the App.pick* commands
//	searchpick.go the search picker: the daemon's search as it is typed, and its hits
//	filepick.go   File › Open and the draft's name: pickers over the workspace's files
//	marks.go      words marked over Markdown's highlighting: the search's and the find's
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
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/script"
)

// Host is the program behind qml/main.qml.
type Host struct {
	p        *tuidecl.Program
	session  *Session
	reg      *registry          // the features, merged (feature.go)
	cat      *catalog           // the user commands, every source's (catalog.go): the registry's
	asker    *asker             // the one yes-or-no question (ask.go)
	prefRows []PrefSpec         // every preference declared, the host's and the features' (prefspec.go)
	keys     keyset             // the keys and their cards, the host's and the features' (keys.go)
	started  bool               // the first preferences were applied: the program has started (applyPrefs)
	editor   editorWidget       // main.qml's Editor, terminal or native (editor.go)
	core     *widget.EditorCore // its core: the text, the cursor and the modes
	gui      bool               // in a window (Options.GUI): the theme kept is gui.theme
	ctx      context.Context
	cancel   context.CancelFunc

	about     string
	aboutInfo AboutInfo // what Help › About lists (help.go)
	// layoutSrc is the layout as last loaded, dev the -dev directory ("" for none), theme the one
	// the screen wears (theme.go).
	layoutSrc []byte
	dev       string
	theme     string

	// the models the document binds
	workspaces, managed *rows[wsInfo] // the daemon's workspaces: the picker's and the manager's
	wsProviders         *tuidecl.ListModel
	vectors             *rows[vectorRow] // the workspace's models (vectors.go)

	// the pickers (pick.go): the search's, File › Open's and the draft's name (the outline's is
	// outline.go's); the search's own state, and its words
	searchPick        *pick[hit]
	openPick, newPick *pick[string]
	searchSeq         uint64
	searchCancel      context.CancelFunc
	searchQuery       string
	searchOpen        bool
	lastSearchError   string
	searchWaiting     bool
	searchWaitToast   bool
	marks             marks
	findMarks         marks // the page find's words, as the page's highlighter marks them (find.go)
	openAt            int   // where the next file opened puts the cursor, a byte offset; -1 for its start

	// find in a pane (find.go): the last find, and the cursors of the panes it moves
	find findState
	// the Relations drawer and the jump card (relations.go), a feature of its own; back's history
	rel     *related
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
	panelOpen                              map[string]bool
	lastPanel                              string      // the panel opened last (Arrange panel)
	tutorialAt                             int         // the tutorial's page shown (tutorial.go)
	fonts                                  FontControl // the window's fonts; nil in a terminal (fonts.go)
	families                               func(mono bool) []string
	defaultCellFont                        string
	fontsApplied                           appliedFonts
	fontGens                               map[uint64]appliedFonts // the fonts set at each generation, for the grid's (fontsFit)
	cellFonts, proseFonts                  []string                // the Fonts dialog's choices, "" the default first
	cellFontModel, proseFontModel          *tuidecl.ListModel
	arrange                                *arrangeState // an arrangement in progress (arrange.go)
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
	// the recent files (recent.go), a feature of its own
	recent *recent
	// the explorer (explorer.go), a feature of its own: find.go finds in its rows
	explorer *explorer

	// the AI models dialog (aimodels.go), a feature of its own: the host lists its providers again
	// when the workspace's vectors change under them (vectors.go), and its rankers when the daemon
	// says the ranker in use changed (events.go)
	ai *aiModels

	hitsRanked string // the hits' title's note of their order (rankedTitle)
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
	// the outline and the breadcrumb (outline.go), a feature of its own; crumbs is the breadcrumb
	// it puts after the file's name (Core.Crumbs)
	outline   *outliner
	crumbs    []string
	focusSent time.Time
	entered   bool // ws was entered on this connection's listing
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
	epoch     uint64
	listSeq   uint64        // numbers the file list's loads; the latest wins (search.go)
	listRetry time.Duration // the delay before the file list's next retry after a failure; 0 after a success
	files     FileList      // the workspace's files, as last listed (page.go)
	page      Page          // the page shown, its text as last snapshotted (page.go)
	pageGen   uint64        // numbers the typing pauses; the latest wins (page.go)
	typing    bool          // typed since the page was last snapshotted: its pause is to come (page.go)

	// the workspace manager: the row under its cursor, and whether the edition offers the database
	// settings
	managerIndex int
	databases    bool

	file openedFile // the file in the editor (files.go)

	// recovery (recovery.go): the folder, whether this start has offered its copies, the copies
	// still to ask about, and the one being restored once its file opens
	recoveryDir     string
	recoveryOffered bool
	// the Lua commands (scripts.go): the engine, and the user's folder of scripts
	scripts        *script.Engine
	scripting      *scripting // the daemon's verbs in the engine: autodoc.call, autodoc.<domain> (scripting.go)
	luaDir         string
	recoveryQueue  []recovered
	pendingRestore *recovered
	restoreWaiting bool       // a restore is under way: its open, or the unsaved question before it
	restoredCopy   *fileRef   // the copy whose text is in the editor, unsaved: kept until saved or discarded
	draft          *draftSave // the draft being named in the new-file picker, nil when none is (files.go)
	prog           progress   // the daemon's work left (progress.go)
	// the notifications (notify.go): the toasts over the page, what was posted before they were,
	// and the history
	toasts    *widget.Toasts
	early     []widget.Toast
	noticeLog *notices      // the notifications kept (notices.go)
	vimKeys   *widget.Float // the Vim keys' card (vimkeys.go)
	findChip  *widget.Float // "finding …" and its ✕, under the menu at the top right (find.go)
	findLabel *widget.Text
	// the plugins (plugins.go): where they are, the folder's as found, the menu's rows, and the open
	// ones by key
	pluginOpt  Plugins
	pluginList []pluginEntry
	pluginRows *tuidecl.ListModel
	// feedVersion numbers the editor's text for the document feed, and feedGen its pending send
	// (pluginfeed.go)
	feedVersion, feedGen int
	// pluginKeyRows are the SPC p card's letters: each command's, as bound (plugincmds.go)
	pluginKeyRows    *tuidecl.ListModel
	pluginKeyTargets map[string]string
	running          map[string]*pluginRun
	// pluginCmds are the catalog's plugin entries by id, and pluginLetters their letters as F1
	// names them (plugincmds.go)
	pluginCmds    map[string]pluginCommandRef
	pluginLetters map[string][]string
	// adding and managing them (plugininstall.go): the change PluginConfirm asks about, and the
	// manager's rows
	pendingPlugin  *pluginChange
	managedPlugins *rows[managedPlugin]
	pageWidth      int // the page's width as last set: the ruler, the border, the gutter
	// helpNow is F1's text as last set; helpPosted, that a refresh of it is on its way
	helpNow    string
	helpPosted bool
	// moreFeatures follow the host's own in features(): a test's, whose keys and catalog entries
	// run in the program as any feature's do
	moreFeatures []Feature

	mu   sync.Mutex
	errs []error // handler errors, returned by Run
}

// Options are what New needs from the program around it.
type Options struct {
	// About replaces Help › About's text entirely; "" is AutoDoc's, from AboutInfo.
	About string
	// AboutInfo is what Help › About lists: the version and its date, the config and the daemon's
	// log, the plugins (the daemon's socket is the session's).
	AboutInfo AboutInfo
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
	// RecoveryDir is where unsaved work is kept when AutoDoc ends without being asked to, and offered
	// back at the next start (recovery.go); "" keeps nothing.
	RecoveryDir string
	// LuaDir holds the user's Lua commands, every *.lua in it (scripts.go); "" loads only AutoDoc's
	// own.
	LuaDir string
	// ConfigPath is the config file the TUI runs on: the agent's autodoc runs on it too, so it
	// reaches the TUI's store (agent.go). "" is the default config.
	ConfigPath string
	// ProgramOptions come after the program's own options (options): a build's choice of how
	// the document runs, such as tuidecl.WithStyle for the window's native widgets. nil: none.
	ProgramOptions []tuidecl.ProgramOption
	// Fonts are the window's fonts (--gui's backend); nil in a terminal. FontFamilies lists the
	// installed families, and DefaultCellFont is the cell font the window draws in by default.
	Fonts           FontControl
	FontFamilies    func(mono bool) []string
	DefaultCellFont string
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
	h := &Host{session: session, ctx: ctx, cancel: cancel, about: opt.About, aboutInfo: opt.AboutInfo, dev: opt.Dev,
		ws: opt.Workspace, remember: opt.Remember, installed: opt.Installed, ownTables: opt.Registrations, agentDir: opt.AgentDir, agentConfig: opt.ConfigPath, recoveryDir: opt.RecoveryDir, luaDir: opt.LuaDir,
		awaitExit:       awaitExit,
		browser:         openDefaultApp,
		wsProviders:     tuidecl.NewListModel("key", "label"),
		agentRows:       tuidecl.NewListModel("key", "default", "name", "command"),
		cellFontModel:   fontModel(),
		proseFontModel:  fontModel(),
		fonts:           opt.Fonts,
		families:        opt.FontFamilies,
		defaultCellFont: opt.DefaultCellFont,
		vectors:         newRows(tuidecl.NewListModel("key", "state", "model", "dims", "vectors", "f32", "bits", "keys", "total"), vectorRowOf),
		openAt:          -1,
		prefs:           defaultPrefsFor(opt.GUI),
		gui:             opt.GUI,
		panelOpen:       map[string]bool{},
		termRestore:     true,
		workspaces:      newRows(tuidecl.NewListModel("key", "label"), workspaceRowOf),
		managed:         newRows(tuidecl.NewListModel("key", "name", "state", "root"), managedRowOf),
		pluginOpt:       opt.Plugins,
		pluginRows:      tuidecl.NewListModel("key", "kind", "label", "enabled", "target", "rows"),
		pluginKeyRows:   tuidecl.NewListModel("key", "target"),
		running:         map[string]*pluginRun{}}
	h.noticeLog = newNotices(core{h})
	h.recent = newRecent(core{h})
	h.explorer = newExplorer(core{h})
	h.ai = newAIModels(core{h})
	h.outline = newOutline(core{h})
	h.rel = newRelated(core{h})
	h.managedPlugins = newRows(tuidecl.NewListModel("key", "name", "place", "commit", "source"), h.managedPluginRowOf)
	h.searchPick, h.openPick, h.newPick = h.newSearchPick(), h.newOpenPick(), h.newNewPick()
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
	startRendered(h.editor) // the GUI's page opens in its Rendered view (rendered_gui.go)
	h.core.SetHighlightOverlay(func(line string, semantic []highlight.Span) []highlight.Span {
		return markedSpans(&h.findMarks, line, semantic)
	})
	if h.cat != nil {
		h.cat.changed = h.commandsChanged // F1 lists the commands as they come and go (help.go)
	}
	h.cat.attach(h.core)             // the catalog's verbs on the command line: find is the page's (catalog.go)
	h.core.SetOnEscape(h.escapeFind) // Esc in the page ends its find (find.go)
	h.loadScripts()                  // the Lua commands: AutoDoc's own, then the user's (scripts.go)
	h.core.SetTableEditing(true)     // the untitled draft is Markdown; readPage decides for a file
	h.installEditorMenu()            // the stock rows, then related documents and back
	h.linkPage()                     // a raw HTML file's page opens its links as the preview pane does
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
	reg, err := newRegistry(h.features()...)
	if err != nil {
		panic(err) // two features naming one source or command: a build that cannot start
	}
	h.reg, h.cat = reg, reg.catalog
	h.syncPluginEntries() // the plugins found at newHost, now that there is a catalog
	h.refreshPlugins()
	opts = append(opts,
		tuidecl.Highlighters(h.searchDefinition(), highlight.Definition{Name: "Plain text", Highlighter: highlight.HighlighterFunc(func(string, highlight.State) ([]highlight.Span, highlight.State) { return nil, 0 })}),
		tuidecl.Sources(h.reg.state),
		tuidecl.Handlers(h.reg.handlers),
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
	// ended unasked (a signal) with unsaved work: kept, and offered at the next start
	keepErr := h.keepRecovery()
	if h.scripts != nil {
		h.scripts.Close()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return errors.Join(append([]error{err, keepErr}, h.errs...)...)
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
