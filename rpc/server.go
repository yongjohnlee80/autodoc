// Package rpc is AutoDoc's msgpack-RPC API (ADR 0203 §4.4): a projection of core, with no business
// logic of its own. Every client speaks it: the TUI, the Web-UI, AutoVim's auto-core, the GUI.
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
	"os"
	"sort"
	"sync"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/msgpack"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// Protocol is the API's version. A client declares the one it was written for, which this build
// serves from MinProtocol up; any change to the verbs, their parameters or their results bumps it
// (TestVerbsArePinned holds the list). sys.hello and
// sys.shutdown are frozen across protocols: a newer client stops an older daemon with them, by
// declaring the daemon's number, to start the installed one in its place.
//
// Protocol 7 adds workspace.set_patterns for workspace admission changes. Protocol 8 adds frontmatter
// schemas (workspace.set_schema, doc.validate, search.query's facets, workspace.list's schema and
// index.status's diagnosed), doc.outline, workspace.set_text_extensions with workspace.list's
// text_extensions, sys.events (sys.hello answers the client's token and the log's head), and
// workspace.set_provider with workspace.list's provider. Protocol 9 adds workspace.configure, every
// setting saved at once, sys.capabilities, and workspace.list's databases (ADR 0214). Protocol 10
// adds a build's registrations (ADR 0216): workspace.list's text_collisions; the documents a daemon
// holds, index.status's held, held_stale and held_unchecked, search.query's hold on a hit, and
// index.reindex's refusal of a held document; sys.capabilities' registrations, always there. Protocol
// 11 adds the ranker models (ADR 0215): the ranker.* verbs, search.query's rank and a ranked hit's
// rank_score, and sys.capabilities' ranker. Protocol 12 adds search.query's stages: the
// stages a search runs, asked for in place of a mode, and how its answer was made. Protocol 13
// adds index.documents, search.query's line_start and line_end on a hit, sys.capabilities' verbs,
// and sys.hello's server_protocol, min_protocol and store_id. Protocol 14 adds links from frontmatter
// relations (the kinds supersedes, superseded_by, amends, related, sources and adr) to graph.links,
// graph.backlinks, graph.neighborhood and graph.unresolved, and their kinds option; a session below 14
// sees the body links alone, its graph unchanged. It also adds graph.unresolved's reason cycle (a
// supersession loop), workspace.configure's abstract_chunk and demote_superseded with
// workspace.list's, search.query's retrieval option and a hit's superseded_by.
// Protocol 15 adds index.documents' diagnosed: the documents whose frontmatter the schema
// diagnoses, each with its diagnostics; a session below 15 is refused the option. Protocol 16 is
// ADR 1791284787: embedding.cancel_switch deletes the partial target's vectors and embedding.remove
// the removed provider's model's, so a session below 16 is refused those two verbs; index.purge_model
// refuses the active model and the target saying what to do instead. Protocol 17 is ADR 1791430651:
// file.read, file.write and file.locate, a local peer's files outside every workspace. Protocol 18
// adds graph.resolve: where a link as written reaches, for an editor following one not yet indexed.
const Protocol int64 = 18

// MinProtocol is the oldest protocol this build still serves. A session keeps the protocol it
// declared: a verb added after it answers as an unknown method, as an older daemon would, and every
// request and result it knew keeps its shape and meaning; a result may only gain keys, which a
// client ignores. A change that is not an addition raises MinProtocol, or keeps the old behaviour
// for sessions below it.
const MinProtocol int64 = 12

// TUIName is the name the TUI says hello with. It is admitted at this build's Protocol only: the
// TUI is the same binary as its daemon, and a mismatch is its cue to offer a restart.
const TUIName = "autodoc-tui"

// verbSince is the protocol each verb arrived in, for the verbs newer than MinProtocol.
var verbSince = map[string]int64{"index.documents": 13,
	"file.read": 17, "file.write": 17, "file.locate": 17, "graph.resolve": 18}

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

