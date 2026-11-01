// Package rpc is AutoDoc's msgpack-RPC API
// (docs/decisions/contract.md#one-rpc-api-for-every-client): a projection of core, with no business
// logic of its own. Every client speaks it: the TUI, the GUI, AutoVim's auto-core.
//
// A session must say sys.hello with a protocol this build serves (MinProtocol through Protocol)
// before any other verb answers; a hello with another refuses the session for good. The TUI is held
// to this build's Protocol exactly. The server only answers: it never sends a notification or a
// request, so a request/response-only client (auto-core's) can use every verb.
package rpc

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/msgpack"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/fsbounds"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
	"github.com/yongjohnlee80/autodoc/internal/profiling"
)

// Protocol is the API's version. A client declares the one it was written for, which this build
// serves from MinProtocol up; any change to the verbs, their parameters or their results bumps it
// (testdata/contracts.json holds the contracts). sys.hello and
// sys.shutdown are frozen across protocols: a newer client stops an older daemon with them, by
// declaring the daemon's number, to start the installed one in its place.
//
// Protocol 7 adds workspace.set_patterns for workspace admission changes. Protocol 8 adds frontmatter
// schemas (workspace.set_schema, doc.validate, search.query's facets, workspace.list's schema and
// index.status's diagnosed), doc.outline, workspace.set_text_extensions with workspace.list's
// text_extensions, sys.events (sys.hello answers the client's token and the log's head), and
// workspace.set_provider with workspace.list's provider. Protocol 9 adds workspace.configure, every
// setting saved at once, sys.capabilities, and workspace.list's databases
// (docs/decisions/store.md#workspace-settings-saved-together). Protocol 10 adds a build's
// registrations (docs/decisions/search.md#registered-chunkers-and-derivers): workspace.list's
// text_collisions; the documents a daemon holds, index.status's held, held_stale and
// held_unchecked, search.query's hold on a hit, and index.reindex's refusal of a held document;
// sys.capabilities' registrations, always there. Protocol 11 adds the ranker models
// (docs/decisions/search.md#re-ranking-with-one-global-ranker): the ranker.* verbs, search.query's
// rank and a ranked hit's rank_score, and sys.capabilities' ranker. Protocol 12 adds search.query's
// stages: the stages a search runs, asked for in place of a mode, and how its answer was made.
// Protocol 13 adds index.documents, search.query's line_start and line_end on a hit,
// sys.capabilities' verbs, and sys.hello's server_protocol, min_protocol and store_id. Protocol 14
// adds links from frontmatter relations (the kinds supersedes, superseded_by, amends, related,
// sources and adr) to graph.links, graph.backlinks, graph.neighborhood and graph.unresolved, and
// their kinds option; a session below 14 sees the body links alone, its graph unchanged. It also
// adds graph.unresolved's reason cycle (a supersession loop), workspace.configure's abstract_chunk
// and demote_superseded with workspace.list's, search.query's retrieval option and a hit's
// superseded_by.
// Protocol 15 adds index.documents' diagnosed: the documents whose frontmatter the schema
// diagnoses, each with its diagnostics; a session below 15 is refused the option. Protocol 16
// (docs/decisions/store.md#protocol-16-gates-the-deleting-verbs): embedding.cancel_switch deletes
// the partial target's vectors and embedding.remove the removed provider's model's, so a session
// below 16 is refused those two verbs; index.purge_model refuses the active model and the target
// saying what to do instead. Protocol 17 is files outside every workspace
// (docs/decisions/editor.md#files-outside-every-workspace): file.read, file.write and file.locate,
// a local peer's files outside every workspace. Protocol 18 adds graph.resolve: where a link as
// written reaches, for an editor following one not yet indexed.
// Protocol 19 is raw HTML (docs/decisions/editor.md#html-files-open-raw-or-simplified):
// doc.read_raw, doc.write_raw, file.read_raw and file.write_raw, an HTML file's own bytes where
// doc.read and file.read give its derived text; a session below 19 sees them as unknown methods.
// Protocol 20 is verb contracts
// (docs/decisions/contract.md#features-as-modules-with-verb-contracts) and the todo system
// (docs/decisions/todos.md#the-daemon-owns-the-todo-system): sys.contracts, and the todos.* verbs.
// Protocol 21 makes file.write and file.write_raw on a file a workspace indexes that workspace's
// doc.write and doc.write_raw, indexed at once, where they wrote round it through "/"; file.locate
// resolves symlinks. No verb is added, and every session gets the new behaviour: the old one was
// the bypass. Protocol 22 is one answer from every route to a store
// (docs/decisions/todos.md#one-answer-from-every-route-to-a-store): todos.migrate_refs, which a
// session below 22 sees as an unknown method; todos.set_var's optional {at, scope}; todos.vars'
// derived, source and machine. Every session resolves a todo store's variables from the store,
// whichever route it names, and todos.open ignores a kb_root: the per-route values were the bug.
// Protocol 23 is graph-only indexing (docs/decisions/todos.md#graph-only-indexing):
// workspace.configure's graph_only and todo_search, with workspace.list's; the review and blocked
// relations, and a todo task's external links (graph.links' external), which a session below 23
// does not see, its graph unchanged. Every session gets a todo store's tasks indexed graph-only:
// out of search, in the graph.
// Protocol 24 (docs/decisions/todos.md#variables-belong-to-the-workspace): variables belong to the
// workspace. It adds workspace.vars, workspace.set_var, workspace.of and todos.set_kb, and a
// variable's conflict; a session below 24 reads a KB-file value's source as "store", as before.
// Protocol 25 is process stats (docs/decisions/editor.md#process-stats): sys.stats, the daemon's
// own process (its memory, its CPU so far, its goroutines and its start), which a session below 25
// does not have.
// Protocol 26 is the workspace map (docs/decisions/search.md#the-workspace-map): graph.map, a
// workspace's documents (each one's title, type and whether another supersedes it) and the resolved
// links among them, or a root's neighbourhood.
// Protocol 27 is the KB type scaffold
// (docs/decisions/contract.md#the-daemon-writes-the-scaffold-create-only): workspace.scaffold, a KB
// type's files created under a workspace's root, which a session below 27 does not have.
// Protocol 28 (docs/decisions/search.md#the-workspace-map): graph.map's nodes carry their tags, for
// the GlobeMap's colour rules, and its answer the schema's document types, a node's type its
// discriminator's value, for the Mindmap's shapes; a session below 28 reads its answer as before.
// Protocol 29 is the claim at the first write
// (docs/decisions/todos.md#a-store-is-claimed-by-its-first-write): a todo store is AutoDoc's from
// its first write, todos.add making it if it is missing, and todos.resolve, todos.stores,
// todos.set_dir and todos.move_store say whether todos.release gave it back (released);
// todos.unclaimed now means only that. A session below 29 ignores released. Every session gets the
// claim at the first write: the refusal was the bug. So does every session's read of a registered
// root's store not made yet: an empty store's answer, where not_a_store was.
// Protocol 30 is the assigner's scope
// (docs/decisions/todos.md#an-assignment-carries-its-assigners-scope): todos.assign's optional
// scope, the assigner's, kept on the assign change (todos.changes' scope) and the task's snapshot
// row (a resync's), and never read by AutoDoc. A session below 30 sends none and ignores the key;
// its assignments have no scope.
// Protocol 31 (docs/decisions/search.md#embedding-setup): the embedding setup's host.probe,
// embedding.catalog, embedding.estimate, embedding.pull, embedding.pull_status and
// embedding.pull_cancel, which a session below 31 sees as unknown methods.
// Protocol 32 is sys.profile: the daemon's memory monitor (--profile's) started, stopped or asked
// about at runtime, for a local peer; a session below 32 sees an unknown method.
// Protocol 33 is todos.changes' optional scoped: a consumer that reads each assignment's scope and
// delivers only its own says so; one that doesn't gets a scoped assignment without its assignee
// (the change's to, a resync row's assignee). It is the one exception to "a result keeps its
// meaning", for every session alike: a notifier that predates the
// scope, still running in a Neovim after auto-core's upgrade, delivered every bare assignee in
// every project. Without the assignee it delivers none; an unscoped assignment is as before.
// Protocol 34 (docs/decisions/contract.md#a-gated-scripts-files-stay-in-the-workspace): a gated Lua
// script's bounds, {within, protect?}, one parameter past any verb's own, which the TUI's gate sets
// on every call a gated script makes. The daemon holds every file the call reaches to them
// (core/fsbounds): inside the workspace, none of it hidden or AutoDoc's own, a write in within's
// workspace only. A session below 34 sends none, and every call of any session without them is
// unbounded, as before.
// Protocol 35 is document listing in the store
// (docs/decisions/search.md#document-listing-in-the-store): index.documents orders and filters in
// the store and pages by a key, so its next is an opaque cursor (a string; "" when no more follow)
// and after takes it back. It is the second exception to "a result keeps its meaning", for every
// session alike: the integer offset is retired at once, not
// kept for a protocol window, so a session that declared an older protocol gets the cursor too, and
// a non-zero integer after is refused saying what replaced it. Every known client echoes next
// without reading it.
// Protocol 36 is unrooted maps in pages (docs/decisions/search.md#unrooted-maps-in-pages): a
// graph.map without a root comes a page at a time (opts.after and opts.limit, and next), each page
// with the links whose source is on it, since the whole map in one reply fails past the transport's
// message limit. Unlike 35, a session below 36 keeps the one reply, and is
// refused after and limit.
// Protocol 37 is the crew (docs/decisions/crew.md#the-crew-registry-and-durable-mail): the crew,
// mail and inbox modules (crew.Protocol, the one place their verbs' Since is set), after 34 above,
// and the 35 and 36 that AP-5 and AP-6 claim. A session below 37 sees their verbs as unknown
// methods.
// Protocol 38 is the agy waker (docs/decisions/crew.md#wakers-and-the-wake-scheduler):
// crew.endpoint's optional secret, an agy session's language-server token, which the daemon holds
// in memory alone for that endpoint (never stored), and which any other kind refuses. A session
// below 38 sends none, and its crew.endpoint is as before.
// Protocol 39 is AM-4's sys.check: the store's whole check, run by the daemon that serves it, so
// --check-store never stops a window.
// Protocol 40 is the OpenCode waker (docs/decisions/crew.md#wakers-and-the-wake-scheduler):
// crew.endpoint's opencode locator is the AutoDoc plugin's socket, its opencode process and the
// session ({socket, pid, start, session}); the shape before it, without the socket, is refused. No
// client registered one before.
// Protocol 41 is the user's own sends (docs/decisions/crew.md#the-human-inbox): inbox.send, a
// message or request from the window that holds a scope (its scope, run and epoch, guarded as a
// host verb is) whose sender is the user, set by the daemon alone. A session below 41 sees it as an
// unknown method, and its window types the text into the agent's prompt as before. With it,
// crew.attention's alive (a session's permission-prompt reporter is running) and crew.list's
// prompts_reported (its prompts are reported now), which a window reads before pressing Enter into an
// agent it typed to, and its why (why a session can't be woken now); crew.register's prompts, that
// the host wrote the hooks that report a Claude session's prompts, without which its
// prompts_reported is false; and a wake that finds the session's endpoint gone for certain makes it
// unreachable at once.
// Protocol 42 is the fallback (docs/decisions/crew.md#the-fallback-exactly-once): mail.fallbacks,
// a host's long-poll for the jobs of its scope's deliveries not received in time, which also makes
// the host take them in its epoch, and mail.fallback_done, its report of each. A host below 42
// never takes jobs, so its agents' mail stays queued, and an unrung inbox.send is withdrawn for the
// window to type, as before.
// Protocol 43 is each session's own mail mode (docs/decisions/crew.md#the-crew-mcp-server):
// crew.register's mail_via (cli or mcp, as the session's setup delivered it), which its wake
// notice follows, crew.list's mail_via, and crew.guide's mail_via. A session below 43 sends none,
// and its mail is read as its kind's (crew.MailViaOf), as before.
// Protocol 44 is the fallback in the human's feed (docs/decisions/crew.md#the-fallback-exactly-once):
// inbox.fallback, the job a feed change of kind fallback, fallback_done or fallback_unknown names,
// with whether its host holds its scope, so a window says a job waits for an absent host, and gives
// the text of one whose outcome is unknown; mail.fallback_done of a delivered job journals
// fallback_done.
// Protocol 45 is mail.show (docs/decisions/crew.md#durable-mail): a delivery the caller holds,
// read again under its lease, which stays as it was, so an agent whose reader cut a body short
// reads it whole without asking its sender to resend. A session below 45 has no such verb.
const Protocol int64 = 45

