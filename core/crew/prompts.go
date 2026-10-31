package crew

// The CLI kinds a crew session runs (Registration.Kind, Endpoint.Kind).
const (
	KindClaude   = "claude"
	KindCodex    = "codex"
	KindAgy      = "agy"
	KindOpenCode = "opencode"
)

// promptReporter is who reports a kind's permission prompts (docs/decisions/crew.md#the-human-inbox):
// raising the session's permission attention when the agent shows one, and clearing it once
// answered. A kind is listed here with its adapter.
type promptReporter int

const (
	reportsNone       promptReporter = iota
	reportsAlways                    // the CLI's own hooks, written by the host: whatever the endpoint
	reportsByEndpoint                // the process that is the session's endpoint (a plugin, a bridge)
)

var reportsPrompts = map[string]promptReporter{
	KindClaude:   reportsAlways,     // the hooks AutoDoc writes: PermissionRequest and Notification raise; PostToolUse, UserPromptSubmit and Stop clear
	KindOpenCode: reportsByEndpoint, // AutoDoc's plugin: permission.asked raises, the last permission.replied clears
	KindAgy:      reportsByEndpoint, // the agy bridge: a trajectory step waiting on a permission raises, none clears
	KindCodex:    reportsNone,
}

// ReportsPrompts reports whether a session of kind reports its permission prompts now; reachable
// is whether the session can be woken (crew.list's reachable). Only into a session that reports
// them, and shows none now, may a window press Enter after typing to it, since an Enter into a
// permission prompt approves it. A kind whose reporter is its endpoint's own process (OpenCode's
// plugin, agy's bridge) reports nothing while it is unreachable: that process may not be running.
// An unknown kind reports none.
func ReportsPrompts(kind string, reachable bool) bool {
	switch reportsPrompts[kind] {
	case reportsAlways:
		return true
	case reportsByEndpoint:
		return reachable
	}
	return false
}
