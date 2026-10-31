package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// envFlag is a repeatable --env K=V.
type envFlag map[string]string

func (e envFlag) String() string { return "" }
func (e envFlag) Set(kv string) error {
	k, v, ok := strings.Cut(kv, "=")
	if !ok || k == "" {
		return fmt.Errorf("--env %q: want K=V", kv)
	}
	e[k] = v
	return nil
}

// crewSetupMain runs `autodoc crew setup --kind K [--host window|nvim] [--program P]
// [--autodoc CMD] [--env K=V]…`: it prints the crew agent's setup (crew.SetupFor) as JSON, for a
// host to start the agent with: what to append to its command, AutoDoc's env to merge after the
// profile's, the user-level files it needs, whether it reports its permission prompts (for
// crew.register), and what to tell the user. The agent's environment is the one this runs under,
// each --env (the profile's) set over it; none of it is printed back. AutoDoc's own files (the
// OpenCode plugin, what each Claude Code knows) go in the data folder of the config at configPath.
// It needs no session: a host runs it before one starts.
func crewSetupMain(configPath string, args []string, environ []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autodoc crew setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "the agent's crew kind: claude, codex, agy, opencode …")
	host := fs.String("host", crew.HostNvim, "the host: nvim (Claude's settings on its command line) or window")
	program := fs.String("program", "", "the CLI the agent's command runs (default the kind's own name)")
	autodoc := fs.String("autodoc", "autodoc", "the command the agent's hooks run AutoDoc with, as a shell reads it")
	profile := envFlag{}
	fs.Var(profile, "env", "K=V of the profile's environment, set over the one this runs under (repeatable)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *kind == "" || fs.NArg() != 0 || (*host != crew.HostNvim && *host != crew.HostWindow) {
		fmt.Fprintln(stderr, "usage: autodoc crew setup --kind K [--host window|nvim] [--program P] [--autodoc CMD] [--env K=V]…")
		return 2
	}
	env := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for k, v := range profile {
		env[k] = v
	}
	data, err := crewDataDir(configPath)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	dir, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	s, err := crew.SetupFor(crew.SetupInput{Kind: *kind, Program: *program, Host: *host, Autodoc: *autodoc, Env: env,
		DataDir: data, Dir: dir}, filepath.Join(data, "claude-hooks.json"))
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	b, err := json.Marshal(s)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
