package todo

import (
	"context"
	"strings"
	"time"
)

// Journal is the daemon's durable state for todos: each store's change log, its snapshot, its
// consumers' cursors, and the store and root records. A store is keyed by a stable id, never by
// its path, so moving a store keeps its claim, snapshot and cursors. core/store implements it on
// SQLite; todotest has an in-memory one.
//
// A change row exists if and only if its file change happened. A mutation calls Begin before its
// first file step and Commit after its last; recovery settles any row still pending (or staged)
// when the store is next opened. Commit and Record apply their snapshot edit in the same
// transaction that writes the row.
type Journal interface {
	// Begin inserts c as pending and returns its seq. Seqs increase across every store.
	Begin(ctx context.Context, c Change) (int64, error)
	// Stage marks a pending delete staged: its file is in the store's trash.
	Stage(ctx context.Context, seq int64) error
	// Commit marks a pending or staged row committed and applies e, in one transaction.
	Commit(ctx context.Context, seq int64, e Edit) error
	// Drop deletes a pending or staged row.
	Drop(ctx context.Context, seq int64) error
	// Pending lists a store's pending and staged rows, oldest first.
	Pending(ctx context.Context, store string) ([]Change, error)
	// Record inserts c already committed and applies e, in one transaction: an edit found by the
	// snapshot diff.
	Record(ctx context.Context, c Change, e Edit) (int64, error)
	// Head is the highest committed seq of a store; 0 for none.
	Head(ctx context.Context, store string) (int64, error)
	// After lists up to limit committed rows of a store with seq > after, in order.
	After(ctx context.Context, store string, after int64, limit int) ([]Change, error)
	// Prune deletes a store's committed rows older than before whose seq is at most upTo.
	Prune(ctx context.Context, store string, before time.Time, upTo int64) (int, error)

	// Snapshot is the store's snapshot: one row per task file.
	Snapshot(ctx context.Context, store string) ([]SnapshotRow, error)
	// Baseline replaces the store's snapshot with rows, logging no change: the first sight of a
	// store.
	Baseline(ctx context.Context, store string, rows []SnapshotRow) error

	// Consumers lists the store's registered consumers.
	Consumers(ctx context.Context, store string) ([]Consumer, error)
	// PutConsumer registers or updates a consumer.
	PutConsumer(ctx context.Context, store string, c Consumer) error

	// Settings reads the store and root records and the user's vars.
	Settings(ctx context.Context) (Settings, error)
	// UpdateSettings applies fn to the settings and saves them, in one transaction; an error
	// from fn saves nothing.
	UpdateSettings(ctx context.Context, fn func(*Settings) error) error
}

// Edit is what a committed row changes besides itself.
type Edit struct {
	Store  string        // the store's id
	Put    []SnapshotRow // rows to add or replace, keyed by path
	Delete []string      // paths whose rows go
	Repath *Repath       // a store move: the store's new path
}

// Repath moves a store: its record's path becomes To, and every root resolving to From now
// resolves to To through an override. Nothing keyed by the store's id changes.
type Repath struct {
	From, To string
}

// Settings are the daemon's records of stores and roots, and the user's vars.
type Settings struct {
	Stores map[string]StoreRecord // by store id
	Roots  map[string]RootRecord  // by canonical root
	Vars   map[string]string
}

// StoreRecord is a store: a stable id given the first time a root resolves to it, and its
// canonical path now.
type StoreRecord struct {
	ID          string
	Path        string
	Claimed     bool
	ClaimedAt   time.Time
	LastUse     time.Time
	RefreshedAt time.Time // the daily refresh's last run (RefreshDue); zero for never
}

// RootRecord is a project root the daemon knows: one registered with Register, one whose store
// SetDir moved, or one a client opened.
type RootRecord struct {
	Root         string
	Override     string    // the store directory SetDir chose; "" for <root>/.todo-list
	Registered   time.Time // zero unless Register enrolled it
	KBRoot       string    // $KB_ROOT for a root that is not a workspace, from Open
	LastResolved time.Time
}

// storeByPath is the id of the store record at path.
func (s Settings) storeByPath(path string) (StoreRecord, bool) {
	for _, r := range s.Stores {
		if r.Path == path {
			return r, true
		}
	}
	return StoreRecord{}, false
}

// ApplyRepath is Repath's effect on settings, for Journal implementations to call inside Commit.
func (s *Settings) ApplyRepath(store string, r Repath) {
	if rec, ok := s.Stores[store]; ok {
		rec.Path = r.To
		s.Stores[store] = rec
	}
	for root, rr := range s.Roots {
		if rr.Override == r.From || (rr.Override == "" && root+"/"+todoDir == r.From) {
			rr.Override = r.To
			s.Roots[root] = rr
		}
	}
	// the root whose default store moved, when the daemon had no record of it yet
	if root, ok := strings.CutSuffix(r.From, "/"+todoDir); ok {
		if _, known := s.Roots[root]; !known {
			if s.Roots == nil {
				s.Roots = map[string]RootRecord{}
			}
			s.Roots[root] = RootRecord{Root: root, Override: r.To}
		}
	}
}