// MinProtocol is the oldest protocol this build still serves. A session keeps the protocol it
// declared: a verb added after it answers as an unknown method, as an older daemon would, and every
// request and result it knew keeps its shape and meaning; a result may only gain keys, which a
// client ignores. A change that is not an addition raises MinProtocol, or keeps the old behaviour
// for sessions below it.
const MinProtocol int64 = 12

// TUIName is the name the TUI says hello with. It is admitted at this build's Protocol only: the
// TUI is the same binary as its daemon, and a mismatch is its cue to offer a restart.
const TUIName = "autodoc-tui"

// CLIName is the name `autodoc --call` and `--print-endpoint --ensure` say hello with: a client
// like any other, admitted across MinProtocol to Protocol, so its events are attributed to it and an
// agent's shell survives a daemon one protocol apart.
const CLIName = "autodoc-cli"

// ServerName is what sys.hello answers as "server", so a probe tells AutoDoc from another occupant.
const ServerName = "autodoc"

// MaxMessage is the largest message either way: a document plus its envelope (docs.MaxSize).
const MaxMessage = 4 << 20

// Session keys.
const (
	sessHello    = "hello"    // true once a compatible sys.hello was said
	sessRefused  = "refused"  // true after a hello with another protocol: the session is spent
	sessProtocol = "protocol" // the protocol the session's hello declared
)

