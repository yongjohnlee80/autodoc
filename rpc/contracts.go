package rpc

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
var workspaceParam = param("workspace", "string", "a name workspace.list answers")

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
		c(module.Contract{Verb: "sys.events", Since: 8, Scope: global, Audience: agent,
			Params: []module.Param{param("since", "int", "a cursor; -1 answers the head alone"), param("limit", "int", "1 to 500")},
			Result: "{cursor, events: [{seq, kind, workspace, client, detail, at}], more}",
			Errors: []int64{CodeUnsupported, CodeCursorExpired},
			Doc:    "Configuration and lifecycle changes after cursor since, each with the token of the client that made it (\"\" for the daemon itself)."}),

		// workspace
		c(module.Contract{Verb: "workspace.list", Since: 1, Scope: global, Audience: agent, Params: none,
			Result: "[{name, root, state, include, exclude, section_tokens, embedding_policy, schema, text_extensions, text_collisions, provider, provider_error?, databases?, abstract_chunk, demote_superseded}]",
			Doc:    "Every workspace. Search only one whose state is ready. schema is {path, active, fields, error, line}; text_extensions are the extensions read as plain text besides .txt, text_collisions those of them the build reads with a registered chunker instead; provider is the workspace's own embedding provider (\"\": the daemon's); databases is {uid, destination, vector_index, view_args, source?, destination_connection?}, each connection {engine, host, database, user, schema, has_password}, never its DSN."}),
		c(module.Contract{Verb: "workspace.add", Since: 2, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("root", "string", "an absolute directory"),
				optional("include", "[]string", "globs"), optional("exclude", "[]string", "globs")},
			Result: "{name, root, state, …}", Errors: []int64{CodeConflict, CodeUnsupported},
			Event: ev("workspace.added", "name", ""), Doc: "Adds a workspace and starts serving it; answers it as workspace.list does."}),
		c(module.Contract{Verb: "workspace.rename", Since: 2, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("new name", "string", "")},
			Errors: []int64{CodeNoSuchWorkspace, CodeConflict, CodeUnsupported},
			Event:  ev("workspace.renamed", "name", "new name")}),
		c(module.Contract{Verb: "workspace.remove", Since: 2, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", "")},
			Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported},
			Event:  ev("workspace.removed", "name", ""), Doc: "Stops serving a workspace and deletes its index; its files stay."}),
		{Contract: module.Contract{Verb: "workspace.configure", Since: 9, Scope: global, Audience: user,
			Params: []module.Param{param("workspace name", "string", ""),
				param("settings", "map", "any of name, include+exclude, schema, text_extensions, section_tokens, embedding_policy, provider, abstract_chunk, demote_superseded, and (where sys.capabilities says databases) destination, vector_index, view_args, source and destination_connection ({engine, dsn, schema} or {remove: true}; a blank dsn keeps the stored one)")},
			Errors: []int64{CodeNoSuchWorkspace, CodeConflict, CodeNotFound, CodeUnsupported, CodeProviderRefused},
			Event:  ev(configureKinds, "workspace name", ""),
			Doc:    "Saves the settings at once: all of them or none. An unknown key is invalid params. Logs the event each changed setting's own verb logs, a rename first."},
			events: configureEvents},
		c(module.Contract{Verb: "workspace.set_patterns", Since: 7, Scope: global, Audience: user,
			Params: []module.Param{param("workspace name", "string", ""), param("include", "[]string", "globs; empty matches no files"), param("exclude", "[]string", "globs")},
			Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported},
			Event:  ev("workspace.patterns", "workspace name", ""), Doc: "Replaces the workspace's validated globs and reconciles it."}),
		c(module.Contract{Verb: "workspace.set_provider", Since: 8, Scope: global, Audience: user,
			Params: []module.Param{param("workspace name", "string", ""), param("provider", "string", "a stored provider; \"\" for the daemon's")},
			Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeUnsupported, CodeProviderRefused},
			Event:  ev("workspace.provider", "workspace name", "provider"),
			Doc:    "Gives the workspace a stored embedding provider of its own; it is set up first, and only that workspace re-embeds."}),
		c(module.Contract{Verb: "workspace.set_text_extensions", Since: 8, Scope: global, Audience: user,
			Params: []module.Param{param("workspace name", "string", ""), param("text extensions", "[]string", "\".log\", …")},
			Result: "[string]", Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported},
			Event: ev("workspace.text_extensions", "workspace name", ""),
			Doc:   "Declares the workspace's own plain-text extensions; answers them normalized. One the build reads with a registered chunker is refused."}),
		c(module.Contract{Verb: "workspace.set_schema", Since: 8, Scope: global, Audience: user,
			Params: []module.Param{param("workspace name", "string", ""), param("schema path", "string", "under the root, or absolute; \"\" for none")},
			Result: "{path, active, fields, error, line}", Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported},
			Event: ev("workspace.schema", "workspace name", ""), Doc: "Names the workspace's frontmatter schema file; answers its status."}),
		c(module.Contract{Verb: "workspace.section_size", Since: 5, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("section size", "int", "tokens; 0 for the default")},
			Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported},
			Event:  ev("workspace.section_size", "name", "")}),
		c(module.Contract{Verb: "workspace.focus", Since: 6, Scope: global, Audience: user,
			Params: []module.Param{param("workspace name", "string", "")},
			Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported}, Doc: "Embeds this workspace's files first."}),
		c(module.Contract{Verb: "workspace.embedding_policy", Since: 6, Scope: global, Audience: user,
			Params: []module.Param{param("workspace name", "string", ""), param("embedding policy", "string", "always, when opened or never")},
			Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported},
			Event:  ev("workspace.embedding_policy", "workspace name", "embedding policy")}),

		// preference
		c(module.Contract{Verb: "preference.list", Since: 3, Scope: global, Audience: user, Params: none,
			Result: "{name: value}", Errors: []int64{CodeUnsupported}, Doc: "What clients keep between runs."}),
		c(module.Contract{Verb: "preference.set", Since: 3, Scope: global, Audience: user,
			Params: []module.Param{param("name", "string", ""), param("value", "string", "")},
			Errors: []int64{CodeUnsupported}, Event: ev("preference.changed", "", "name"),
			Doc: "A preference that has a verb of its own (embedding.use, ranker.use, ranker.window) is refused."}),

		// search
		c(module.Contract{Verb: "search.query", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("query", "string", "words, \"phrases\", a prefix*, field:value filters"),
				optional("opts", "map", "{limit, stages, mode, tags, paths, facets, retrieval}")},
			Result: "{hits: [{path, breadcrumb, snippet, byte_start, byte_end, line_start?, line_end?, relevance, score, via, rank_score?, superseded_by?, generation, hold}], mode_used, semantic, rank, stages, semantic_error?}",
			Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported, CodeEmbedFailed, CodeSwitching, CodeRankUnavailable},
			Doc:    "The search, ranked: see search.query above."}),

		// index
		c(module.Contract{Verb: "index.list", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("after", "string", "a path; \"\" from the start"), param("limit", "int", "")},
			Result: "{docs: [{path, generation, version}], more}", Errors: []int64{CodeNoSuchWorkspace},
			Doc: "Every file in path order, after after."}),
		c(module.Contract{Verb: "index.documents", Since: 13, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, optional("opts", "map", "{sort, fields, tags, paths, facets, missing, diagnosed, after, limit}")},
			Result: "{docs: [{path, generation, title, updated, indexed_at, fields, diagnostics?}], more, next}",
			Errors: []int64{CodeNoSuchWorkspace},
			Doc:    "The files with their frontmatter, most recently updated first. sort is updated, path or indexed; fields the frontmatter fields to return (default title, type, status, updated, tags, abstract); tags every one; paths folders or files; facets as search's; missing the files that lack one of these fields; diagnosed (protocol 15) the files the schema diagnoses, with their diagnostics; after the previous page's next; limit up to 500, default 100."}),
		c(module.Contract{Verb: "index.status", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam},
			Result: "{docs, chunks, pending_jobs, cursor, oldest_retained, diagnosed, failing, held, held_stale, held_unchecked, following, warming?, embeddings?, …}",
			Errors: []int64{CodeNoSuchWorkspace},
			Doc:    "diagnosed counts files whose frontmatter has a problem; held the files another build indexed that this one holds, held_stale and held_unchecked among them."}),
		c(module.Contract{Verb: "index.changes", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("since", "int", "a cursor"), param("limit", "int", "")},
			Result: "{cursor, changes: [{path, op, generation}], more}", Errors: []int64{CodeNoSuchWorkspace, CodeCursorExpired},
			Doc: "What changed after cursor since."}),
		c(module.Contract{Verb: "index.reindex", Since: 1, Scope: workspace, Audience: user,
			Params: []module.Param{workspaceParam, param("path", "string", "")},
			Errors: []int64{CodeNoSuchWorkspace, CodeNotFound}, Doc: "Indexes one file again; a held file is refused, naming the way out."}),
		c(module.Contract{Verb: "index.models", Since: 4, Scope: workspace, Audience: user,
			Params: []module.Param{workspaceParam},
			Result: "[{fp, provider, name, dims, state, vectors, f32_bytes, bits_bytes, key_bytes}]", Errors: []int64{CodeNoSuchWorkspace},
			Doc: "The workspace's models and the room their vectors take."}),
		c(module.Contract{Verb: "index.purge_model", Since: 1, Scope: workspace, Audience: user,
			Params: []module.Param{workspaceParam, param("model", "string", "a model's fp, as index.models answers it")},
			Errors: []int64{CodeNoSuchWorkspace}, Doc: "Deletes a model's vectors; the active model and the target are refused."}),

		// graph
		c(module.Contract{Verb: "graph.links", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("path", "string", ""), optional("options", "map", "{kinds} (protocol 14)")},
			Result: "[{path, raw, anchor, kind, resolved}]", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound},
			Doc: "The links the file makes."}),
		c(module.Contract{Verb: "graph.backlinks", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("path", "string", ""), optional("options", "map", "{kinds} (protocol 14)")},
			Result: "[{path, raw, anchor, kind, resolved}]", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound},
			Doc: "The files that link to it."}),
		c(module.Contract{Verb: "graph.neighborhood", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("path", "string", ""), param("depth", "int", ""), optional("options", "map", "{kinds} (protocol 14)")},
			Result: "{nodes, edges: [{src, dst, kind}]}", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound},
			Doc: "The files within depth links."}),
		c(module.Contract{Verb: "graph.unresolved", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, optional("options", "map", "{kinds} (protocol 14)")},
			Result: "[{src, raw, reason}]", Errors: []int64{CodeNoSuchWorkspace}, Doc: "Links that name no file."}),
		c(module.Contract{Verb: "graph.resolve", Since: 18, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("from", "string", "the file the link is written in"), param("raw", "string", "the link as written")},
			Result: "{path, reason}", Errors: []int64{CodeNoSuchWorkspace},
			Doc: "Where one link reaches now, saved or not, as the index resolves links: path \"\" with reason missing or ambiguous, or \"\" when raw is no link the index keeps (a URL, a heading of from)."}),

		// doc
		c(module.Contract{Verb: "doc.read", Since: 1, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("path", "string", "")},
			Result: "{content, version}", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeUnsupported},
			Doc: "The file's text and its version. A format a build derives (PDF, DOCX) answers its derived Markdown, read-only; when that is longer than one answer carries, it is cut at a paragraph's end and its last line, after a --- rule, begins [autodoc: truncated]: that line is AutoDoc's, not the document's."}),
		c(module.Contract{Verb: "doc.read_raw", Since: 19, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("path", "string", "")},
			Result: "{content, version}", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeUnsupported},
			Doc: "doc.read on an HTML file's own bytes, where doc.read gives its derived text."}),
		c(module.Contract{Verb: "doc.write", Since: 1, Scope: workspace, Audience: user,
			Params: []module.Param{workspaceParam, param("path", "string", ""), param("content", "bytes", "binary, or a string"), param("version", "string", "the version read; \"\" to create the file")},
			Result: "{version}", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeConflict, CodeCommitted, CodeUnsupported},
			Doc: "Writes the file at version; a stale version is a conflict."}),
		c(module.Contract{Verb: "doc.write_raw", Since: 19, Scope: workspace, Audience: user,
			Params: []module.Param{workspaceParam, param("path", "string", ""), param("content", "bytes", "binary, or a string"), param("version", "string", "the version read; \"\" to create the file")},
			Result: "{version}", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeConflict, CodeCommitted, CodeUnsupported},
			Doc: "doc.write on an HTML file's own bytes, which doc.write refuses."}),
		c(module.Contract{Verb: "doc.outline", Since: 8, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("path", "string", "")},
			Result: "{version, headings: [{id, level, text, line, byte}]}", Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeUnsupported},
			Doc: "A Markdown file's headings in order, with the version they were read at; other kinds have none."}),
		c(module.Contract{Verb: "doc.validate", Since: 8, Scope: workspace, Audience: agent,
			Params: []module.Param{workspaceParam, param("path", "string", ""), param("content", "bytes", "the text, saved or not")},
			Result: "{diagnostics: [{field, line, rule, message}]}", Errors: []int64{CodeNoSuchWorkspace},
			Doc: "The text's frontmatter checked against the workspace's schema (with a version 2 schema, against its document type's fields; a type the schema does not name is rule unknown_type); only Markdown has frontmatter."}),
		c(module.Contract{Verb: "doc.rename", Since: 1, Scope: workspace, Audience: user,
			Params: []module.Param{workspaceParam, param("from", "string", ""), param("to", "string", "")},
			Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeConflict, CodeUnsupported}}),
		c(module.Contract{Verb: "doc.remove", Since: 1, Scope: workspace, Audience: user,
			Params: []module.Param{workspaceParam, param("path", "string", ""), param("version", "string", "the version read")},
			Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeConflict, CodeUnsupported}}),

		// file
		c(module.Contract{Verb: "file.read", Since: 17, Scope: local, Audience: agent,
			Params: []module.Param{param("path", "string", "absolute and clean")},
			Result: "{content, version}", Errors: []int64{golibrpc.CodeAccessDenied, CodeNotFound, CodeUnsupported},
			Doc: "A UTF-8 text file anywhere on the daemon's disk, as doc.read; a file that is not text is invalid params."}),
		c(module.Contract{Verb: "file.read_raw", Since: 19, Scope: local, Audience: agent,
			Params: []module.Param{param("path", "string", "absolute and clean")},
			Result: "{content, version}", Errors: []int64{golibrpc.CodeAccessDenied, CodeNotFound, CodeUnsupported},
			Doc: "file.read on an HTML file's own bytes."}),
		c(module.Contract{Verb: "file.write", Since: 17, Scope: local, Audience: user,
			Params: []module.Param{param("path", "string", "absolute and clean"), param("content", "bytes", "UTF-8 text"), param("version", "string", "the version read; \"\" to create the file and its folders")},
			Result: "{version}", Errors: []int64{golibrpc.CodeAccessDenied, CodeNotFound, CodeConflict, CodeCommitted, CodeUnsupported},
			Doc: "As doc.write, on a file anywhere on the daemon's disk."}),
		c(module.Contract{Verb: "file.write_raw", Since: 19, Scope: local, Audience: user,
			Params: []module.Param{param("path", "string", "absolute and clean"), param("content", "bytes", "UTF-8 text"), param("version", "string", "the version read; \"\" to create the file and its folders")},
			Result: "{version}", Errors: []int64{golibrpc.CodeAccessDenied, CodeNotFound, CodeConflict, CodeCommitted, CodeUnsupported},
			Doc: "file.write on an HTML file's own bytes."}),
		c(module.Contract{Verb: "file.locate", Since: 17, Scope: local, Audience: agent,
			Params: []module.Param{param("path", "string", "absolute and clean")},
			Result: "{workspace, path} | nil", Errors: []int64{golibrpc.CodeAccessDenied},
			Doc: "The workspace that indexes the file (the most specific root) and its path there; nil for none."}),

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
