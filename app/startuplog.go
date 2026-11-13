package app

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/internal/userdirs"
)

// startupLogName is where a GUI that gave up its console says why it could not start, in AutoDoc's
// folder of the user's state (docs/decisions/install.md#windows).
const startupLogName = "gui-startup.log"

// startupLogPath is the log's path.
func startupLogPath() (string, error) {
	dir, err := userdirs.State(nil)
	if err != nil {
		return "", fmt.Errorf("the startup log's path: %w", err)
	}
	return filepath.Join(dir, "autodoc", startupLogName), nil
}

// openStartupLog creates the log afresh for this run, private to the user, and its folder.
func openStartupLog() (*os.File, error) {
	path, err := startupLogPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
}
