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
	providerSpec := "{name, kind, base_url, model, context?, key?}: an absent key keeps the stored one, \"\" removes it"
	rankerSpec := "{name, kind, base_url, model, key?}: an absent key keeps the stored one, \"\" removes it"
	usage := "[{day, requests, texts, tokens, failures, limited}]"
	calls := "[{at, texts, tokens, millis, outcome}]"
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

		// embedding
		c(module.Contract{Verb: "embedding.providers", Since: 3, Scope: global, Audience: user, Params: none,
			Result: "{providers: [{name, kind, base_url, model, context, has_key}], active, error}", Errors: []int64{CodeUnsupported},
			Doc: "The stored embedding providers, the one in use, and why the one chosen is not in use."}),
		c(module.Contract{Verb: "embedding.add", Since: 3, Scope: global, Audience: user,
			Params: []module.Param{param("provider", "map", providerSpec)},
			Result: "{name, kind, base_url, model, context, has_key}", Errors: []int64{CodeUnsupported, CodeConflict, CodeProviderRefused},
			Event: ev("embedding.providers", "", "")}),
		c(module.Contract{Verb: "embedding.update", Since: 3, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("provider", "map", providerSpec)},
			Errors: []int64{CodeUnsupported, CodeNotFound, CodeConflict, CodeProviderRefused},
			Event:  ev("embedding.providers", "", "name")}),
		c(module.Contract{Verb: "embedding.remove", Since: 3, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", "")},
			Errors: []int64{CodeUnsupported, CodeNotFound}, Event: ev("embedding.providers", "", "name"),
			Doc: "Deletes the provider and its model's vectors (refused below protocol 16)."}),
		c(module.Contract{Verb: "embedding.use", Since: 3, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", "")},
			Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused}, Event: ev("embedding.switched", "", "name"),
			Doc: "Switches the daemon's provider; the new model fills while the old one answers."}),
		c(module.Contract{Verb: "embedding.cancel_switch", Since: 4, Scope: global, Audience: user, Params: none,
			Result: "{model}", Errors: []int64{CodeUnsupported}, Event: ev("embedding.cancelled", "", ""),
			Doc: "Ends the model switch under way and deletes the partial target's vectors (refused below protocol 16)."}),
		c(module.Contract{Verb: "embedding.models", Since: 3, Scope: global, Audience: user,
			Params: []module.Param{param("provider", "any", "a stored provider's name, or an unsaved one's spec"), optional("spec", "map", "with a name: the edit asked about")},
			Result: "[string]", Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused},
			Doc: "The models a provider offers."}),
		c(module.Contract{Verb: "embedding.model_context", Since: 5, Scope: global, Audience: user,
			Params: []module.Param{param("stored provider", "string", "\"\" for an unsaved one"), param("provider", "map", providerSpec)},
			Result: "{maximum}", Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused},
			Doc: "The context window the provider's model reports, in tokens."}),
		c(module.Contract{Verb: "embedding.usage", Since: 3, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("days", "int", "")},
			Result: usage, Errors: []int64{CodeUnsupported, CodeNotFound}, Doc: "A provider's usage by day."}),
		c(module.Contract{Verb: "embedding.log", Since: 3, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("limit", "int", "")},
			Result: calls, Errors: []int64{CodeUnsupported, CodeNotFound}, Doc: "A provider's recent calls."}),

		// ranker
		c(module.Contract{Verb: "ranker.list", Since: 11, Scope: global, Audience: user, Params: none,
			Result: "{rankers: [{name, kind, base_url, model, has_key}], active, window, error, supplied}", Errors: []int64{CodeUnsupported},
			Doc: "The stored rankers (kind tei or rerank-api), the one in use, how many of the top candidates it ranks, why the one chosen is not in use, and the model of the build's own ranker (\"\" for none), which is then the one in use."}),
		c(module.Contract{Verb: "ranker.add", Since: 11, Scope: global, Audience: user,
			Params: []module.Param{param("ranker", "map", rankerSpec)},
			Result: "{name, kind, base_url, model, has_key}", Errors: []int64{CodeUnsupported, CodeConflict, CodeProviderRefused},
			Event: ev("ranker.providers", "", "")}),
		c(module.Contract{Verb: "ranker.update", Since: 11, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("ranker", "map", rankerSpec)},
			Errors: []int64{CodeUnsupported, CodeNotFound, CodeConflict, CodeProviderRefused},
			Event:  ev("ranker.providers", "", "name")}),
		c(module.Contract{Verb: "ranker.remove", Since: 11, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", "")},
			Errors: []int64{CodeUnsupported, CodeNotFound}, Event: ev("ranker.providers", "", "name")}),
		c(module.Contract{Verb: "ranker.use", Since: 11, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", "\"\" for none")},
			Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused}, Event: ev("ranker.switched", "", "name"),
			Doc: "Refused while the build supplies its ranker."}),
		c(module.Contract{Verb: "ranker.window", Since: 11, Scope: global, Audience: user,
			Params: []module.Param{param("window", "int", "how many candidates it ranks, 10 to 100")},
			Errors: []int64{CodeUnsupported}, Event: ev("ranker.switched", "", ""),
			Doc: "Refused while the build supplies its ranker."}),
		c(module.Contract{Verb: "ranker.models", Since: 11, Scope: global, Audience: user,
			Params: []module.Param{param("ranker", "any", "a stored ranker's name, or an unsaved one's spec"), optional("spec", "map", "with a name: the edit asked about")},
			Result: "[string]", Errors: []int64{CodeUnsupported, CodeNotFound, CodeProviderRefused},
			Doc: "A TEI server's one model; none for a rerank-API endpoint, whose model is typed."}),
		c(module.Contract{Verb: "ranker.usage", Since: 11, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("days", "int", "")},
			Result: usage, Errors: []int64{CodeUnsupported, CodeNotFound}, Doc: "A ranker's usage by day."}),
		c(module.Contract{Verb: "ranker.log", Since: 11, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("limit", "int", "")},
			Result: calls, Errors: []int64{CodeUnsupported, CodeNotFound}, Doc: "A ranker's recent calls."}),
	}
}