// Workspace is one workspace as the daemon serves it. Err set means it could not be opened (its
// root is gone); every verb naming it fails with Err, and workspace.list reports it.
type Workspace struct {
	Name, Root       string
	EmbeddingPolicy  string
	EmbeddingMode    func() string
	SectionTokens    int
	SectionSize      func() int // live workspace preference, when managed by the daemon
	Include, Exclude []string
	Err              error
	Index            *index.Indexer
	Docs             *docs.Docs
	Following        func() follow.Status
	// Warming is what the workspace waits on before it answers fully — a provider being set up, a
	// restart to take one — for index.status; nil or empty when it is ready.
	Warming        func() []string
	Searched       func() // any client searched this workspace
	EmbeddingQueue func() (state, behind string)
	// FrontmatterSchema is the workspace's frontmatter schema (ADR 0212 §5): the active one, nil for
	// none, and how its file stands. nil when the server keeps no schemas.
	FrontmatterSchema func() (*schema.Schema, SchemaStatus)
	// TextExtensions are the workspace's own plain-text extensions (ADR 0212 §3); nil for none.
	TextExtensions func() []string
	// TextCollisions are those of its text extensions a registration of the build reads instead
	// (ADR 0216 §1.3); nil for none.
	TextCollisions func() []string
	// Databases are the workspace's identity, destination and database connections (ADR 0214),
	// read from the store when listed; nil when the server keeps none.
	Databases func() Databases
	// Provider is the workspace's own embedding provider, when it has one (ADR 0212 §7).
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
	List() []*Workspace
	// Get is the workspace named name.
	Get(name string) (*Workspace, bool)
	// Add creates a workspace and starts serving it.
	Add(ctx context.Context, w config.Workspace) (*Workspace, error)
	// Rename gives a workspace a new name.
	Rename(ctx context.Context, name, to string) error
	// Remove stops serving a workspace and deletes its index; its files stay.
	Remove(ctx context.Context, name string) error
}

// Fixed is a set of workspaces that never changes: the workspace verbs answer that it cannot.
func Fixed(ws ...*Workspace) Workspaces {
	f := fixed{}
	for _, w := range ws {
		f[w.Name] = w
	}
	return f
}

type fixed map[string]*Workspace

// errFixed is a change asked of a fixed set of workspaces.
var errFixed = fmt.Errorf("%w: this set of workspaces is fixed", errs.ErrUnsupported)

func (f fixed) List() []*Workspace {
	out := make([]*Workspace, 0, len(f))
	for _, w := range f {
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
func (f fixed) Get(name string) (*Workspace, bool) { w, ok := f[name]; return w, ok }
func (fixed) Add(context.Context, config.Workspace) (*Workspace, error) {
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
	reg         registrations.Tables // the build's registrations, sys.capabilities reports them
	files       *docs.Docs           // a local peer's files outside every workspace (WithFiles); nil: none
	rpc         *golibrpc.Server
	workspaces  Workspaces
	preferences Preferences
	embeddings  Embeddings
	rankers     Rankers
	events      Events
	log         logger.Logger
	version     string
	instance    string
	storeID     string // the store's identity (store.Identity), "" when not set
	verbs       map[string]bool
	stop        chan struct{}
	stopOnce    sync.Once
}

type options struct {
	listener    net.Listener
	log         logger.Logger
	preferences Preferences
	embeddings  Embeddings
	rankers     Rankers
	events      Events
	reg         registrations.Tables
	files       *docs.Docs
	storeID     string
}

// Option configures a Server.
type Option func(*options)

// WithListener serves on ln (the daemon's unix socket).
func WithListener(ln net.Listener) Option { return func(o *options) { o.listener = ln } }

// WithStoreID reports the store's identity (store.Identity) in sys.hello and its probe, so a client
// that found this daemon through the store's lease-info can check it serves that store.
func WithStoreID(id string) Option { return func(o *options) { o.storeID = id } }

// WithLogger logs to l.
func WithLogger(l logger.Logger) Option { return func(o *options) { o.log = l } }

// WithPreferences keeps clients' preferences in p (the daemon's store).
func WithPreferences(p Preferences) Option { return func(o *options) { o.preferences = p } }

// WithRegistrations reports the build's registrations in sys.capabilities (ADR 0216 §1.4); without
// it, the empty ones of the community build.
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
	s := &Server{workspaces: workspaces, preferences: o.preferences, embeddings: o.embeddings, rankers: o.rankers, events: o.events, log: o.log,
		version: version, instance: hex.EncodeToString(id[:]), reg: o.reg, files: o.files, storeID: o.storeID,
		verbs: map[string]bool{}, stop: make(chan struct{})}
	ropts := []golibrpc.Option{golibrpc.WithLogger(o.log), golibrpc.MaxMessageBytes(MaxMessage), golibrpc.WithGate(s.gate)}
	if o.listener != nil {
		ropts = append(ropts, golibrpc.WithListener(o.listener))
	}
	s.rpc = golibrpc.New(msgpackrpc.New(decodeLimits()), ropts...)
	s.register()
	return s
}

// handle registers a verb; a second registration of one name panics at start (golib's Handle
// would silently replace the first).
func (s *Server) handle(method string, h golibrpc.Handler) {
	if s.verbs[method] {
		panic("rpc: duplicate method registration: " + method)
	}
	s.verbs[method] = true
	if spec, ok := eventsOf[method]; ok {
		h = s.logged(spec, h)
	}
	s.rpc.Handle(method, h)
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

// gate admits only sys.hello until a compatible hello was said, and nothing after a mismatch.
func (s *Server) gate(sess *golibrpc.Session, method string) error {
	if refused, _ := sess.Value(sessRefused).(bool); refused {
		return &golibrpc.Error{Code: CodeProtocolMismatch, Message: "protocol mismatch: reconnect with a compatible client"}
	}
	if method == "sys.hello" {
		return nil
	}
	if ok, _ := sess.Value(sessHello).(bool); !ok {
		return &golibrpc.Error{Code: CodeHandshakeRequired, Message: "handshake required: call sys.hello first"}
	}
	if since := verbSince[method]; since > sessionProtocol(sess) {
		return &golibrpc.Error{Code: golibrpc.CodeMethodNotFound,
			Message: fmt.Sprintf("unknown method: %s (it needs protocol %d)", method, since)}
	}
	return nil
}

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
		if verbSince[v] <= p {
			out = append(out, v)
		}
	}
	return out
}

