package crew

// The CLI kinds a crew session runs (Registration.Kind, Endpoint.Kind).
const (
	KindClaude   = "claude"
	KindCodex    = "codex"
	KindAgy      = "agy"
	KindOpenCode = "opencode"
)

// reportsPrompts are the kinds whose adapter reports the agent's permission prompts
// (docs/decisions/crew.md#the-human-inbox): it raises the session's permission attention when the
// agent shows one, and clears it once answered. A kind turns true here with its adapter.
var reportsPrompts = map[string]bool{
	KindClaude:   true, // the hooks AutoDoc writes: PermissionRequest and Notification raise, PostToolUse, UserPromptSubmit and Stop clear
	KindCodex:    false,
	KindAgy:      false,
	KindOpenCode: true, // AutoDoc's plugin: permission.asked raises, the last permission.replied clears
}

// ReportsPrompts reports whether kind's sessions report their permission prompts: only into one
// that does, and shows none now, may a window press Enter after typing to it, since an Enter into
// a permission prompt approves it. Any kind not listed reports none.
func ReportsPrompts(kind string) bool { return reportsPrompts[kind] }
