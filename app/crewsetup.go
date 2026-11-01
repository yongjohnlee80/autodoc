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

// crewSetupMain runs `autodoc crew setup --kind K [--host window|nvim] [--program P]
// [--autodoc CMD]`: it prints the crew agent's setup (crew.SetupFor) as JSON, for a
// host to start the agent with: what to append to its command, AutoDoc's env to merge after the
// profile's, the user-level files it needs, whether it reports its permission prompts (for
// crew.register), and what to tell the user. The agent's environment, the profile's included, is the
// one this runs under: a host passes it in the process environment, never on the command line,
// which any user can read (/proc/<pid>/cmdline), as the owner alone can read the environment. None
// of it is printed back. AutoDoc's own files (the
// OpenCode plugin, what each Claude Code knows) go in the data folder of the config at configPath.
// It needs no session: a host runs it before one starts.
func crewSetupMain(configPath string, args []string, environ []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autodoc crew setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "the agent's crew kind: claude, codex, agy, opencode …")
	host := fs.String("host", crew.HostNvim, "the host: nvim (Claude's settings on its command line) or window")
	program := fs.String("program", "", "the CLI the agent's command runs (default the kind's own name)")
	autodoc := fs.String("autodoc", "autodoc", "the command the agent's hooks run AutoDoc with, as a shell reads it")
	agentDir := fs.String("agent-dir", "", "the folder the host owns for the agent, where a kind's folder files go (never the user's project)")
	mailVia := fs.String("mail-via", "", "mcp: the host registers the session with the mail_via this setup answers (crew.register's), so a kind that can read its mail through the crew MCP server is given it; absent or cli: the CLI's setup. agy, whose mail is MCP alone, is given the server either way")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *kind == "" || fs.NArg() != 0 || (*host != crew.HostNvim && *host != crew.HostWindow) || (*mailVia != "" && *mailVia != "cli" && *mailVia != "mcp") {
		fmt.Fprintln(stderr, "usage: autodoc crew setup --kind K [--host window|nvim] [--program P] [--autodoc CMD] [--agent-dir DIR] [--mail-via mcp|cli]")
		return 2
	}
	// the crew MCP server only for a host that registers the mail mode it is answered (--mail-via
	// mcp), or for a kind whose mail is MCP alone (agy), which its kind's default already says: a
	// host that registers none would otherwise have a session allowed the MCP tools alone while its
	// notice and guide name the CLI's commands. A command its words can't be read from offers none:
	// the setup goes on without it, and says why
	var servers []crew.MCPServer
	var serverErr error
	if *mailVia == "mcp" || crew.MailViaOf(*kind) == crew.MailViaMCP {
		var server crew.MCPServer
		if server, serverErr = crewMCPServer(*autodoc); serverErr == nil {
			servers = append(servers, server)
		}
	}
	env := map[string]string{}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
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
		DataDir: data, Dir: dir, AgentDir: *agentDir, Servers: servers}, filepath.Join(data, "claude-hooks.json"))
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	if serverErr != nil {
		s.Notices = append(s.Notices, "AutoDoc's MCP server isn't offered: "+serverErr.Error())
	}
	b, err := json.Marshal(s)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	fmt.Fprintln(stdout, string(b))
	return 0
}

// crewMCPServer is the crew's own MCP server for a session (autodoc crew mcp), as SetupFor's kinds
// are given it: run by the agent's own autodoc (autodoc, as a shell reads it), its tools
// crew.MCPToolNames. It takes no env of its own: started by the agent's CLI, it inherits the
// agent's environment, $AUTODOC_CREW_CONTEXT with it, so it acts as that session.
func crewMCPServer(autodoc string) (crew.MCPServer, error) {
	argv, err := shellFields(autodoc)
	if err != nil || len(argv) == 0 {
		return crew.MCPServer{}, fmt.Errorf("--autodoc %q: not a command: %v", autodoc, err)
	}
	return crew.MCPServer{Name: MCPServerName, Command: append(argv, "crew", "mcp"), Env: map[string]string{},
		Tools: crew.MCPToolNames()}, nil
}

// shellFields splits s into words as a POSIX shell does for a plain command: blanks separate, single
// quotes keep everything, double quotes keep all but a backslash before \, " or $, and a backslash
// outside quotes keeps the next character. Anything a shell would expand ($, `, globs) is refused, as
// is an unterminated quote: a command must mean what it says.
func shellFields(s string) ([]string, error) {
	var out []string
	var w strings.Builder
	in, quote := false, rune(0)
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		r := rs[i]
		switch {
		case quote == '\'':
			if r == '\'' {
				quote = 0
			} else {
				w.WriteRune(r)
			}
		case quote == '"':
			switch {
			case r == '"':
				quote = 0
			case r == '\\' && i+1 < len(rs) && strings.ContainsRune(`\"$`, rs[i+1]):
				i++
				w.WriteRune(rs[i])
			case r == '$' || r == '`':
				return nil, fmt.Errorf("%q would be expanded by a shell", string(r))
			default:
				w.WriteRune(r)
			}
		case r == ' ' || r == '\t' || r == '\n':
			if in {
				out, in = append(out, w.String()), false
				w.Reset()
			}
		case r == '\'' || r == '"':
			quote, in = r, true
		case r == '\\':
			if i+1 == len(rs) {
				return nil, fmt.Errorf("a trailing backslash")
			}
			i++
			w.WriteRune(rs[i])
			in = true
		case strings.ContainsRune("$`*?[]{}~;&|<>()#", r):
			return nil, fmt.Errorf("%q would be read by a shell, not passed", string(r))
		default:
			w.WriteRune(r)
			in = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("an unterminated %c quote", quote)
	}
	if in {
		out = append(out, w.String())
	}
	return out, nil
}
