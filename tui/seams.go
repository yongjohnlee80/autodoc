package tui

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	regindex "github.com/yongjohnlee80/autodoc/core/registry"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// seams are the program's waits, addresses, limits and test hooks: the values a test sets for its
// own program, in one place. A Host copies them when it is made (Options.seams, or defaultSeams)
// and hands its features a pointer to its copy, so each program reads its own and two tests that
// set different ones can run at once. Each field is read on the loop unless its comment says
// otherwise. A test that changes one while its program runs changes that program's copy, on the
// loop: only a field read on the loop. The four read off the loop (serverReady, installFS,
// exportWritten, beforeSend) are set before the program starts, through tune(t), and never after;
// a worker that needs another field (env, registry) is handed a copy read on the loop as it starts.
// The agents' hooks an agent's start reads off the loop (agentSpawn, agentCodexHere, agentBin) are
// the exception: each program reads its own atomic copy (live), set through their setters, so a
// test may change one on the loop while the program runs.
type seams struct {
	// diskPoll is vfs.Poll's interval where the folder has no native watcher.
	// NOTE: deadline: a poll is a listing per interval, the only way to see a change without a watcher.
	diskPoll time.Duration

	// pluginFeed is the pause the plugins' document feed waits for after an edit or a move.
	pluginFeed time.Duration
	// The deadlines of a plugin's life, read as it starts: plugin.open to host.ready (handshake),
	// plugin.close to its exit before SIGTERM (grace), SIGTERM to its exit before SIGKILL (termGrace).
	pluginHandshake, pluginGrace, pluginTermGrace time.Duration

	// serverReady bounds the wait for a started agent server's socket; a server not listening by
	// then is stopped, and the CLI isn't started. Read off the loop, by the agent's start.
	serverReady time.Duration

	// terminalCardArm is how long a terminal card shows before its Run counts: a card queued behind
	// another opens the moment that one is answered, Run in the same place on the same key, so a key
	// typed ahead or a double click could run a line unread. A Run sooner is a Don't run, and said.
	terminalCardArm time.Duration

	// scheduled, when set (tests only), hears each delay Host.after is given, on the loop: the seam
	// that shows a consumer's wait without timing it.
	scheduled func(time.Duration)

	// registry is the plugin registry's index and its cache between runs (registry.go).
	registry registrySource

	// latestURL is the latest release of AutoDoc, as GitHub answers it (startup.go).
	latestURL string

	// offerAgentSetup and offerVimQuestion are whether a first run offers the agent setup and asks
	// the Vim question at all; the tests of something else turn them off, as their first card would
	// otherwise be one of these.
	offerAgentSetup, offerVimQuestion bool
	// lookAgentCLI, when a test sets it, is how the agent setup's card finds the CLIs; nil is on the
	// program's PATH (env).
	lookAgentCLI func(string) (string, error)
	// The agents' hooks a test sets (agentSeams: SetupRun, Spawn, CommandCheck, CodexHere,
	// PromptsReported, AgentBin), each nil or "" for the program's own. Spawn, CodexHere and
	// AgentBin are read off the loop too, by an agent's start (runAgent): each is set through its
	// setter (setAgentSpawn, setAgentCodexHere, setAgentBin), and a program reads its own atomic copy
	// (live), so a test may set one on the loop while the program runs.
	agentSetupRun func(kind, dir string, environ []string) (crew.Setup, error)
	agentSpawn    func(pty.Cmd) (terminal.TerminalProcess, error)
	// shellSpawn, when a test sets it, is a shell tab's program in place of the user's shell on a
	// pseudo-terminal: a spawn the test holds, say (newShell).
	shellSpawn           func(pty.Cmd) (terminal.TerminalProcess, error)
	agentCommandCheck    func(shell usershell.Shell, command string, env []string) error
	agentCodexHere       func(environ []string, args ...string) error
	agentPromptsReported func(kind string, reported bool) bool
	agentBin             string
	// live is the program's own copy of the hooks read off the loop, made from the fields above
	// when its Host is made (ownHooks); nil before. Each Host makes its own: two programs never
	// share one.
	live *liveHooks

	// installFS is swapIn's renames and removal; a test makes each fail in turn. Read off the loop.
	installFS installFS
	// folderReadWait is how long a plugin's removal or update waits for a read of its folder already
	// running (the manager's listing, an update's fetch) before it fails, the folder left as it is.
	// NOTE: deadline: a read is a git process, which only its own minutes-long timeout ends.
	folderReadWait time.Duration
	// pluginGit is the git a read of an installed plugin's folder runs (the manager's listing, an
	// update's fetch); nil is git itself. A test holds a read here. Handed to the worker on the loop.
	pluginGit func(ctx context.Context, env procenv.Env, dir string, args ...string) (string, error)

	// resultsResident is the most rows the results panel holds: rows past it are not loaded, and the
	// table says the result has more. resultsCopyCap is the most text a copy puts on the clipboard:
	// past it nothing is copied, and the notice says to export instead.
	resultsResident, resultsCopyCap int
	// exportWritten is told each chunk an export has written, as it is written, off the loop (a test
	// holds the writer here, so an export is still writing when AutoDoc quits); nil in AutoDoc.
	exportWritten func(rows int)

	// beforeSend, when set, runs in a worker as a gated call is about to be sent: a cell holds the
	// call there, while the workspace changes under it. Off the loop. Nothing in the program sets it.
	beforeSend func()

	// flashed, when a test sets it, hears each step of a dock flash on the loop: the panel, and its
	// icon lit or not.
	flashed func(id string, lit bool)

	// labelsResolved, when a test sets it, is told each pass bufferTabLabels makes over the buffers
	// (the cells count them, bufferkeys_test.go); nil in AutoDoc.
	labelsResolved func()

	// readDropped, when a test sets it, is told the path of each note's read whose answer loadInto
	// dropped as stale, once it has said so or not (the cells of a silent drop wait on it,
	// openlost_test.go); nil in AutoDoc.
	readDropped func(p string)

	// globeMapPage is the documents a page of the GlobeMap's map asks for; 0 is the daemon's
	// default. A test makes it small, to read a map of a few documents over several pages.
	globeMapPage int

	// surfaceSinkOf is where a plugin run's frames go; nil is the window's stream
	// (pluginsurface_gui.go), or none in a terminal. A cell sets its own, so the host's whole surface
	// path runs without a window.
	surfaceSinkOf func(*plugins) (surfaceSink, any, bool)

	// env, when set (tests only), is the program's environment: its HOME, PATH, cache and temporary
	// folders, and what a process it starts inherits. nil is the process's own, read through the os
	// calls themselves as it is needed (procenv, userdirs).
	env procenv.Env
}

