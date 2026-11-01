package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// codexLocator is the calling Codex session's endpoint
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler), for `crew endpoint --codex-thread` (a
// Codex agent's own $CODEX_THREAD_ID) and `--codex-here` (the thread its window found): the
// thread, and the app-server its TUI is attached to (codexServer).
func codexLocator(thread string, getenv func(string) string) (string, error) {
	if thread == "" {
		return "", errors.New("no Codex thread: pass $CODEX_THREAD_ID, which Codex sets in every tool command")
	}
	server, err := codexServer(getenv)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(map[string]string{"server": server, "thread": thread})
	return string(b), err
}

// codexSocketRoots are the folders a Codex app-server's socket may lie in: Codex's own, for this
// user. A cell gives its own.
var codexSocketRoots = func() []string { return crew.CodexSocketDirs(os.Getuid()) }

// codexServer is the app-server a Codex agent's TUI attaches to: its own (crew.EnvCodexServer, an
// agent crew setup gave its own app-server), else the shared one's control socket under $CODEX_HOME
// (default ~/.codex). Either is resolved to the socket itself, which must lie in Codex's socket
// folder for this user (crew.CodexServerOK): a variable anyone can set, or a link, naming anything
// else is refused, never dialed or registered.
func codexServer(getenv func(string) string) (string, error) {
	link := getenv(crew.EnvCodexServer)
	if link == "" {
		home := getenv("CODEX_HOME")
		if home == "" {
			h, err := os.UserHomeDir()
			if err != nil {
				return "", err
			}
			home = filepath.Join(h, ".codex")
		}
		link = filepath.Join(home, "app-server-control", "app-server-control.sock")
	}
	server, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", fmt.Errorf("Codex's app-server control socket (%s): %w; is the shared app-server running?", link, err)
	}
	if !crew.CodexServerOK(server, codexSocketRoots()) {
		return "", fmt.Errorf("the app-server socket %s (from %s) isn't in Codex's socket folder: refused", server, link)
	}
	return server, nil
}
