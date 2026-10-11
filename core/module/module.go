package module

import (
	"context"
	"errors"
	"fmt"
)

// Scope is what a verb's first param names, and so what the host resolves before the handler runs.
type Scope int

const (
	// ScopeGlobal names no workspace.
	ScopeGlobal Scope = iota
	// ScopeWorkspace takes a workspace name first; the host resolves it (WorkspaceFrom).
	ScopeWorkspace
	// ScopeLocalPeer is for a peer on the unix socket only: a path on the daemon's own disk.
	ScopeLocalPeer
)

func (s Scope) String() string {
	switch s {
	case ScopeWorkspace:
		return "workspace"
	case ScopeLocalPeer:
		return "local-peer"
	}
	return "global"
}

// Audience is who a verb is for: agents are told about it, and Lua binds every verb but the
// protocol's own.
type Audience int

const (
	// AudienceAgent is for agents and people's tools: --call, Lua, autodoc.nvim.
	AudienceAgent Audience = iota
	// AudienceUser is for people's tools and Lua, and not advertised to agents.
	AudienceUser
	// AudienceInternal is the protocol's own (sys.hello, sys.shutdown): never bound to Lua.
	AudienceInternal
)

func (a Audience) String() string {
	switch a {
	case AudienceUser:
		return "user"
	case AudienceInternal:
		return "internal"
	}
	return "agent"
}

// Param is one positional parameter, as AGENTS.md names it.
type Param struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "string", "int", "bool", "bytes", "[]string", "map", "any", or a Decoder's own
	Optional bool   `json:"optional,omitempty"`
	Doc      string `json:"doc,omitempty"`
}

// Contract is a verb as every surface sees it. It is pure data: --contracts prints it with no
// daemon running.
type Contract struct {
	Verb     string     `json:"verb"`  // "todos.get"
	Since    int64      `json:"since"` // the protocol it arrived in; a session below it gets "unknown method"
	Scope    Scope      `json:"-"`
	Audience Audience   `json:"-"`
	Params   []Param    `json:"params"`
	Result   string     `json:"result,omitempty"` // the result's shape, for people: "{tasks[], malformed[]}"
	Errors   []int64    `json:"errors,omitempty"` // the public codes it may answer, beyond invalid params
	Event    *EventSpec `json:"event,omitempty"`  // a success logs an event in sys.events; nil for none
	Doc      string     `json:"doc,omitempty"`
}

// Required is how many of the params a call must pass: those before the first optional one.
func (c Contract) Required() int {
	for i, p := range c.Params {
		if p.Optional {
			return i
		}
	}
	return len(c.Params)
}

// EventSpec is what a verb's success logs: the kind, and which params' values are the event's
// workspace and detail ("" for none).
type EventSpec struct {
	Kind      string `json:"kind"`
	Workspace string `json:"workspace,omitempty"` // the param naming the workspace
	Detail    string `json:"detail,omitempty"`    // the param whose value is the detail
}

// Event is one entry a verb's success logs.
type Event struct{ Kind, Workspace, Detail string }

// Verb is a Contract and its handler. params are the call's positional params as the wire decoded
// them; the result is what the host encodes (Wire).
type Verb struct {
	Contract
	Call func(ctx context.Context, params []any) (any, error)
	// Events, when set, are the events a success logs, in place of the one Event's params name: for
	// a verb whose workspace or detail is inside a param or in its result, or that logs several.
	Events func(ctx context.Context, params []any, result any) []Event
}

// Logs reports whether a success of v logs anything.
func (v Verb) Logs() bool { return v.Event != nil || v.Events != nil }

// EventsOf are the events a success of v with params and result logs: Events', or the one Event
// names; none when v logs none.
func (v Verb) EventsOf(ctx context.Context, params []any, result any) []Event {
	if v.Events != nil {
		return v.Events(ctx, params, result)
	}
	if v.Event == nil {
		return nil
	}
	e := Event{Kind: v.Event.Kind}
	for i, p := range v.Params {
		if i >= len(params) {
			break
		}
		s, _ := params[i].(string)
		switch p.Name {
		case v.Event.Workspace:
			e.Workspace = s
		case v.Event.Detail:
			e.Detail = s
		}
	}
	return []Event{e}
}

// PublicError is an error a client may see. The code is its identity on the wire and the message is
// all of it that crosses; Name and Next are for people, in --contracts and AGENTS.md. errors.Is
// decides, and the first match in a module's list wins.
type PublicError struct {
	Err     error  `json:"-"`
	Code    int64  `json:"code"`
	Name    string `json:"name"` // "todos.stale"
	Message string `json:"message"`
	Next    string `json:"next,omitempty"` // what the client does next
}

// Module is one feature's verbs and errors. Name, Verbs and Errors do no I/O and work on a Module
// built with no dependencies, so a host can list them without a daemon.
type Module interface {
	Name() string
	Verbs() []Verb
	Errors() []PublicError
}

// Check reports what is wrong with c as a host would register it: no name, an optional param before
// a required one, or an event naming a param c does not have. A host refuses such a verb at start.
func (c Contract) Check() error {
	if c.Verb == "" {
		return errors.New("module: a contract with no verb name")
	}
	names := map[string]bool{}
	optional := false
	for _, p := range c.Params {
		if optional && !p.Optional {
			return fmt.Errorf("module: %s: %s is required after an optional param", c.Verb, p.Name)
		}
		optional = p.Optional
		names[p.Name] = true
	}
	if e := c.Event; e != nil {
		for _, n := range []string{e.Workspace, e.Detail} {
			if n != "" && !names[n] {
				return fmt.Errorf("module: %s: its event names a param it does not have: %s", c.Verb, n)
			}
		}
	}
	return nil
}
