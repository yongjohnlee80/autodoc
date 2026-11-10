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
	"time"

	"github.com/yongjohnlee80/golib/paths"

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

// writeFolderFiles writes a setup's folder files into the agent's folder dir, each private to this
// user (writePrivate: an MCP config names the build and the session's server), an earlier one
// replaced: each path stays inside it (filepath.IsLocal), as SetupFor holds.
func writeFolderFiles(dir string, files []crew.FolderFile) error {
	for _, f := range files {
		if !filepath.IsLocal(f.Path) {
			return fmt.Errorf("%w: %q", crew.ErrFolderFile, f.Path)
		}
		p := filepath.Join(dir, f.Path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		if err := writePrivate(p, []byte(f.Content)); err != nil {
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

// crewSetupTimeout bounds a crew start's setup: one run of the agent's own autodoc, which may write
// a file of AutoDoc's own (OpenCode's plugin) and prints its answer.
const crewSetupTimeout = 10 * time.Second

// crewSetupArgs is crewSetupOf's command line: the config, the kind, the window's host, the agent's
// own wrapper as the command its hooks and its MCP server run, its folder, and --mail-via mcp (the
// window registers the session with the mail_via it is answered). Never an environment value of
// the profile's.
func crewSetupArgs(config, kind, dir string) []string {
	args := []string{}
	if config != "" {
		args = append(args, "--config", config)
	}
	return append(args, "crew", "setup", "--kind", kind, "--host", crew.HostWindow,
		"--autodoc", shellQuote(filepath.Join(dir, "bin", "autodoc")), "--agent-dir", dir, "--mail-via", "mcp")
}

// askSetup runs kind's setup for the agent folder dir under environ off the loop, as the crew
// host runs its calls (its registration's too), every start: its answer depends on the folder, the
// profile's env, the program and the build, so none is kept for another start. then runs on the
// loop with it. agents.setupRun replaces the run (a cell's).
func (a *agents) askSetup(kind, dir string, environ []string, then func(crew.Setup, error)) {
	run := a.setupRun
	if run == nil {
		bin, config := a.agentBin(), a.agentConfig // read on the loop
		run = func(kind, dir string, environ []string) (crew.Setup, error) {
			return crewSetupOf(bin, config, kind, dir, environ)
		}
	}
	a.crew.Go(func(context.Context) any {
		s, err := run(kind, dir, environ)
		return setupAnswer{s, err}
	}, func(v any) {
		ans, _ := v.(setupAnswer)
		then(ans.s, ans.err)
	})
}

// setupAnswer is a crew start's setup, carried from off the loop.
type setupAnswer struct {
	s   crew.Setup
	err error
}
