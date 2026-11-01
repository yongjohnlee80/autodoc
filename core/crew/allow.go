package crew

import "slices"

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

// earlierClaudeAllow are the exact rules earlier builds wrote and this one no longer does, so a
// host still takes them out. None yet: the allowance's first build is this one.
var earlierClaudeAllow = []string{}

// IsClaudeAllow reports whether rule is one of AutoDoc's Claude allow rules, of this build's or an
// earlier one's, exactly: a host takes them out before writing this build's, and keeps every other
// rule, the user's own `Bash(autodoc search:*)` included.
func IsClaudeAllow(rule string) bool {
	return slices.Contains(ClaudeAllow(), rule) || slices.Contains(earlierClaudeAllow, rule)
}

// openCodeAllowProven is whether SetupFor writes OpenCode's allowance (OpenCodeWithAllow) into a
// session's OPENCODE_CONFIG_CONTENT. An allow rule is the one place the crew widens an agent's
// permissions, so it ships only once a live probe shows a compound command can't ride it: the KB
// crew harness's probe-compound.sh, run on OpenCode. Claude Code's passed; OpenCode's hasn't run
// yet (no model was available), so OpenCode agents keep their own policy and prompt for the
// guide's commands. A passing probe flips this, with its ledger.
const openCodeAllowProven = false

// OpenCodeAllow is OpenCode's permission.bash for the guide's commands: two allows per command,
// the command alone and the command with arguments, so neither matches another command that
// merely starts with its name (`autodoc mail sendmail`).
func OpenCodeAllow() map[string]any {
	out := make(map[string]any, 2*len(GuideCommands))
	for _, c := range GuideCommands {
		out["autodoc "+c] = "allow"
		out["autodoc "+c+" *"] = "allow"
	}
	return out
}
