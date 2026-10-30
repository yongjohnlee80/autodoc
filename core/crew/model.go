package crew

import (
	"context"
	"time"
)

// The rows the crew module reads and writes (ADR 1793271500 §4.2). Times are Unix nanoseconds; a
// nil *int64 is SQL NULL. Each engine's adapter maps them onto its tables.
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

	// Wake is a session's row of the wake scheduler's state machine (§4.7). The crew module
	// raises PendingGen; the scheduler owns the rest.
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

// WakeIdle is a wake row's state with no attempt out.
const WakeIdle = "idle"

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
// it, and every engine implements it. Every guard runs on the verb's own Tx (§4.4), so a takeover
// that commits between a verb's first check and its write fails the verb.
type Tx interface {
	// Now is the transaction's clock.
	Now() time.Time

	Host(id string) (Host, bool, error)
	PutHost(Host) error
	Scope(id string) (Scope, bool, error)
	PutScope(Scope) error

	Profile(id string) (Profile, bool, error)
	ActiveProfile(scope, codename string) (Profile, bool, error)
	PutProfile(Profile) error

	Session(id string) (Session, bool, error)
	SessionByInstance(host string, epoch int64, instance string) (Session, bool, error)
	LiveSession(profile string) (Session, bool, error)
	LiveSessionsOfScope(scope string) ([]Session, error)
	PutSession(Session) error

	Wake(session string) (Wake, bool, error)
	PutWake(Wake) error
}

// Store runs transactions over the crew tables, each one atomic: Write commits fn's changes or
// none of them, and Read sees one consistent state.
type Store interface {
	Write(ctx context.Context, fn func(Tx) error) error
	Read(ctx context.Context, fn func(Tx) error) error
}
