package crew

import "strings"

// GuideCommands are the guide's own autodoc commands, the ones a crew session's CLI is allowed to
// run with no permission prompt (docs/decisions/crew.md#wake-acceptance-not-tool-approvals): exactly
// these, each named, never `autodoc *`. The allowance is written only where the session's own CLI
// reads it (its folder's settings, or its setup's argv or env), never in the user's own config.
var GuideCommands = []string{
	"mail receive", "mail ack", "mail reply", "mail send", "mail inbox",
	"crew whoami", "crew list", "crew endpoint", "crew attention", "crew bridge",
}

// ClaudeAllow is Claude Code's permissions.allow for the guide's commands: one Bash rule per
// command, matching it and its arguments.
func ClaudeAllow() []string {
	out := make([]string, 0, len(GuideCommands))
	for _, c := range GuideCommands {
		out = append(out, "Bash(autodoc "+c+":*)")
	}
	return out
}

// IsClaudeAllow reports whether rule is one of AutoDoc's Claude allow rules, of this build's or an
// earlier one's: a host takes them out before writing this build's, and keeps every other rule.
func IsClaudeAllow(rule string) bool {
	return strings.HasPrefix(rule, "Bash(autodoc ") && strings.HasSuffix(rule, ":*)")
}

// OpenCodeAllow is OpenCode's permission.bash for the guide's commands: one allow per command,
// matching it and its arguments.
func OpenCodeAllow() map[string]any {
	out := make(map[string]any, len(GuideCommands))
	for _, c := range GuideCommands {
		out["autodoc "+c+"*"] = "allow"
	}
	return out
}