// Databases is a workspace's database settings as workspace.list reports them: never a DSN, only
// what each connection points at (store.ConnectionSummary).
type Databases struct {
	UID, Destination, VectorIndex string
	ViewArgs                      map[string]any
	Connections                   []store.ConnectionSummary
}

// WorkspaceInfo is a workspace given whole, each answer a value or a closure of its own: what
// Static serves, for rpc.Fixed and tests. The daemon implements Workspace itself.
type WorkspaceInfo struct {
	Name, Root       string
	EmbeddingPolicy  string
	EmbeddingMode    func() string
	SectionTokens    int
	SectionSize      func() int // live workspace preference, when managed by the daemon
	Include, Exclude []string
	Err              error
	Index            *index.Indexer
	Docs             *docs.Docs
	Files            *docs.Docs // every file of the root (Workspace.Files); nil: a bounded file.* finds none
	Following        func() follow.Status
	// Warming is what the workspace waits on before it answers fully — a provider being set up, a
	// restart to take one — for index.status; nil or empty when it is ready.
	Warming        func() []string
	Searched       func() // any client searched this workspace
	EmbeddingQueue func() (state, behind string)
	// FrontmatterSchema is the workspace's frontmatter schema
	// (docs/decisions/search.md#frontmatter-schemas): the active one, nil for none, and how its file
	// stands. nil when the server keeps no schemas.
	FrontmatterSchema func() (*schema.Schema, SchemaStatus)
	// TextExtensions are the workspace's own plain-text extensions
	// (docs/decisions/search.md#a-workspaces-own-text-types); nil for none.
	TextExtensions func() []string
	// TextCollisions are those of its text extensions a registration of the build reads instead
	// (docs/decisions/search.md#file-kinds-and-their-precedence); nil for none.
	TextCollisions func() []string
	// Databases are the workspace's identity, destination and database connections
	// (docs/decisions/store.md#databases-attach-to-a-workspace), read from the store when listed; nil
	// when the server keeps none.
	Databases func() Databases
	// Provider is the workspace's own embedding provider, when it has one
	// (docs/decisions/search.md#workspace-providers-and-peer-changes).
	Provider ProviderChoice
}

