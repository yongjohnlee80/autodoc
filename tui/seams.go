package tui

import (
	"os"
	"path/filepath"
	"time"

	regindex "github.com/yongjohnlee80/autodoc/core/registry"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// seams are the program's waits, addresses, limits and test hooks: the values a test sets for its
// own program, in one place. A Host copies them when it is made (Options.seams, or defaultSeams)
// and hands its features a pointer to its copy, so each program reads its own and two tests that
// set different ones can run at once. Each field is read on the loop unless its comment says
// otherwise. A test that changes one while its program runs changes that program's copy, on the
// loop: only a field read on the loop. The four read off the loop (serverReady, installFS,
// exportWritten, beforeSend) are set before the program starts, through tune(t), and never after;
// a worker that needs another field (env, registry) is handed a copy read on the loop as it starts.
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

	// installFS is swapIn's renames and removal; a test makes each fail in turn. Read off the loop.
	installFS installFS

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
	resultsResident:  100_000,
	resultsCopyCap:   16 << 20,
}
