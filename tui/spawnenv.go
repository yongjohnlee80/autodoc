package tui

import (
	"os"
	"slices"
	"strings"
)

// SPAWNER IDENTITY — the variables naming the agent host that started AutoDoc. A process AutoDoc
// starts on its own behalf (an agent, a plugin, a plugin's build, git) does not get them: with
// them, it could act as that host's agent (send its mail, use its session's socket and secret).
// Each is named, with what it is; nothing else is dropped (PATH, HOME and the user's own settings,
// MCP_TIMEOUT say, pass through). A profile's command that sets one itself (NAME=value before its
// program) has it: the shell sets it after they were dropped. The terminal panel is the user's own
// shell, and keeps them.
const (
	autoAgentsPrefix = "AUTO_AGENTS_" // auto-agents' identity and KB scope: the mailbox, the instance, the KB roots
)

var spawnerIdentityNames = []string{
	"CLAUDECODE",                   // marks the process as running inside a Claude Code session
	"CLAUDE_CODE_SESSION_ID",       // the parent Claude Code session's id
	"CLAUDE_CODE_CHILD_SESSION",    // marks the process as that session's child
	"CLAUDE_CODE_ENTRYPOINT",       // how the parent session was started
	"CLAUDE_CODE_SESSION_ATTENDED", // whether a user attends the parent session
	"CLAUDE_CODE_MESSAGING_SOCKET", // the parent session's messaging socket
	"CLAUDE_CODE_MESSAGING_TOKEN",  // that socket's secret
	"CLAUDE_CODE_SSE_PORT",         // the parent session's IDE connection
	"ENABLE_IDE_INTEGRATION",       // has a Claude Code connect to that IDE: an agent would dial the parent's
	"CLAUDE_CODE_EXECPATH",         // the parent session's own binary
	"CLAUDE_EFFORT",                // the parent session's effort, which Claude Code hands its subprocesses
	"CLAUDE_PID",                   // the parent session's process
	"NVIM",                         // the parent Neovim's RPC socket
	"NVIM_LISTEN_ADDRESS",          // the same socket, as older Neovims name it
	"AUTODOC_CREW_CONTEXT",         // the crew session of the agent that started AutoDoc, never its agents'
}

// spawnerIdentity are the names in environ (os.Environ's form) that name the agent host that
// started AutoDoc.
func spawnerIdentity(environ []string) []string {
	var out []string
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, autoAgentsPrefix) || slices.Contains(spawnerIdentityNames, name) {
			out = append(out, name)
		}
	}
	return out
}

// ownEnviron is the environment AutoDoc was started with, without its spawner's identity: what a
// process AutoDoc starts on its own behalf inherits.
func ownEnviron() []string {
	drop := spawnerIdentity(os.Environ())
	return slices.DeleteFunc(os.Environ(), func(kv string) bool {
		name, _, _ := strings.Cut(kv, "=")
		return slices.Contains(drop, name)
	})
}
