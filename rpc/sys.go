package rpc

import (
	"context"
	"fmt"
	"os"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
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
	sysContracts struct {
		Filter string `param:"filter,optional" doc:"\"\" or \"*\" for every verb, \"domain.*\" for a domain's, or a verb's name"`
	}
)

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
		module.Handle(module.Contract{Verb: "sys.hello", Since: 1, Audience: internal,
			Result: "{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}",
			Errors: []int64{CodeProtocolMismatch},
			Doc:    "Opens the session at the protocol it declares. protocol is the session's, server_protocol and min_protocol the range the daemon serves, store_id the store's identity, client this connection's token, events the event log's head."},
			s.hello),
		module.Handle(module.Contract{Verb: "sys.shutdown", Since: 1, Scope: module.ScopeLocalPeer, Audience: internal,
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
		module.Handle(module.Contract{Verb: "sys.capabilities", Since: 9, Audience: agent,
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
		module.Handle(module.Contract{Verb: "sys.events", Since: 8, Audience: agent,
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
		module.Handle(module.Contract{Verb: "sys.contracts", Since: 20, Audience: agent,
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
	reply.Client = newClientToken(name)
	sess.SetValue(sessClient, reply.Client)
	if s.events != nil {
		// where this client's sys.events begins: what happened before it connected is in its snapshot
		if _, head, _, err := s.events.Events(ctx, -1, 1); err == nil {
			reply.Events = &head
		}
	}
	return reply, nil
}