// hello answers sys.hello({protocol, name}). No protocol is a probe: it is answered, and admits
// nothing. A protocol this build serves admits the session at that protocol (the TUI's, only this
// build's own); another refuses it for good.
func (s *Server) hello(ctx context.Context, req *golibrpc.Request) (any, error) {
	if len(req.Params) > 1 {
		return nil, invalid("sys.hello takes one map")
	}
	var info map[string]any
	if len(req.Params) == 1 {
		m, ok := req.Params[0].(map[string]any)
		if !ok {
			return nil, invalid("sys.hello takes one map")
		}
		info = m
	}
	reply := map[string]any{"protocol": Protocol, "server_protocol": Protocol, "min_protocol": MinProtocol,
		"server": ServerName, "version": s.version, "instance": s.instance, "pid": int64(os.Getpid()),
		"addr": s.rpc.Addr(), "store_id": s.storeID}
	raw, declared := info["protocol"]
	if !declared {
		return reply, nil
	}
	proto, ok := raw.(int64)
	if !ok {
		return nil, invalid("sys.hello: protocol must be an integer")
	}
	name, _ := info["name"].(string)
	if !admits(proto, name) {
		req.Session.SetValue(sessRefused, true)
		return nil, &golibrpc.Error{Code: CodeProtocolMismatch,
			Message: fmt.Sprintf("protocol mismatch: client %d, server %d (serves %d to %d)", proto, Protocol, MinProtocol, Protocol)}
	}
	req.Session.SetValue(sessHello, true)
	req.Session.SetValue(sessProtocol, proto)
	reply["protocol"] = proto
	token := newClientToken(name)
	req.Session.SetValue(sessClient, token)
	reply["client"] = token
	if s.events != nil {
		// where this client's sys.events begins: what happened before it connected is in its snapshot
		if _, head, _, err := s.events.Events(ctx, -1, 1); err == nil {
			reply["events"] = head
		}
	}
	return reply, nil
}

// shutdown answers sys.shutdown from a local peer: the unix socket, whose 0600 mode is the access
// control.
func (s *Server) shutdown(_ context.Context, req *golibrpc.Request) (any, error) {
	if err := exactArgs(req.Params, 0); err != nil {
		return nil, err
	}
	if req.Peer == nil || req.Peer.Network() != "unix" {
		return nil, &golibrpc.Error{Code: golibrpc.CodeAccessDenied, Message: "sys.shutdown is for local peers"}
	}
	s.RequestShutdown()
	return nil, nil
}

var errNoSuchWorkspace = errors.New("rpc: no such workspace")

// errNoPreferences answers the preference verbs of a server given no Preferences.
var errNoPreferences = errors.New("rpc: this server keeps no preferences")

var errNoEvents = errors.New("rpc: this server keeps no event log")

// workspace resolves a verb's first parameter.
func (s *Server) workspace(params []any) (*Workspace, error) {
	name, err := argStr(params, 0, "workspace")
	if err != nil {
		return nil, err
	}
	w, ok := s.workspaces.Get(name)
	if !ok {
		return nil, errNoSuchWorkspace
	}
	if w.Err != nil {
		return nil, w.Err
	}
	return w, nil
}
