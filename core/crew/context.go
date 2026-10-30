package crew

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// ContextFile is a session context's file name (§4.5): in the folder $AUTODOC_CREW_CONTEXT names,
// or in the agent's working directory.
const ContextFile = ".autodoc-crew"

// Context is a session's context, written by its host (the AutoDoc window, Neovim) before the agent
// starts and read by `autodoc crew|mail`: which daemon and store, and the session's credential.
type Context struct {
	Store      string `json:"store"`  // the daemon's store id (sys.hello's store_id)
	Socket     string `json:"socket"` // its address
	Scope      string `json:"scope"`
	Profile    string `json:"profile"`
	Session    string `json:"session"`
	Credential string `json:"credential"` // "<session id>.<secret>"
	Kind       string `json:"kind"`
}

// WriteContext writes c as dir's context file, mode 0600, through a temporary file renamed over it:
// a reader sees the old session's context or the new one, never a part of either.
func WriteContext(dir string, c Context) error {
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ContextFile+".*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // after the rename, nothing is there: a no-op
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, ContextFile))
}

// RemoveContextOf removes dir's context file when it names session, once that session has ended:
// no later process reads a dead session's credential, and a newer session's context in the same
// folder is never taken with it. One already gone, or naming another session, is no error.
func RemoveContextOf(dir, session string) error {
	path := filepath.Join(dir, ContextFile)
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var c Context
	if json.Unmarshal(b, &c) == nil && c.Session != session {
		return nil // a newer session's
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
