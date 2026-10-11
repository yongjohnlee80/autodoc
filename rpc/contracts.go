package rpc

//go:generate go run ../internal/contractsgen -agents ../AGENTS.md

import (
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"

	"github.com/yongjohnlee80/autodoc/core/module"
)

// THE CONTRACTS of the verbs rpc serves itself: one row per verb, by domain. register gives each row
// its handler (handle panics on a verb with no row, and New on a row with no handler), so a verb is
// declared once and its protocol, params, errors and events come from here.

func param(name, typ, doc string) module.Param { return module.Param{Name: name, Type: typ, Doc: doc} }

func optional(name, typ, doc string) module.Param {
	return module.Param{Name: name, Type: typ, Optional: true, Doc: doc}
}

// workspaceParam is the first param of a ScopeWorkspace verb.
var workspaceParam = param("workspace", "string", "")

// The kinds of event workspace.configure may log, one for each setting it changed (configureEvents).
const configureKinds = "workspace.renamed | workspace.patterns | workspace.schema | workspace.text_extensions | " +
	"workspace.section_size | workspace.embedding_policy | workspace.provider | workspace.databases | workspace.retrieval"

// legacyVerbs are the contracts of rpc's own verbs.
func legacyVerbs() []verb {
	const (
		global    = module.ScopeGlobal
		workspace = module.ScopeWorkspace
		local     = module.ScopeLocalPeer
		agent     = module.AudienceAgent
		user      = module.AudienceUser
		internal  = module.AudienceInternal
	)
	ev := func(kind, workspace, detail string) *module.EventSpec {
		return &module.EventSpec{Kind: kind, Workspace: workspace, Detail: detail}
	}
	none := []module.Param(nil)
	c := func(c module.Contract) verb { return verb{Contract: c} }
	return []verb{
		// sys
		c(module.Contract{Verb: "sys.hello", Since: 1, Scope: global, Audience: internal,
			Params: []module.Param{optional("info", "map", "{protocol, name}; none, or no protocol, is a probe that admits nothing")},
			Result: "{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}",
			Errors: []int64{CodeProtocolMismatch},
			Doc:    "Opens the session at the protocol it declares. protocol is the session's, server_protocol and min_protocol the range the daemon serves, store_id the store's identity, client this connection's token, events the event log's head."}),
		c(module.Contract{Verb: "sys.shutdown", Since: 1, Scope: local, Audience: internal, Params: none,
			Errors: []int64{golibrpc.CodeAccessDenied}, Doc: "Stops the daemon. Frozen across protocols."}),
		c(module.Contract{Verb: "sys.capabilities", Since: 9, Scope: global, Audience: agent, Params: none,
			Result: "{databases, registrations, ranker, verbs}",
			Doc:    "verbs is what this session may call at its protocol; the rest is what this edition offers beyond the core (a client hides what is false), the build's registrations ({chunkers: {ext: version}, formats: {ext: {id, version}}, fingerprint}, empty for the community build) and ranker, {supplied, model}."}),
		c(module.Contract{Verb: "sys.contracts", Since: 20, Scope: global, Audience: agent,
			Params: []module.Param{optional("filter", "string", "\"\" or \"*\" for every verb, \"domain.*\" for a domain's, or a verb's name")},
			Result: "{protocol, min_protocol, verbs: [{verb, since, scope, audience, params, result, errors, event, doc}], errors: [{code, name, next, messages}]}",
			Doc:    "These tables: the verbs this session may call, and the codes they may answer."}),
		c(module.Contract{Verb: "sys.events", Since: 8, Scope: global, Audience: agent,
			Params: []module.Param{param("since", "int", "a cursor; -1 answers the head alone"), param("limit", "int", "1 to 500")},
			Result: "{cursor, events: [{seq, kind, workspace, client, detail, at}], more}",
			Errors: []int64{CodeUnsupported, CodeCursorExpired},
			Doc:    "Configuration and lifecycle changes after cursor since, each with the token of the client that made it (\"\" for the daemon itself)."}),

		// workspace
		{Contract: module.Contract{Verb: "workspace.configure", Since: 9, Scope: global, Audience: user,
			Params: []module.Param{param("workspace name", "string", ""),
				param("settings", "map", "any of name, include+exclude, schema, text_extensions, section_tokens, embedding_policy, provider, abstract_chunk, demote_superseded, and (where sys.capabilities says databases) destination, vector_index, view_args, source and destination_connection ({engine, dsn, schema} or {remove: true}; a blank dsn keeps the stored one)")},
			Errors: []int64{CodeNoSuchWorkspace, CodeConflict, CodeNotFound, CodeUnsupported, CodeProviderRefused},
			Event:  ev(configureKinds, "workspace name", ""),
			Doc:    "Saves the settings at once: all of them or none. An unknown key is invalid params. Logs the event each changed setting's own verb logs, a rename first."},
			events: configureEvents},
	}
}
