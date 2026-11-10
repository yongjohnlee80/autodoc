package tui

import (
	"context"
	"path/filepath"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

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
	a.crew.c.Go(func(context.Context) any {
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
