package rpc

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/yongjohnlee80/golib/logger"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/procstat"
)

// THE PROTOCOL'S OWN — sys.hello opens a session, sys.shutdown stops the daemon, and the rest say
// what this daemon offers: its capabilities, its event log and its verbs' contracts.

type (
	sysHello struct {
		Info optionMap `param:"info,optional" doc:"{protocol, name}; none, or no protocol, is a probe that admits nothing"`
	}
	sysEvents struct {
		Since int64 `param:"since" doc:"a cursor; -1 answers the head alone"`
		Limit int   `param:"limit" doc:"1 to 500"`
	}
	sysProfile struct {
		Op  string `param:"op" doc:"start, stop or status"`
		Dir string `param:"dir,optional" doc:"start's directory; absent, autodoc-profile-serve-<pid> in the runtime directory"`
	}
	sysContracts struct {
		Filter string `param:"filter,optional" doc:"\"\" or \"*\" for every verb, \"domain.*\" for a domain's, or a verb's name"`
	}
)

// sys.profile's ops.
const (
	profileStart  = "start"
	profileStop   = "stop"
	profileStatus = "status"
)

// profileJSON is sys.profile's answer: whether the daemon's monitor runs, and its directory ("" when
// it does not), where a local peer fetches its profiles from pprof.sock.
type profileJSON struct {
	On  bool   `json:"on"`
	Dir string `json:"dir"`
}

// helloJSON is sys.hello's answer. client and events are a session's, when the hello opened one.
type helloJSON struct {
	Protocol       int64  `json:"protocol"`
	ServerProtocol int64  `json:"server_protocol"`
	MinProtocol    int64  `json:"min_protocol"`
	Server         string `json:"server"`
	Version        string `json:"version"`
	Instance       string `json:"instance"`
	PID            int64  `json:"pid"`
	Addr           string `json:"addr"`
	StoreID        string `json:"store_id"`
	Client         string `json:"client,omitempty"`
	Events         *int64 `json:"events,omitempty"` // where this client's sys.events begins
}

type eventsJSON struct {
	Cursor int64       `json:"cursor"`
	Events []eventJSON `json:"events"`
	More   bool        `json:"more"`
}

type eventJSON struct {
	Seq       int64  `json:"seq"`
	Kind      string `json:"kind"`
	Workspace string `json:"workspace"`
	Client    string `json:"client"`
	Detail    string `json:"detail"`
	At        int64  `json:"at"`
}