// registrySource is where the plugin registry's index is fetched from, and where it is kept between
// runs.
type registrySource struct {
	url      string
	cacheDir func() (string, error)
}

// installFS is a plugin install's renames and removal (swapIn).
type installFS struct {
	rename    func(oldpath, newpath string) error
	removeAll func(path string) error
}

// defaultSeams are the program's own. The package's tests set their own defaults over them once,
// before any test runs (TestMain), and each test its own values for its own programs.
var defaultSeams = seams{
	diskPoll:        2 * time.Second,
	pluginFeed:      300 * time.Millisecond,
	pluginHandshake: 5 * time.Second,
	pluginGrace:     2 * time.Second,
	pluginTermGrace: 1 * time.Second,
	serverReady:     10 * time.Second,
	terminalCardArm: 400 * time.Millisecond,
	registry: registrySource{url: regindex.PagesURL + "index.json", cacheDir: func() (string, error) {
		d, err := os.UserCacheDir()
		return filepath.Join(d, "autodoc", "registry"), err
	}},
	latestURL:        "https://api.github.com/repos/yongjohnlee80/autodoc/releases/latest",
	offerAgentSetup:  true,
	offerVimQuestion: true,
	installFS:        installFS{rename: os.Rename, removeAll: os.RemoveAll},
	folderReadWait:   5 * time.Second,
	resultsResident:  100_000,
	resultsCopyCap:   16 << 20,
}