// ProviderChoice is which embedding provider a workspace uses: its own (Override, by name), or the
// daemon's when Override is "". Err says why its own is not set up, so it searches by words.
type ProviderChoice struct {
	Override string
	Err      string
}

// SchemaStatus is how a workspace's schema file stands: the stored path ("" for none), whether a
// valid schema is active and how many fields it declares, and why the file is not valid now (Err,
// on Line when it is one line's). An invalid edit leaves the last valid schema active.
type SchemaStatus struct {
	Path   string
	Active bool
	Fields int
	Err    string
	Line   int
}

// Workspaces is the daemon's set of workspaces: what the API serves, and what the workspace verbs
// change. The daemon's implementation keeps them in the store and starts and stops each one's
// indexer and follower.
type Workspaces interface {
	// List is every workspace, by name.
	List() []Workspace
	// Get is the workspace named name.
	Get(name string) (Workspace, bool)
	// Add creates a workspace and starts serving it.
	Add(ctx context.Context, w workspace.Spec) (Workspace, error)
	// Rename gives a workspace a new name.
	Rename(ctx context.Context, name, to string) error
	// Remove stops serving a workspace and deletes its index; its files stay.
	Remove(ctx context.Context, name string) error
}

// Fixed is a set of workspaces that never changes: the workspace verbs answer that it cannot.
func Fixed(ws ...Workspace) Workspaces {
	f := fixed{}
	for _, w := range ws {
		f[w.Name()] = w
	}
	return f
}

