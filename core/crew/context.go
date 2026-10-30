package crew

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ContextDir is the folder of an agent folder's session contexts (§4.5): one file per session,
// named by the session's id, so a host removing its ended session's file never touches another
// session's, a replacement's written into the same agent folder by another host at any moment.
// $AUTODOC_CREW_CONTEXT names the agent's own file.
const ContextDir = ".autodoc-crew"

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

// ContextPath is session's context file in the agent folder dir.
func ContextPath(dir, session string) string { return filepath.Join(dir, ContextDir, session) }

// checkSessionName refuses a session id that is no plain file name: it names the context's file.
func checkSessionName(session string) error {
	if session == "" || strings.HasPrefix(session, ".") || strings.ContainsAny(session, `/\`) {
		return fmt.Errorf("%w: session id %q", ErrInvalid, session)
	}
	return nil
}

// WriteContext writes c as its session's context file in the agent folder dir (ContextPath), mode
// 0600, through a temporary file renamed over it: a reader sees the whole context or none. It
// answers the file's path, which the agent is told ($AUTODOC_CREW_CONTEXT).
func WriteContext(dir string, c Context) (string, error) {
	if err := checkSessionName(c.Session); err != nil {
		return "", err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	folder := filepath.Join(dir, ContextDir)
	if err := os.MkdirAll(folder, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(folder, "."+c.Session+".*") // a dot file: never taken for a context
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // after the rename, nothing is there: a no-op
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return "", err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	path := ContextPath(dir, c.Session)
	return path, os.Rename(tmp, path)
}

// RemoveContext removes session's context file from the agent folder dir, once the session has
// ended, so no later process reads its credential. It is that file alone, by its session's name:
// nothing is read first and no lock is needed, since no other session's context has that name.
// One already gone is no error.
func RemoveContext(dir, session string) error {
	if err := checkSessionName(session); err != nil {
		return err
	}
	if err := os.Remove(ContextPath(dir, session)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
