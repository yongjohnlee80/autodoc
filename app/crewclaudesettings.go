package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// crewClaudeSettingsMain runs `autodoc crew claude-settings [--claude PATH] [--autodoc CMD]
// [--prompts]`: it prints a crew Claude agent's settings, as `claude --settings` takes them, for
// the Claude Code found on PATH (or the one --claude names), from the one source an AutoDoc window
// writes its own agents' from (crew.ClaudeSettings): the SessionStart hook that registers the
// session's endpoint, crossSessionInbound "accept", and the hooks that report its permission
// prompts when that Claude Code knows them; only the events it knows. --prompts prints instead
// whether those hooks are there (true or false), which the session's crew.register says. It needs
// no session: a host runs it before one starts.
func crewClaudeSettingsMain(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autodoc crew claude-settings", flag.ContinueOnError)
	fs.SetOutput(stderr)
	claude := fs.String("claude", "claude", "the Claude Code to write them for: a path, or a name looked up on PATH")
	autodoc := fs.String("autodoc", "autodoc", "the command the hooks run AutoDoc with, as a shell reads it")
	prompts := fs.Bool("prompts", false, "print whether the settings report the permission prompts, instead of the settings")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: autodoc crew claude-settings [--claude PATH] [--autodoc CMD] [--prompts]")
		return 2
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	bin, err := crew.FindClaude(*claude, os.Getenv("PATH"), cwd)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	k, err := crew.ClaudeKnows(bin)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	settings, reported := crew.ClaudeSettings(*autodoc, k)
	if *prompts {
		fmt.Fprintln(stdout, reported)
		return 0
	}
	b, err := json.Marshal(settings)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}