// Env, ServerReady, OfferAgentSetup, LookAgentCLI, VimQuestionPending and the agents' hooks
// (SetupRun, Spawn, CommandCheck, CodexHere, PromptsReported, AgentBin) are the agents' seams
// (agent.go agentSeams), read when used.
func (s *seams) Env() procenv.Env                           { return s.env }
func (s *seams) ServerReady() time.Duration                 { return s.serverReady }
func (s *seams) OfferAgentSetup() bool                      { return s.offerAgentSetup }
func (s *seams) LookAgentCLI() func(string) (string, error) { return s.lookAgentCLI }
func (s *seams) VimQuestionPending(p feature.PrefsView) bool {
	return vimQuestionPending(s, p)
}

func (s *seams) SetupRun() func(kind, dir string, environ []string) (crew.Setup, error) {
	return s.agentSetupRun
}
func (s *seams) Spawn() func(pty.Cmd) (terminal.TerminalProcess, error) {
	if s.live != nil {
		return *s.live.spawn.Load()
	}
	return s.agentSpawn
}
func (s *seams) CommandCheck() func(shell usershell.Shell, command string, env []string) error {
	return s.agentCommandCheck
}
func (s *seams) CodexHere() func(environ []string, args ...string) error {
	if s.live != nil {
		return *s.live.codexHere.Load()
	}
	return s.agentCodexHere
}
func (s *seams) PromptsReported() func(kind string, reported bool) bool {
	return s.agentPromptsReported
}
func (s *seams) AgentBin() string {
	if s.live != nil {
		return *s.live.bin.Load()
	}
	return s.agentBin
}

// liveHooks are a program's own copies of the agents' hooks read off the loop (Spawn, CodexHere,
// AgentBin), each read and set atomically.
type liveHooks struct {
	spawn     atomic.Pointer[func(pty.Cmd) (terminal.TerminalProcess, error)]
	codexHere atomic.Pointer[func(environ []string, args ...string) error]
	bin       atomic.Pointer[string]
}

// ownHooks gives s its own live hooks, made from its fields: a Host calls it once, as it is made,
// before anything reads them.
func (s *seams) ownHooks() {
	spawn, codexHere, bin := s.agentSpawn, s.agentCodexHere, s.agentBin
	l := &liveHooks{}
	l.spawn.Store(&spawn)
	l.codexHere.Store(&codexHere)
	l.bin.Store(&bin)
	s.live = l
}

// setAgentSpawn, setAgentCodexHere and setAgentBin set a hook read off the loop: the program's own
// copy once its Host is made (on the loop, then), else the field it is made from.
func (s *seams) setAgentSpawn(f func(pty.Cmd) (terminal.TerminalProcess, error)) {
	if s.live != nil {
		s.live.spawn.Store(&f)
		return
	}
	s.agentSpawn = f
}

func (s *seams) setAgentCodexHere(f func(environ []string, args ...string) error) {
	if s.live != nil {
		s.live.codexHere.Store(&f)
		return
	}
	s.agentCodexHere = f
}

func (s *seams) setAgentBin(bin string) {
	if s.live != nil {
		s.live.bin.Store(&bin)
		return
	}
	s.agentBin = bin
}

// shellSpawnSeam is the test's shell stand-in (seams.shellSpawn), nil for the user's shell: what
// the terminals ask for each new shell (terminals.shellSpawn).
func (h *Host) shellSpawnSeam() func(pty.Cmd) (terminal.TerminalProcess, error) {
	return h.seams.shellSpawn
}
