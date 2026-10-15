package todo

import "time"

// Task is one task file, decoded. The frontmatter fields carry their YAML names; Path is the
// file's place in the store and Rev the SHA-256 of its bytes, which every mutation must name.
type Task struct {
	ID            string       `json:"id"`
	Version       int          `json:"version"`
	Status        string       `json:"status"`
	Title         string       `json:"title"`
	Due           string       `json:"due,omitempty"`
	Priority      string       `json:"priority,omitempty"`
	Assignee      string       `json:"assignee,omitempty"`
	Tags          []string     `json:"tags,omitempty"`
	ADR           []string     `json:"adr,omitempty"`
	Review        []string     `json:"review,omitempty"`
	Blocked       []string     `json:"blocked,omitempty"`
	Created       string       `json:"created"`
	Updated       string       `json:"updated"`
	StatusChanged string       `json:"status_changed"`
	CompletedAt   string       `json:"completed_at,omitempty"`
	ArchivedAt    string       `json:"archived_at,omitempty"`
	Errors        []ErrorEntry `json:"errors,omitempty"`
	Description   string       `json:"description"`
	// Extra holds a newer format's fields that have no field here yet: a string, []string or
	// []map[string]string, by the field's name.
	Extra map[string]any `json:"extra,omitempty"`

	Path string `json:"path"`           // store-relative: "open/<id>.md", "archived/2026/05/<id>.md"
	Rev  string `json:"rev"`            // SHA-256 hex of the file's bytes
	Body string `json:"body,omitempty"` // Get only: the file's Markdown after the frontmatter, as written

	blank []string // optional fields the file writes as "": kept, as auto-core keeps them
}

// ErrorEntry is one errors[] entry: a reference the last refresh could not resolve.
type ErrorEntry struct {
	Field    string `json:"field"` // "adr[0]", "review[2]", "blocked[1]"
	Code     string `json:"code"`  // "not-found" | "unresolved-variable"
	Message  string `json:"message"`
	Detected string `json:"detected"` // when it was first seen; kept while the error persists
}

