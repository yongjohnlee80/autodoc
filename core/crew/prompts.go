package crew

import (
	"context"
	"time"
)

// The CLI kinds a crew session runs (Registration.Kind, Endpoint.Kind).
const (
	KindClaude   = "claude"
	KindCodex    = "codex"
	KindAgy      = "agy"
	KindOpenCode = "opencode"
)

// A permission prompt's report (docs/decisions/crew.md#the-human-inbox): a kind's adapter raises
// the session's permission attention when the agent shows a prompt, and clears it once answered.
// A window may press Enter after typing to an agent only while its prompts are reported and none
// shows now (crew.list's prompts_reported, and its attention), since an Enter into a permission
// prompt approves it. A report is trusted only while its reporter is known to be there.
type promptReporter int

const (
	reportsNone      promptReporter = iota
	reportsHooks                    // the CLI's own hooks, which the host writes: whenever its session is live and registered with prompts
	reportsHeartbeat                // a process of the agent's (a plugin, a bridge) that says it is alive
)

var reporters = map[string]promptReporter{
	KindClaude:   reportsHooks,     // AutoDoc's hooks: PermissionRequest raises; its own PostToolUse, UserPromptSubmit and Stop clear
	KindOpenCode: reportsHeartbeat, // AutoDoc's plugin: permission.asked raises, the last permission.replied clears
	KindAgy:      reportsHeartbeat, // the agy bridge: a trajectory step waiting on a permission raises, none clears
}

// reporterStale is how long a reporter's heartbeat (crew.attention alive) stands: a reporter that
// hasn't said it is alive since is taken for gone, and its session's prompts as unreported.
const reporterStale = 15 * time.Second

// ReporterAlive records that the calling session's prompt reporter is running (crew.attention
// alive), by the store's clock. It is kept in memory alone: a daemon restart forgets it, and the
// session's prompts read as unreported until its reporter speaks again.
func (m *Manager) ReporterAlive(ctx context.Context, credential string) error {
	return m.store.Read(ctx, func(tx Tx) error {
		s, err := m.Authenticate(tx, credential)
		if err != nil {
			return err
		}
		m.aliveMu.Lock()
		defer m.aliveMu.Unlock()
		if m.alive == nil {
			m.alive = map[string]time.Time{}
		}
		m.alive[s.ID] = tx.Now()
		return nil
	})
}

// promptsReported is whether live session s, of kind, has its permission prompts reported at now:
// Claude's hooks report whenever it is live, if its host wrote them (its registration's prompts,
// which a Claude Code too old for those hooks doesn't get: never assumed); a heartbeat reporter
// while its last heartbeat is within reporterStale; any other kind never.
func (m *Manager) promptsReported(kind string, s Session, now time.Time) bool {
	switch reporters[kind] {
	case reportsHooks:
		return s.Prompts
	case reportsHeartbeat:
		m.aliveMu.Lock()
		at, ok := m.alive[s.ID]
		m.aliveMu.Unlock()
		return ok && now.Sub(at) <= reporterStale
	}
	return false
}