type fixed map[string]Workspace

// errFixed is a change asked of a fixed set of workspaces.
var errFixed = fmt.Errorf("%w: this set of workspaces is fixed", errs.ErrUnsupported)

func (f fixed) List() []Workspace {
	out := make([]Workspace, 0, len(f))
	for _, w := range f {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}
func (f fixed) Get(name string) (Workspace, bool) { w, ok := f[name]; return w, ok }
func (fixed) Add(context.Context, workspace.Spec) (Workspace, error) {
	return nil, errFixed
}
func (fixed) Rename(context.Context, string, string) error {
	return errFixed
}
func (fixed) Remove(context.Context, string) error {
	return errFixed
}

// Preferences are what a client keeps between runs, by name: the store's. A server given none
// answers the preference verbs that it cannot.
type Preferences interface {
	Preferences(ctx context.Context) (map[string]string, error)
	SetPreference(ctx context.Context, name, value string) error
}

// Server is the API over the daemon's workspaces.
type Server struct {
	reg          registrations.Tables // the build's registrations, sys.capabilities reports them
	files        *docs.Docs           // a local peer's files outside every workspace (WithFiles); nil: none
	guard        *fsbounds.Guard      // a gated script's bounds, for file.* (WithGuard); nil: every bounded file.* refused
	profiler     *profiling.Profiler  // the daemon's own monitor, for sys.profile (WithProfiler); nil: none
	rpc          *golibrpc.Server
	workspaces   Workspaces
	preferences  Preferences
	embeddings   Embeddings
	setup        embeddingSetup // the embeddings', when they set a model up (embedsetup.go)
	rankers      Rankers
	events       Events
	checker      StoreChecker // the store's whole check, for sys.check (WithStoreCheck); nil: none
	log          logger.Logger
	version      string
	instance     string
	storeID      string // the store's identity (store.Identity), "" when not set
	setters      setters
	modelContext modelContexter // the embeddings', when they read a model's context window
	verbs        map[string]verb
	legacy       map[string]verb // rpc's own contracts, until register gives each its handler
	moduleErrs   []module.PublicError
	stop         chan struct{}
	// damaged is set once a check found the store damaged (RefuseWrites): every verb that writes is
	// refused from then on, before the daemon stops
	damaged  atomic.Pointer[string]
	stopOnce sync.Once
}

type options struct {
	listener    net.Listener
	log         logger.Logger
	preferences Preferences
	embeddings  Embeddings
	rankers     Rankers
	events      Events
	checker     StoreChecker
	reg         registrations.Tables
	files       *docs.Docs
	guard       *fsbounds.Guard
	profiler    *profiling.Profiler
	storeID     string
	modules     []module.Module
}

// Option configures a Server.
type Option func(*options)

// WithListener serves on ln (the daemon's unix socket).
func WithListener(ln net.Listener) Option { return func(o *options) { o.listener = ln } }

// StoreChecker is the store's whole check (store.Store.Check), which sys.check answers.
type StoreChecker interface {
	Check(ctx context.Context) (store.CheckReport, error)
}

// WithStoreCheck lets sys.check run the store's whole check (AM-4); without it, sys.check is
// unsupported (a store that is not SQLite's).
func WithStoreCheck(c StoreChecker) Option { return func(o *options) { o.checker = c } }

// WithStoreID reports the store's identity (store.Identity) in sys.hello and its probe, so a client
// that found this daemon through the store's lease-info can check it serves that store.
func WithStoreID(id string) Option { return func(o *options) { o.storeID = id } }

// WithProfiler lets sys.profile start and stop p, the daemon's own monitor (--profile's).
func WithProfiler(p *profiling.Profiler) Option { return func(o *options) { o.profiler = p } }

// WithLogger logs to l.
func WithLogger(l logger.Logger) Option { return func(o *options) { o.log = l } }

// WithPreferences keeps clients' preferences in p (the daemon's store).
func WithPreferences(p Preferences) Option { return func(o *options) { o.preferences = p } }

// WithRegistrations reports the build's registrations in sys.capabilities
// (docs/decisions/search.md#held-documents); without it, the empty ones of the community build.
func WithRegistrations(t registrations.Tables) Option { return func(o *options) { o.reg = t } }

// decodeLimits bound what a peer may send: a document (docs.MaxSize) and little else.
func decodeLimits() *msgpack.Limits {
	return &msgpack.Limits{MaxDepth: 16, MaxStrBytes: MaxMessage, MaxBinBytes: MaxMessage,
		MaxElements: 4096, MaxTotalElements: 16384, MaxTotalBytes: MaxMessage}
}

// New returns the server of workspaces, answering as version.
func New(workspaces Workspaces, version string, opts ...Option) *Server {
	o := options{log: logger.New()}
	for _, opt := range opts {
		opt(&o)
	}
	var id [8]byte
	_, _ = rand.Read(id[:])
	s := &Server{workspaces: workspaces, preferences: o.preferences, embeddings: o.embeddings, rankers: o.rankers, events: o.events, checker: o.checker, log: o.log,
		version: version, instance: hex.EncodeToString(id[:]), reg: o.reg, files: o.files, guard: o.guard, profiler: o.profiler, storeID: o.storeID, setters: settersOf(workspaces),
		modelContext: modelContextOf(o.embeddings), setup: setupOf(o.embeddings),
		verbs: map[string]verb{}, legacy: map[string]verb{}, stop: make(chan struct{})}
	ropts := []golibrpc.Option{golibrpc.WithLogger(o.log), golibrpc.MaxMessageBytes(MaxMessage), golibrpc.WithGate(s.gate)}
	if o.listener != nil {
		ropts = append(ropts, golibrpc.WithListener(o.listener))
	}
	s.rpc = golibrpc.New(msgpackrpc.New(decodeLimits()), ropts...)
	s.register()
	s.registerModules(o.modules)
	return s
}

// register registers rpc's own verbs, by domain: each domain's file declares its verbs.
func (s *Server) register() {
	s.own(s.sysVerbs()...)
	s.own(s.workspaceVerbs()...)
	s.own(s.scaffoldVerb())
	s.own(s.configureVerb())
	s.own(s.preferenceVerbs()...)
	s.own(s.searchVerbs()...)
	s.own(s.indexVerbs()...)
	s.own(s.graphVerbs()...)
	s.own(s.docVerbs()...)
	s.own(s.fileVerbs()...)
	s.own(s.embeddingVerbs()...)
	s.own(s.setupVerbs()...)
	s.own(s.rankerVerbs()...)
}

// add registers v. A contract that does not check, or a second registration of one name, panics at
// start (golib's Handle would silently replace the first). A verb that logs is wrapped to log its
// events; a module's result is carried by module.Wire once they are logged.
func (s *Server) add(v verb) {
	if err := v.Check(); err != nil {
		panic("rpc: " + err.Error())
	}
	if _, dup := s.verbs[v.Verb]; dup {
		panic("rpc: duplicate method registration: " + v.Verb)
	}
	s.verbs[v.Verb] = v
	s.rpc.Handle(v.Verb, s.chain(v))
}

// chain is the handler golib serves for v: its own, wrapped to log its events, and its result
// carried by module.Wire once they are logged.
func (s *Server) chain(v verb) golibrpc.Handler {
	h := v.h
	if v.Verb == "sys.hello" {
		h = helloAnswered(h)
	}
	if mv := v.module(); mv.Logs() {
		h = s.logged(mv, h)
	}
	return s.wired(h)
}

// Verbs lists the registered verbs, sorted.
func (s *Server) Verbs() []string {
	out := make([]string, 0, len(s.verbs))
	for v := range s.verbs {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}

// Run serves until ctx ends or a client calls sys.shutdown, then drains.
func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		select {
		case <-s.stop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return s.rpc.Run(ctx)
}

// RequestShutdown stops Run, as sys.shutdown does.
func (s *Server) RequestShutdown() { s.stopOnce.Do(func() { close(s.stop) }) }

// Addr is the address the server listens on.
func (s *Server) Addr() string { return s.rpc.Addr() }

// Instance is this process's instance id, as sys.hello answers it.
func (s *Server) Instance() string { return s.instance }

// gate admits only sys.hello until a compatible hello was said, and nothing after a mismatch. A
// request behind a hello still being answered waits for it (hello.go).
func (s *Server) gate(sess *golibrpc.Session, method string) error {
	if method != "sys.hello" {
		if ok, _ := sess.Value(sessHello).(bool); !ok {
			awaitHellos(sess)
		}
	}
	if refused, _ := sess.Value(sessRefused).(bool); refused {
		return &golibrpc.Error{Code: CodeProtocolMismatch, Message: "protocol mismatch: reconnect with a compatible client"}
	}
	if method == "sys.hello" {
		pendingHellos(sess).add()
		return nil
	}
	if ok, _ := sess.Value(sessHello).(bool); !ok {
		return &golibrpc.Error{Code: CodeHandshakeRequired, Message: "handshake required: call sys.hello first"}
	}
	if since := s.verbs[method].Since; since > sessionProtocol(sess) {
		return &golibrpc.Error{Code: golibrpc.CodeMethodNotFound,
			Message: fmt.Sprintf("unknown method: %s (it needs protocol %d)", method, since)}
	}
	if why := s.damaged.Load(); why != nil && writes(method, s.verbs[method].Effect) {
		return &golibrpc.Error{Code: CodeStoreDamaged, Message: *why}
	}
	return nil
}

// writes reports whether method, of effect e, can write the store: a write, or a settings change
// other than the daemon's own sys.* (sys.shutdown must still stop it).
func writes(method string, e module.Effect) bool {
	return e == module.EffectWrite || (e == module.EffectSettings && !strings.HasPrefix(method, "sys."))
}

// RefuseWrites refuses every verb that writes the store from now on, saying why: a check found the
// store damaged (AM-4), and writing on it spreads the damage. Reads go on until the daemon stops.
func (s *Server) RefuseWrites(why string) { s.damaged.Store(&why) }

// sessionProtocol is the protocol a session's hello declared.
func sessionProtocol(sess *golibrpc.Session) int64 {
	p, _ := sess.Value(sessProtocol).(int64)
	return p
}

// admits reports whether a hello from name declaring proto opens a session.
func admits(proto int64, name string) bool {
	if name == TUIName {
		return proto == Protocol
	}
	return proto >= MinProtocol && proto <= Protocol
}

// verbsAt lists the verbs a session at protocol p may call, sorted.
func (s *Server) verbsAt(p int64) []string {
	out := []string{}
	for _, v := range s.Verbs() {
		if s.verbs[v].Since <= p {
			out = append(out, v)
		}
	}
	return out
}

var errNoSuchWorkspace = errors.New("rpc: no such workspace")

// errNoPreferences answers the preference verbs of a server given no Preferences.
var errNoPreferences = errors.New("rpc: this server keeps no preferences")

var errNoEvents = errors.New("rpc: this server keeps no event log")

// errNoCheck is sys.check on a server with no store check (WithStoreCheck): a store that is not
// SQLite's, whose integrity is its database server's own.
var errNoCheck = errors.New("rpc: this server's store has no check here; a PostgreSQL store is checked by its server")

// errNoProfiler answers sys.profile on a server given no profiler; errProfileRefused a monitor
// that could not start, and errProfileIncomplete a stop that could not write its last samples
// (their reasons are logged, never sent).
var (
	errNoProfiler        = errors.New("rpc: this server keeps no profiler")
	errProfileRefused    = errors.New("rpc: the monitor did not start")
	errProfileIncomplete = errors.New("rpc: the monitor stopped, its samples incomplete")
)

// workspace resolves a verb's first parameter.
func (s *Server) workspace(params []any) (Workspace, error) {
	name, err := argStr(params, 0, "workspace")
	if err != nil {
		return nil, err
	}
	w, ok := s.workspaces.Get(name)
	if !ok {
		return nil, errNoSuchWorkspace
	}
	if w.Err() != nil {
		return nil, w.Err()
	}
	return w, nil
}
