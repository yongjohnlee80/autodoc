// Package rpc is AutoDoc's msgpack-RPC API (ADR 0203 §4.4): a projection of core, with no business
// logic of its own. Every client speaks it: the TUI, the Web-UI, AutoVim's auto-core, the GUI.
//
// A session must say sys.hello with this build's Protocol before any other verb answers; a hello
// with another protocol refuses the session for good. The server only answers: it never sends a
// notification or a request, so a request/response-only client (auto-core's) can use every verb.
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

	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/msgpack"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
)

// Protocol is the API's version. A client must declare exactly this one; any change to the verbs,
// their parameters or their results bumps it (TestVerbsArePinned holds the list).
const Protocol int64 = 1

// ServerName is what sys.hello answers as "server", so a probe tells AutoDoc from another occupant.
const ServerName = "autodoc"

// MaxMessage is the largest message either way: a document plus its envelope (docs.MaxSize).
const MaxMessage = 4 << 20

// Session keys.
const (
	sessHello   = "hello"   // true once a compatible sys.hello was said
	sessRefused = "refused" // true after a hello with another protocol: the session is spent
)

// Workspace is one configured workspace as the daemon serves it. Err set means it could not be
// opened (another instance holds its store: workspace.ErrWorkspaceBusy); every verb naming it
// fails with Err, and workspace.list reports it.
type Workspace struct {
	Name, Root string
	Err        error
	Index      *index.Indexer
	Docs       *docs.Docs
	Following  func() follow.Status
}

// Server is the API over the daemon's workspaces.
type Server struct {
	rpc        *golibrpc.Server
	workspaces map[string]*Workspace
	order      []string
	version    string
	instance   string
	verbs      map[string]bool
	stop       chan struct{}
	stopOnce   sync.Once
}

type options struct {
	listener net.Listener
	log      logger.Logger
}

// Option configures a Server.
type Option func(*options)

// WithListener serves on ln (the daemon's unix socket).
func WithListener(ln net.Listener) Option { return func(o *options) { o.listener = ln } }

// WithLogger logs to l.
func WithLogger(l logger.Logger) Option { return func(o *options) { o.log = l } }

// decodeLimits bound what a peer may send: a document (docs.MaxSize) and little else.
func decodeLimits() *msgpack.Limits {
	return &msgpack.Limits{MaxDepth: 16, MaxStrBytes: MaxMessage, MaxBinBytes: MaxMessage,
		MaxElements: 4096, MaxTotalElements: 16384, MaxTotalBytes: MaxMessage}
}

// New returns the server of workspaces (in config order), answering as version.
func New(workspaces []*Workspace, version string, opts ...Option) *Server {
	o := options{log: logger.New()}
	for _, opt := range opts {
		opt(&o)
	}
	var id [8]byte
	_, _ = rand.Read(id[:])
	s := &Server{workspaces: map[string]*Workspace{}, version: version, instance: hex.EncodeToString(id[:]),
		verbs: map[string]bool{}, stop: make(chan struct{})}
	for _, w := range workspaces {
		s.workspaces[w.Name] = w
		s.order = append(s.order, w.Name)
	}
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
	return nil
}

// hello answers sys.hello({protocol, name}). No protocol is a probe: it is answered, and admits
// nothing. This build's protocol admits the session; another refuses it for good.
func (s *Server) hello(_ context.Context, req *golibrpc.Request) (any, error) {
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
	reply := map[string]any{"protocol": Protocol, "server": ServerName, "version": s.version,
		"instance": s.instance, "pid": int64(os.Getpid()), "addr": s.rpc.Addr()}
	raw, declared := info["protocol"]
	if !declared {
		return reply, nil
	}
	proto, ok := raw.(int64)
	if !ok {
		return nil, invalid("sys.hello: protocol must be an integer")
	}
	if proto != Protocol {
		req.Session.SetValue(sessRefused, true)
		return nil, &golibrpc.Error{Code: CodeProtocolMismatch, Message: fmt.Sprintf("protocol mismatch: client %d, server %d", proto, Protocol)}
	}
	req.Session.SetValue(sessHello, true)
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

// workspace resolves a verb's first parameter.
func (s *Server) workspace(params []any) (*Workspace, error) {
	name, err := argStr(params, 0, "workspace")
	if err != nil {
		return nil, err
	}
	w, ok := s.workspaces[name]
	if !ok {
		return nil, errNoSuchWorkspace
	}
	if w.Err != nil {
		return nil, w.Err
	}
	return w, nil
}
