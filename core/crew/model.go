package crew

import (
	"context"
	"time"
)

// The rows the crew module reads and writes (docs/decisions/crew.md#the-crew-and-mail-tables).
// Times are Unix nanoseconds; a nil *int64 is SQL NULL. Each engine's adapter maps them onto its
// tables.
type (
	// Host is a scope's owner: one process incarnation of an AutoDoc window or a Neovim, fenced by
	// Epoch. Its ID is the scope it holds.
	Host struct {
		ID          string
		Epoch       int64
		Incarnation []byte // sha256 of the holder's run token; empty when released
		Proc        string // "<boot id>:<pid ns>:<pid>:<start time>", "" when unknown
		Renewed     int64
	}

	// Scope is an identity namespace: "autovim:<project>", "nvim:<key>", "autodoc:<id>", "global".
	Scope struct {
		ID      string
		Kind    string // autovim | nvim | autodoc | global
		State   string // live | adopting | gone
		Created int64
	}

	// Profile is an agent's identity within a scope, (Scope, Codename).
	Profile struct {
		ID         string
		Scope      string
		Codename   string
		Kind       string // claude | codex | agy | opencode …; "" for the human
		Status     string // active | merged
		MergedInto string
		Created    int64
	}

	// Session is one running agent process registered under a host's epoch.
	Session struct {
		ID         string
		Profile    string
		Host       string
		Epoch      int64
		Instance   string
		SecretHash []byte
		ArgsDigest []byte
		Started    int64
		Ended      *int64
		EndedWhy   string // exit | takeover | reclaim | adopted | start_failed
	}

	// Endpoint is how a session is woken (docs/decisions/crew.md#wakers-and-the-wake-scheduler):
	// its CLI's channel, never a secret.
	Endpoint struct {
		Session    string
		Generation int64  // raised by every crew.endpoint
		Kind       string // claude | codex | agy | opencode
		Locator    string // JSON, kind-specific
		Registered int64
	}

	// Attention is a session waiting on the user: a permission prompt, or blocked
	// (docs/decisions/crew.md#the-human-inbox).
	Attention struct {
		Session string
		Owner   string // the host whose terminal shows the prompt
		Kind    string // permission | blocked
		Text    string
		Raised  int64
		Cleared *int64
	}

	// Wake is a session's row of the wake scheduler's state machine
	// (docs/decisions/crew.md#wakers-and-the-wake-scheduler). The crew module raises PendingGen;
	// the scheduler owns the rest.
	Wake struct {
		Session      string
		PendingGen   int64 // raised whenever a delivery becomes eligible for the session
		AnnouncedGen int64 // the PendingGen its last mail.receive had seen
		State        string
		Token        string
		AttemptGen   int64
		Failures     int64
		DaemonRun    string
		NextAt       *int64
	}
)

// A wake row's states (docs/decisions/crew.md#wakers-and-the-wake-scheduler): no attempt out; an
// attempt's call out; a notice accepted (or sent with an unknown outcome) and the session not yet
// received.
const (
	WakeIdle     = "idle"
	WakeInFlight = "in_flight"
	WakeAwaiting = "awaiting_receive"
)

// Scope states, host and session end reasons, and profile statuses.
const (
	ScopeLive     = "live"
	ScopeAdopting = "adopting"
	ScopeGone     = "gone"

	EndedExit        = "exit"
	EndedTakeover    = "takeover"
	EndedReclaim     = "reclaim"
	EndedAdopted     = "adopted"
	EndedStartFailed = "start_failed"

	ProfileActive = "active"
	ProfileMerged = "merged"
)

// Tx is one write (or read) transaction over the crew tables: the module's state machines run on
// it, and every engine implements it. Every guard runs on the verb's own Tx
// (docs/decisions/crew.md#sessions-and-the-guard), so a takeover that commits between a verb's
// first check and its write fails the verb.
type Tx interface {
	// Now is the transaction's clock.
	Now() time.Time

	Host(id string) (Host, bool, error)
	PutHost(Host) error
	Scope(id string) (Scope, bool, error)
	PutScope(Scope) error

	Profile(id string) (Profile, bool, error)
	ActiveProfile(scope, codename string) (Profile, bool, error)
	// ActiveProfiles are every active profile, by scope then codename.
	ActiveProfiles() ([]Profile, error)
	// Scopes are the scopes of kind, by id.
	Scopes(kind string) ([]Scope, error)
	PutProfile(Profile) error

	Session(id string) (Session, bool, error)
	SessionByInstance(host string, epoch int64, instance string) (Session, bool, error)
	LiveSession(profile string) (Session, bool, error)
	LiveSessionsOfScope(scope string) ([]Session, error)
	PutSession(Session) error

	Wake(session string) (Wake, bool, error)
	PutWake(Wake) error
	// WakesDue is the idle rows with pending_gen past announced_gen whose next_at is unset or at or
	// before now, of a live session with an endpoint of one of kinds (the kinds the scheduler has
	// wakers for): the sessions a wake may be reserved for. An ended session's row, an
	// endpoint-less one's, or one whose endpoint no waker serves is never due, so such rows can't
	// crowd out the live ones under the limit; no kinds, none. limit must be > 0, here and in every
	// limited read.
	WakesDue(now int64, kinds []string, limit int) ([]Wake, error)
	// WakesTimedOut is the awaiting_receive rows whose next_at is at or before now (limit > 0).
	WakesTimedOut(now int64, limit int) ([]Wake, error)
	// WakesInFlight is every in_flight row, for a daemon start's recovery.
	WakesInFlight() ([]Wake, error)
	// NextWakeAt is the earliest next_at among the rows that have work: idle with pending_gen past
	// announced_gen and due by WakesDue's other terms (a live session, an endpoint of one of
	// kinds), or awaiting_receive of any kind (its timeout ends an attempt already made); false
	// when none has a deadline. A row no look can act on never sets the timer, or a past deadline
	// on it would have the loop look again at once, forever.
	NextWakeAt(kinds []string) (int64, bool, error)

	Endpoint(session string) (Endpoint, bool, error)
	PutEndpoint(Endpoint) error
	DeleteEndpoint(session string) error

	Attention(session string) (Attention, bool, error)
	PutAttention(Attention) error

	// AddInboxChange journals one change of the human's feed (inbox_change,
	// docs/decisions/crew.md#the-human-inbox), in the same transaction as the change itself.
	AddInboxChange(kind, ref string) error
}

// Store runs transactions over the crew tables, each one atomic: Write commits fn's changes or
// none of them, and Read sees one consistent state.
type Store interface {
	Write(ctx context.Context, fn func(Tx) error) error
	Read(ctx context.Context, fn func(Tx) error) error
}
