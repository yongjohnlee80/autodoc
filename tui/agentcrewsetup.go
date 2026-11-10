package tui

// AN AGENT'S CREW SETUP: what an agent of a window holding a crew scope is started with, the agents'
// own: its session, its folder's names, and the setup the agent's autodoc gives (crew setup) with
// the files it writes into the agent's folder. The crew host (crewhost.go) registers the session and
// publishes its context.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// crewSetupOf is the setup of a crew agent of kind whose folder is dir, as the agent's own autodoc
// (bin, on config) answers it: `autodoc crew setup --kind K --host window --autodoc <its wrapper>
// --agent-dir <dir>` (docs/decisions/crew.md: every host asks, none builds). Its mail mode, argv,
// env, allowance, folder files and after-steps are that build's. The profile's environment
// (environ) is the process's own, never on its command line, which any user can read.
func crewSetupOf(bin, config, kind, dir string, environ []string) (crew.Setup, error) {
	ctx, cancel := context.WithTimeout(context.Background(), crewSetupTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, crewSetupArgs(config, kind, dir)...)
	cmd.Dir = dir
	cmd.Env = environ
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return crew.Setup{}, fmt.Errorf("crew setup: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return crew.Setup{}, fmt.Errorf("crew setup: %w", err)
	}
	var s crew.Setup
	if err := json.Unmarshal(out, &s); err != nil {
		return crew.Setup{}, fmt.Errorf("unexpected output from crew setup: %q", strings.TrimSpace(string(out)))
	}
	return s, nil
}

// writeFolderFiles writes a setup's folder files into the agent's folder dir, 0600 (an MCP config
// names the session's context): each path stays inside it (filepath.IsLocal), as SetupFor holds.
func writeFolderFiles(dir string, files []crew.FolderFile) error {
	for _, f := range files {
		if !filepath.IsLocal(f.Path) {
			return fmt.Errorf("%w: %q", crew.ErrFolderFile, f.Path)
		}
		p := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := writeAtomic(p, []byte(f.Content), 0o600); err != nil {
			return err
		}
	}
	return nil
}

// crewSession is one agent's session, as registered under the window's scope.
type crewSession struct {
	ID, Profile, Scope string
	Epoch              int64
	Dir                string // the folder whose context file names it
}

// crewFileName is identity as one path element: paths.Encode's, so a colon, a separator or a name
// Windows reserves is escaped, and two identities never share a name.
func crewFileName(identity string) string { return paths.Encode(identity) }