// Spec is a new task. Title is required; the rest default as auto-core's add does.
type Spec struct {
	Title       string   `json:"title"`
	ID          string   `json:"id,omitempty"` // "" derives <date>-<slug> from Title
	Description string   `json:"description,omitempty"`
	Status      string   `json:"status,omitempty"` // "" is open
	Due         string   `json:"due,omitempty"`
	Priority    string   `json:"priority,omitempty"`
	Assignee    string   `json:"assignee,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	ADR         []string `json:"adr,omitempty"`
	Review      []string `json:"review,omitempty"`
	Blocked     []string `json:"blocked,omitempty"`
}

// ListOpts filters List. Buckets names active buckets ("open"), "archived" for the archive's
// months, or one month ("archived/2026/05"); none means the active buckets.
type ListOpts struct {
	Buckets   []string `json:"buckets,omitempty"`
	Tag       string   `json:"tag,omitempty"`
	Assignee  string   `json:"assignee,omitempty"`
	Priority  string   `json:"priority,omitempty"`
	HasErrors bool     `json:"has_errors,omitempty"`
}

// Listing is a store's tasks and the files that are not tasks; Partitions answers a request for
// "archived". Cursor is the change log's head.
type Listing struct {
	Tasks      []Task      `json:"tasks"`
	Malformed  []Malformed `json:"malformed"`
	Partitions []Partition `json:"partitions,omitempty"`
	Cursor     int64       `json:"cursor"`
}

// Malformed is a file in a bucket that does not decode or validate.
type Malformed struct {
	Path   string `json:"path"` // store-relative
	Reason string `json:"reason"`
}

// RefreshSummary counts what a refresh did, as auto-core's refresh counts it.
type RefreshSummary struct {
	Scanned   int `json:"scanned"`
	Moved     int `json:"moved"`
	Archived  int `json:"archived"`
	ErrorsSet int `json:"errors_set"`
	Rewritten int `json:"rewritten"`
	Malformed int `json:"malformed"`
}

// Resolved is where an At points: the store's stable id and its path now.
type Resolved struct {
	StoreID  string `json:"store_id"`
	Store    string `json:"store"` // the store directory's canonical path
	Root     string `json:"root"`
	Override string `json:"override,omitempty"`
	Writable bool   `json:"writable"`
	Claimed  bool   `json:"claimed"`
}

// Opened is a store Open found, or Scaffold made.
type Opened struct {
	StoreID    string `json:"store_id"`
	Store      string `json:"store"`
	Scaffolded bool   `json:"scaffolded"`
}

// StoreInfo is one store the daemon knows.
type StoreInfo struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Roots   []string  `json:"roots"`
	Claimed bool      `json:"claimed"`
	LastUse time.Time `json:"last_use"`
}

// Var is one variable a ref may start with: $KB_ROOT, $WORKSPACE, $HOME, or a user's.
type Var struct {
	Name    string `json:"name"`
	Value   string `json:"value,omitempty"` // the effective value; "" for none here
	Builtin bool   `json:"builtin"`
	Derived string `json:"derived,omitempty"` // a built-in's value from the store's location, before overrides
	Source  string `json:"source,omitempty"`  // where Value came from: machine, store, derived, global or env
	Machine bool   `json:"machine,omitempty"` // declared machine-only by the store's file
}

// ChangesPage is a consumer's next changes, or a resync: the consumer was unregistered while it
// was away, so it gets the store's current snapshot instead.
type ChangesPage struct {
	Changes  []Change      `json:"changes,omitempty"`
	Resync   bool          `json:"resync,omitempty"`
	Snapshot []SnapshotRow `json:"snapshot,omitempty"`
	Cursor   int64         `json:"cursor"`
}

// Change kinds.
const (
	KindAdd             = "add"
	KindUpdate          = "update"
	KindStatus          = "status"
	KindAssign          = "assign"
	KindArchive         = "archive" // refresh's 28-day rule
	KindRefresh         = "refresh" // refresh's other rewrites and moves
	KindRemove          = "remove"
	KindRemoveMalformed = "remove_malformed"
	KindMoveStore       = "move_store"
)

// File operations a change makes, which decide its recovery.
const (
	OpCreate    = "create"
	OpInPlace   = "in-place"
	OpMove      = "move"
	OpDelete    = "delete"
	OpStoreMove = "store-move"
)

// Change row states.
const (
	StatePending   = "pending"
	StateStaged    = "staged" // a delete whose file is in the store's trash
	StateCommitted = "committed"
)

// Change is one row of a store's change log.
type Change struct {
	Seq      int64     `json:"seq"`
	Store    string    `json:"store"` // the store's id
	ID       string    `json:"id,omitempty"`
	Kind     string    `json:"kind"`
	Op       string    `json:"op"`
	From     string    `json:"from,omitempty"` // status: the old status; assign: the old assignee; move_store: the old path
	To       string    `json:"to,omitempty"`
	Fields   []string  `json:"fields,omitempty"` // update and refresh: the fields that changed
	Reason   string    `json:"reason,omitempty"`
	Hand     bool      `json:"hand,omitempty"` // found by the snapshot diff: an edit made outside the daemon
	FromPath string    `json:"from_path,omitempty"`
	ToPath   string    `json:"to_path,omitempty"`
	FromRev  string    `json:"from_rev,omitempty"`
	ToRev    string    `json:"to_rev,omitempty"`
	State    string    `json:"state"`
	At       time.Time `json:"at"`
}

// SnapshotRow is a task file as the daemon last saw it. Fields digests each frontmatter field
// and the description, so a hand edit can be reported field by field.
type SnapshotRow struct {
	Path     string            `json:"path"`
	ID       string            `json:"id"`
	Rev      string            `json:"rev"`
	Status   string            `json:"status"`
	Assignee string            `json:"assignee,omitempty"`
	Fields   map[string]string `json:"fields,omitempty"`
}

// Consumer is a change-log reader and how far it has acknowledged. One silent too long is
// unregistered, and kept so that its return gets a resync. It stays unregistered, answered the
// resync again on every ask, until it acknowledges the resync's cursor: Resync, the cursor of the
// resync last answered, and SeenAt, when.
type Consumer struct {
	Name         string    `json:"name"`
	Acked        int64     `json:"acked_seq"`
	SeenAt       time.Time `json:"seen_at"`
	Unregistered bool      `json:"unregistered,omitempty"`
	Resync       int64     `json:"resync_seq,omitempty"`
}

// resyncing reports an unregistered consumer answered a resync within ConsumerSilence that it has
// not acknowledged yet. An unregistered consumer's SeenAt moves only when it is answered a resync,
// so one dropped and not back since was last seen more than ConsumerSilence ago.
func (c Consumer) resyncing(now time.Time) bool {
	return c.Unregistered && now.Sub(c.SeenAt) <= ConsumerSilence
}

// Event is something the host records in sys.events that no verb's own event covers.
type Event struct {
	// "todos.ambiguous", "todos.store_move_blocked", "todos.consumer_dropped", "todos.set_dir",
	// "todos.register", "todos.refresh_failed"; and "todos.changed" for a daily refresh that
	// changed a store, as the verbs log it
	Kind      string
	Workspace string // the store's workspace, for a todos.changed of a workspace's store
	Store     string // the store's id
	Path      string
	ID        string
	Detail    string
}