func (s *Server) sysVerbs() []module.Verb {
	agent, internal := module.AudienceAgent, module.AudienceInternal
	return []module.Verb{
		module.Handle(module.Contract{Verb: "sys.hello", Effect: module.EffectRead, Since: 1, Audience: internal,
			Result: "{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}",
			Errors: []int64{CodeProtocolMismatch},
			Doc:    "Opens the session at the protocol it declares. protocol is the session's, server_protocol and min_protocol the range the daemon serves, store_id the store's identity, client this connection's token, events the event log's head."},
			s.hello),
		module.Handle(module.Contract{Verb: "sys.shutdown", Effect: module.EffectSettings, Since: 1, Scope: module.ScopeLocalPeer, Audience: internal,
			Errors: []int64{golibrpc.CodeAccessDenied}, Doc: "Stops the daemon. Frozen across protocols."},
			func(context.Context, struct{}) (any, error) {
				s.RequestShutdown()
				return nil, nil
			}),
		// sys.capabilities is what this daemon offers beyond the core (core/edition): a client hides
		// what is false. A daemon whose workspaces say nothing offers nothing extra. Its
		// registrations are always there, empty for the community build (Protocol 10). verbs is what
		// the session may call at the protocol it declared, so a client asks before it calls
		// (Protocol 13). It stays a map: an edition's capabilities are keys of its own.
		module.Handle(module.Contract{Verb: "sys.capabilities", Effect: module.EffectRead, Since: 9, Audience: agent,
			Result: "{databases, registrations, ranker, verbs}",
			Doc:    "verbs is what this session may call at its protocol; the rest is what this edition offers beyond the core (a client hides what is false), the build's registrations ({chunkers: {ext: version}, formats: {ext: {id, version}}, fingerprint}, empty for the community build) and ranker, {supplied, model}."},
			func(ctx context.Context, _ struct{}) (map[string]any, error) {
				out := map[string]any{"databases": false, "registrations": RegistrationsMap(s.reg), "ranker": s.rankerCapability(),
					"verbs": s.verbsAt(protocolOf(ctx))}
				if s.setters.capabilities != nil {
					for k, v := range s.setters.capabilities.Capabilities() {
						out[k] = v
					}
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "sys.events", Effect: module.EffectRead, Since: 8, Audience: agent,
			Result: "{cursor, events: [{seq, kind, workspace, client, detail, at}], more}",
			Errors: []int64{CodeUnsupported, CodeCursorExpired},
			Doc:    "Configuration and lifecycle changes after cursor since, each with the token of the client that made it (\"\" for the daemon itself)."},
			func(ctx context.Context, p sysEvents) (*eventsJSON, error) {
				if s.events == nil {
					return nil, errNoEvents
				}
				if p.Limit < 1 || p.Limit > maxEvents {
					return nil, invalid("sys.events: limit must be 1 to 500")
				}
				evs, cursor, more, err := s.events.Events(ctx, p.Since, p.Limit)
				if err != nil {
					return nil, err
				}
				out := &eventsJSON{Cursor: cursor, Events: make([]eventJSON, len(evs)), More: more}
				for i, e := range evs {
					out.Events[i] = eventJSON{Seq: e.Seq, Kind: e.Kind, Workspace: e.Workspace, Client: e.Client, Detail: e.Detail, At: e.At}
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "sys.stats", Effect: module.EffectRead, Since: 25, Audience: agent,
			Result: "{pid, version, started_at, uptime, rss, peak_rss, cpu_seconds, go_memory, go_heap, goroutines, cpus}",
			Doc:    "The daemon's own process: its sizes in bytes (rss 0 where the platform cannot say), cpu_seconds its CPU so far, cumulative (a reader takes a rate between two calls), started_at in Unix seconds and uptime in seconds."},
			func(context.Context, struct{}) (StatsJSON, error) {
				return StatsOf(procstat.Read(), s.version, time.Now()), nil
			}),
		// sys.profile is the protocol's own: never bound to Lua, refused to a gated script's call, and
		// for a peer on the unix socket alone, since a monitor writes files and serves its profiles on
		// a socket of its own (Protocol 32).
		module.Handle(module.Contract{Verb: "sys.profile", Effect: module.EffectSettings, Since: 32, Scope: module.ScopeLocalPeer, Audience: internal,
			Result: "{on, dir}", Errors: []int64{golibrpc.CodeAccessDenied, golibrpc.CodeInvalidParams, CodeUnsupported, golibrpc.CodeInternal},
			Doc: "Starts (op start, in dir or the default), stops (op stop) or reports (op status) the daemon's memory monitor, --profile's; dir is where its samples, heap profiles and pprof.sock are, \"\" while it is off."},
			s.profile),
		module.Handle(module.Contract{Verb: "sys.contracts", Effect: module.EffectRead, Since: 20, Audience: agent,
			Result: "{protocol, min_protocol, verbs: [{verb, since, scope, audience, params, result, errors, event, doc}], errors: [{code, name, next, messages}]}",
			Doc:    "These tables: the verbs this session may call, and the codes they may answer."},
			func(ctx context.Context, p sysContracts) (catalogJSON, error) {
				return s.Catalog(protocolOf(ctx), p.Filter).wire(), nil
			}),
	}
}

// hello answers sys.hello({protocol, name}). No protocol is a probe: it is answered, and admits
// nothing. A protocol this build serves admits the session at that protocol (the TUI's, only this
// build's own); another refuses it for good.
func (s *Server) hello(ctx context.Context, p sysHello) (*helloJSON, error) {
	var info map[string]any
	if p.Info.raw != nil {
		m, ok := p.Info.raw.(map[string]any)
		if !ok {
			return nil, invalid("sys.hello takes one map")
		}
		info = m
	}
	reply := &helloJSON{Protocol: Protocol, ServerProtocol: Protocol, MinProtocol: MinProtocol, Server: ServerName,
		Version: s.version, Instance: s.instance, PID: int64(os.Getpid()), Addr: s.rpc.Addr(), StoreID: s.storeID}
	raw, declared := info["protocol"]
	if !declared {
		return reply, nil
	}
	proto, ok := raw.(int64)
	if !ok {
		return nil, invalid("sys.hello: protocol must be an integer")
	}
	name, _ := info["name"].(string)
	sess := sessionOf(ctx)
	if !admits(proto, name) {
		sess.SetValue(sessRefused, true)
		return nil, &golibrpc.Error{Code: CodeProtocolMismatch,
			Message: fmt.Sprintf("protocol mismatch: client %d, server %d (serves %d to %d)", proto, Protocol, MinProtocol, Protocol)}
	}
	sess.SetValue(sessHello, true)
	sess.SetValue(sessProtocol, proto)
	reply.Protocol = proto
	reply.Client = newClientToken(name + who(name, info))
	sess.SetValue(sessClient, reply.Client)
	if s.events != nil {
		// where this client's sys.events begins: what happened before it connected is in its snapshot
		if _, head, _, err := s.events.Events(ctx, -1, 1); err == nil {
			reply.Events = &head
		}
	}
	return reply, nil
}

// StatsJSON is sys.stats's answer, and autodoc.app.stats's (the TUI's own process, in the same keys).
type StatsJSON struct {
	PID        int64   `json:"pid"`
	Version    string  `json:"version"`
	StartedAt  int64   `json:"started_at"` // Unix seconds
	Uptime     float64 `json:"uptime"`     // seconds
	RSS        int64   `json:"rss"`
	PeakRSS    int64   `json:"peak_rss"`
	CPUSeconds float64 `json:"cpu_seconds"`
	GoMemory   int64   `json:"go_memory"`
	GoHeap     int64   `json:"go_heap"`
	Goroutines int64   `json:"goroutines"`
	CPUs       int64   `json:"cpus"`
}

// StatsOf is st as sys.stats answers it, version the process's, now the time of the reading.
func StatsOf(st procstat.Stats, version string, now time.Time) StatsJSON {
	return StatsJSON{PID: int64(st.PID), Version: version, StartedAt: st.Started.Unix(),
		Uptime: now.Sub(st.Started).Seconds(), RSS: int64(st.RSS), PeakRSS: int64(st.PeakRSS),
		CPUSeconds: st.CPUSeconds, GoMemory: int64(st.GoMemory), GoHeap: int64(st.GoHeap),
		Goroutines: int64(st.Goroutines), CPUs: int64(st.CPUs)}
}

// profile answers sys.profile. A monitor already running is kept by start; stop when none runs is
// no error, and a stop whose last samples could not be written is one (the monitor is off all
// the same: status says so).
func (s *Server) profile(_ context.Context, p sysProfile) (profileJSON, error) {
	if s.profiler == nil {
		return profileJSON{}, errNoProfiler
	}
	switch p.Op {
	case profileStart:
		if _, err := s.profiler.Start(p.Dir); err != nil {
			logger.Warning(s.log, err, "sys.profile: the monitor did not start")
			return profileJSON{}, errProfileRefused
		}
	case profileStop:
		if err := s.profiler.Stop(); err != nil {
			logger.Warning(s.log, err, "sys.profile: the monitor stopped, its samples incomplete")
			return profileJSON{}, errProfileIncomplete
		}
	case profileStatus:
	default:
		return profileJSON{}, invalid("sys.profile: op must be start, stop or status")
	}
	dir := s.profiler.Dir()
	return profileJSON{On: dir != "", Dir: dir}, nil
}
