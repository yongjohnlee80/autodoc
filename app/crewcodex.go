package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// codexLocator is the calling Codex session's endpoint
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler), for `crew endpoint --codex-thread`, which
// the agent guide has a Codex agent run with its $CODEX_THREAD_ID: the thread, and the app-server
// its TUI is attached to, which is Codex's control socket under $CODEX_HOME (default ~/.codex)
// resolved to the socket itself.
func codexLocator(thread string, getenv func(string) string) (string, error) {
	if thread == "" {
		return "", errors.New("no Codex thread: pass $CODEX_THREAD_ID, which Codex sets in every tool command")
	}
	home := getenv("CODEX_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		home = filepath.Join(h, ".codex")
	}
	link := filepath.Join(home, "app-server-control", "app-server-control.sock")
	server, err := filepath.EvalSymlinks(link)
	if err != nil {
		return "", fmt.Errorf("Codex's app-server control socket (%s): %w; is the shared app-server running?", link, err)
	}
	b, err := json.Marshal(map[string]string{"server": server, "thread": thread})
	return string(b), err
}
