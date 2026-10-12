-- The daemon's verb contracts, for autodoc.nvim: generated from the contracts this build serves by
-- go generate ./rpc (internal/contractsgen). Do not edit; autodoc --contracts prints the same.
-- autodoc.contract binds them as functions; the plugin and the binary ship from one commit.
return {
  protocol = 21,
  min_protocol = 12,
  verbs = {
    ["doc.outline"] = {
      since = 8, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" }
      },
      result = "{version, headings: [{id, level, text, line, byte}]}",
      doc = "A Markdown file's headings in order, with the version they were read at; other kinds have none.",
    },
    ["doc.read"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" }
      },
      result = "{content, version}",
      doc = "The file's text and its version. A format a build derives (PDF, DOCX) answers its derived Markdown, read-only; when that is longer than one answer carries, it is cut at a paragraph's end and its last line, after a --- rule, begins [autodoc: truncated]: that line is AutoDoc's, not the document's.",
    },
    ["doc.read_raw"] = {
      since = 19, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" }
      },
      result = "{content, version}",
      doc = "doc.read on an HTML file's own bytes, where doc.read gives its derived text.",
    },
    ["doc.remove"] = {
      since = 1, scope = "workspace", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" },
        { name = "version", type = "string" }
      },
    },
    ["doc.rename"] = {
      since = 1, scope = "workspace", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "from", type = "string" },
        { name = "to", type = "string" }
      },
    },
    ["doc.validate"] = {
      since = 8, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" },
        { name = "content", type = "bytes" }
      },
      result = "{diagnostics: [{field, line, rule, message}]}",
      doc = "The text's frontmatter checked against the workspace's schema (with a version 2 schema, against its document type's fields; a type the schema does not name is rule unknown_type); only Markdown has frontmatter.",
    },
    ["doc.write"] = {
      since = 1, scope = "workspace", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" },
        { name = "content", type = "bytes" },
        { name = "version", type = "string" }
      },
      result = "{version}",
      doc = "Writes the file at version; a stale version is a conflict.",
    },
    ["doc.write_raw"] = {
      since = 19, scope = "workspace", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" },
        { name = "content", type = "bytes" },
        { name = "version", type = "string" }
      },
      result = "{version}",
      doc = "doc.write on an HTML file's own bytes, which doc.write refuses.",
    },
    ["embedding.add"] = {
      since = 3, scope = "global", audience = "user",
      params = {
        { name = "provider", type = "map" }
      },
      result = "{name, kind, base_url, model, context, has_key}",
    },
    ["embedding.cancel_switch"] = {
      since = 4, scope = "global", audience = "user",
      params = {},
      result = "{model}",
      doc = "Ends the model switch under way and deletes the partial target's vectors (refused below protocol 16).",
    },
    ["embedding.log"] = {
      since = 3, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "limit", type = "int" }
      },
      result = "[{at, texts, tokens, millis, outcome}]",
      doc = "A provider's recent calls.",
    },
    ["embedding.model_context"] = {
      since = 5, scope = "global", audience = "user",
      params = {
        { name = "stored", type = "string" },
        { name = "provider", type = "map" }
      },
      result = "{maximum}",
      doc = "The context window the provider's model reports, in tokens.",
    },
    ["embedding.models"] = {
      since = 3, scope = "global", audience = "user",
      params = {
        { name = "provider", type = "any" },
        { name = "spec", type = "map", optional = true }
      },
      result = "[string]",
      doc = "The models a provider offers.",
    },
    ["embedding.providers"] = {
      since = 3, scope = "global", audience = "user",
      params = {},
      result = "{providers: [{name, kind, base_url, model, context, has_key}], active, error}",
      doc = "The stored embedding providers, the one in use, and why the one chosen is not in use.",
    },
    ["embedding.remove"] = {
      since = 3, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" }
      },
      doc = "Deletes the provider and its model's vectors (refused below protocol 16).",
    },
    ["embedding.update"] = {
      since = 3, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "provider", type = "map" }
      },
    },
    ["embedding.usage"] = {
      since = 3, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "days", type = "int" }
      },
      result = "[{day, requests, texts, tokens, failures, limited}]",
      doc = "A provider's usage by day.",
    },
    ["embedding.use"] = {
      since = 3, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" }
      },
      doc = "Switches the daemon's provider; the new model fills while the old one answers.",
    },
    ["file.locate"] = {
      since = 17, scope = "local-peer", audience = "agent",
      params = {
        { name = "path", type = "string" }
      },
      result = "{workspace, path} | nil",
      doc = "The workspace that indexes the file (the most specific root, symlinks resolved) and its path there; nil for none.",
    },
    ["file.read"] = {
      since = 17, scope = "local-peer", audience = "agent",
      params = {
        { name = "path", type = "string" }
      },
      result = "{content, version}",
      doc = "A UTF-8 text file anywhere on the daemon's disk, as doc.read; a file that is not text is invalid params.",
    },
    ["file.read_raw"] = {
      since = 19, scope = "local-peer", audience = "agent",
      params = {
        { name = "path", type = "string" }
      },
      result = "{content, version}",
      doc = "file.read on an HTML file's own bytes.",
    },
    ["file.write"] = {
      since = 17, scope = "local-peer", audience = "user",
      params = {
        { name = "path", type = "string" },
        { name = "content", type = "bytes" },
        { name = "version", type = "string" }
      },
      result = "{version}",
      doc = "As doc.write, on a file anywhere on the daemon's disk. A file a workspace indexes (file.locate's answer) is written by that workspace's doc.write, and indexed at once.",
    },
    ["file.write_raw"] = {
      since = 19, scope = "local-peer", audience = "user",
      params = {
        { name = "path", type = "string" },
        { name = "content", type = "bytes" },
        { name = "version", type = "string" }
      },
      result = "{version}",
      doc = "file.write on an HTML file's own bytes; in a workspace, its doc.write_raw.",
    },
    ["graph.backlinks"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" },
        { name = "options", type = "map", optional = true }
      },
      result = "[{path, raw, anchor, kind, resolved}]",
      doc = "The files that link to it.",
    },
    ["graph.links"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" },
        { name = "options", type = "map", optional = true }
      },
      result = "[{path, raw, anchor, kind, resolved}]",
      doc = "The links the file makes.",
    },
    ["graph.neighborhood"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" },
        { name = "depth", type = "int" },
        { name = "options", type = "map", optional = true }
      },
      result = "{nodes, edges: [{src, dst, kind}]}",
      doc = "The files within depth links.",
    },
    ["graph.resolve"] = {
      since = 18, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "from", type = "string" },
        { name = "raw", type = "string" }
      },
      result = "{path, reason}",
      doc = "Where one link reaches now, saved or not, as the index resolves links: path \"\" with reason missing or ambiguous, or \"\" when raw is no link the index keeps (a URL, a heading of from).",
    },
    ["graph.unresolved"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "options", type = "map", optional = true }
      },
      result = "[{src, raw, reason}]",
      doc = "Links that name no file.",
    },
    ["index.changes"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "since", type = "int" },
        { name = "limit", type = "int" }
      },
      result = "{cursor, changes: [{path, op, generation}], more}",
      doc = "What changed after cursor since.",
    },
    ["index.documents"] = {
      since = 13, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "opts", type = "map", optional = true }
      },
      result = "{docs: [{path, generation, title, updated, indexed_at, fields, diagnostics?}], more, next}",
      doc = "The files with their frontmatter, most recently updated first. sort is updated, path or indexed; fields the frontmatter fields to return (default title, type, status, updated, tags, abstract); tags every one; paths folders or files; facets as search's; missing the files that lack one of these fields; diagnosed (protocol 15) the files the schema diagnoses, with their diagnostics; after the previous page's next; limit up to 500, default 100.",
    },
    ["index.list"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "after", type = "string" },
        { name = "limit", type = "int" }
      },
      result = "{docs: [{path, generation, version}], more}",
      doc = "Every file in path order, after after.",
    },
    ["index.models"] = {
      since = 4, scope = "workspace", audience = "user",
      params = {
        { name = "workspace", type = "string" }
      },
      result = "[{fp, provider, name, dims, state, vectors, f32_bytes, bits_bytes, key_bytes}]",
      doc = "The workspace's models and the room their vectors take.",
    },
    ["index.purge_model"] = {
      since = 1, scope = "workspace", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "model", type = "string" }
      },
      doc = "Deletes a model's vectors; the active model and the target are refused.",
    },
    ["index.reindex"] = {
      since = 1, scope = "workspace", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" }
      },
      doc = "Indexes one file again; a held file is refused, naming the way out.",
    },
    ["index.status"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" }
      },
      result = "{docs, chunks, pending_jobs, cursor, oldest_retained, diagnosed, failing, held, held_stale, held_unchecked, following, warming?, embeddings?, …}",
      doc = "diagnosed counts files whose frontmatter has a problem; held the files another build indexed that this one holds, held_stale and held_unchecked among them.",
    },
    ["preference.list"] = {
      since = 3, scope = "global", audience = "user",
      params = {},
      result = "{name: value}",
      doc = "What clients keep between runs.",
    },
    ["preference.set"] = {
      since = 3, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "value", type = "string" }
      },
      doc = "A preference that has a verb of its own (embedding.use, ranker.use, ranker.window) is refused.",
    },
    ["ranker.add"] = {
      since = 11, scope = "global", audience = "user",
      params = {
        { name = "ranker", type = "map" }
      },
      result = "{name, kind, base_url, model, has_key}",
    },
    ["ranker.list"] = {
      since = 11, scope = "global", audience = "user",
      params = {},
      result = "{rankers: [{name, kind, base_url, model, has_key}], active, window, error, supplied}",
      doc = "The stored rankers (kind tei or rerank-api), the one in use, how many of the top candidates it ranks, why the one chosen is not in use, and the model of the build's own ranker (\"\" for none), which is then the one in use.",
    },
    ["ranker.log"] = {
      since = 11, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "limit", type = "int" }
      },
      result = "[{at, texts, tokens, millis, outcome}]",
      doc = "A ranker's recent calls.",
    },
    ["ranker.models"] = {
      since = 11, scope = "global", audience = "user",
      params = {
        { name = "ranker", type = "any" },
        { name = "spec", type = "map", optional = true }
      },
      result = "[string]",
      doc = "A TEI server's one model; none for a rerank-API endpoint, whose model is typed.",
    },
    ["ranker.remove"] = {
      since = 11, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" }
      },
    },
    ["ranker.update"] = {
      since = 11, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "ranker", type = "map" }
      },
    },
    ["ranker.usage"] = {
      since = 11, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "days", type = "int" }
      },
      result = "[{day, requests, texts, tokens, failures, limited}]",
      doc = "A ranker's usage by day.",
    },
    ["ranker.use"] = {
      since = 11, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" }
      },
      doc = "Refused while the build supplies its ranker.",
    },
    ["ranker.window"] = {
      since = 11, scope = "global", audience = "user",
      params = {
        { name = "window", type = "int" }
      },
      doc = "Refused while the build supplies its ranker.",
    },
    ["search.query"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "query", type = "string" },
        { name = "opts", type = "map", optional = true }
      },
      result = "{hits: [{path, breadcrumb, snippet, byte_start, byte_end, line_start?, line_end?, relevance, score, via, rank_score?, superseded_by?, generation, hold}], mode_used, semantic, rank, stages, semantic_error?}",
      doc = "The search, ranked: see search.query above.",
    },
    ["sys.capabilities"] = {
      since = 9, scope = "global", audience = "agent",
      params = {},
      result = "{databases, registrations, ranker, verbs}",
      doc = "verbs is what this session may call at its protocol; the rest is what this edition offers beyond the core (a client hides what is false), the build's registrations ({chunkers: {ext: version}, formats: {ext: {id, version}}, fingerprint}, empty for the community build) and ranker, {supplied, model}.",
    },
    ["sys.contracts"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "filter", type = "string", optional = true }
      },
      result = "{protocol, min_protocol, verbs: [{verb, since, scope, audience, params, result, errors, event, doc}], errors: [{code, name, next, messages}]}",
      doc = "These tables: the verbs this session may call, and the codes they may answer.",
    },
    ["sys.events"] = {
      since = 8, scope = "global", audience = "agent",
      params = {
        { name = "since", type = "int" },
        { name = "limit", type = "int" }
      },
      result = "{cursor, events: [{seq, kind, workspace, client, detail, at}], more}",
      doc = "Configuration and lifecycle changes after cursor since, each with the token of the client that made it (\"\" for the daemon itself).",
    },
    ["sys.hello"] = {
      since = 1, scope = "global", audience = "internal",
      params = {
        { name = "info", type = "map", optional = true }
      },
      result = "{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}",
      doc = "Opens the session at the protocol it declares. protocol is the session's, server_protocol and min_protocol the range the daemon serves, store_id the store's identity, client this connection's token, events the event log's head.",
    },
    ["sys.shutdown"] = {
      since = 1, scope = "local-peer", audience = "internal",
      params = {},
      doc = "Stops the daemon. Frozen across protocols.",
    },
    ["todos.ack"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "consumer", type = "string" },
        { name = "seq", type = "int" }
      },
      result = "nil",
      doc = "Advances a consumer's cursor once it has handled the changes up to seq.",
    },
    ["todos.add"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "spec", type = "map" }
      },
      result = "task: {id, version, status, title, …, rev, path, description}",
      doc = "Creates a task. With an assignee it is an assignment: open becomes in-progress, and the assignee is told.",
    },
    ["todos.assign"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "id", type = "string" },
        { name = "rev", type = "string" },
        { name = "assignee", type = "string" },
        { name = "reason", type = "string", optional = true }
      },
      result = "task: {id, version, status, title, …, rev, path, description}",
      doc = "Assigns a task read at rev (\"\" clears it); assigning an open task makes it in-progress, and the assignee is told.",
    },
    ["todos.changes"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "consumer", type = "string" },
        { name = "limit", type = "int", optional = true }
      },
      result = "{changes?: [{seq, id, kind, op, from?, to?, fields?, reason?, hand?, at}], resync?, snapshot?, cursor}",
      doc = "A consumer's committed changes after its cursor, or a resync when it was away too long.",
    },
    ["todos.claim"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "opts", type = "map", optional = true }
      },
      result = "nil",
      doc = "The cutover: the daemon writes the store from now on. Only the release that removes auto-core's writer calls it.",
    },
    ["todos.get"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "id", type = "string" }
      },
      result = "task: {id, version, status, title, …, rev, path, description}",
      doc = "One task, with its rev and body.",
    },
    ["todos.list"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "opts", type = "map", optional = true }
      },
      result = "{tasks: [task: {id, version, status, title, …, rev, path, description}], malformed: [{path, reason}], partitions?: [{month, count}], cursor}",
      doc = "The active buckets' tasks; archives only by month, on request.",
    },
    ["todos.move_store"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "to", type = "string" }
      },
      result = "{store_id, store, root, override?, writable, claimed}",
      doc = "Moves an at's store to a new directory; its claim, change log and cursors move with it.",
    },
    ["todos.open"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "opts", type = "map", optional = true }
      },
      result = "{store_id, store, scaffolded: false}",
      doc = "Checks that an at's store exists, and records the client's kb_root for its refs.",
    },
    ["todos.refresh"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" }
      },
      result = "{scanned, moved, archived, errors_set, rewritten, malformed}",
      doc = "Reconciles the active buckets: misplaced files, errors[], the 28-day archive. Never reads archives.",
    },
    ["todos.register"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "root", type = "string" }
      },
      result = "{new}",
      doc = "Enrolls a project root that is no AutoDoc workspace, so its .todo-list may be written; allowed before any claim.",
    },
    ["todos.release"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" }
      },
      result = "nil",
      doc = "The cutover's rollback: the daemon only reads the store again.",
    },
    ["todos.remove"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "id", type = "string" },
        { name = "rev", type = "string" }
      },
      result = "nil",
      doc = "Deletes a task read at rev.",
    },
    ["todos.remove_malformed"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "path", type = "string" }
      },
      result = "nil",
      doc = "Deletes one malformed file; a file using the removed automation fields is never deleted by the daemon.",
    },
    ["todos.resolve"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" }
      },
      result = "{store_id, store, root, override?, writable, claimed}",
      doc = "Where an at's store is, and whether the daemon may write it.",
    },
    ["todos.scaffold"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" }
      },
      result = "{store_id, store, scaffolded}",
      doc = "Creates the at's .todo-list and its buckets.",
    },
    ["todos.set_dir"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "dir", type = "string" }
      },
      result = "{store_id, store, root, override?, writable, claimed}",
      doc = "Points an at's root at another store directory, or back at <root>/.todo-list.",
    },
    ["todos.set_var"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "value", type = "string" }
      },
      result = "nil",
      doc = "Sets or removes a user variable; built-ins are read-only.",
    },
    ["todos.status"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "id", type = "string" },
        { name = "rev", type = "string" },
        { name = "status", type = "string" }
      },
      result = "task: {id, version, status, title, …, rev, path, description}",
      doc = "Moves a task read at rev to another status.",
    },
    ["todos.stores"] = {
      since = 20, scope = "global", audience = "agent",
      params = {},
      result = "[{id, path, roots, claimed, last_use}]",
      doc = "The stores the daemon knows.",
    },
    ["todos.update"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "id", type = "string" },
        { name = "rev", type = "string" },
        { name = "patch", type = "map" }
      },
      result = "task: {id, version, status, title, …, rev, path, description}",
      doc = "Changes hand-editable fields of a task read at rev; status and assignee have their own verbs.",
    },
    ["todos.vars"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" }
      },
      result = "[{name, value?, builtin}]",
      doc = "The variables a ref may start with, for an at's store.",
    },
    ["workspace.add"] = {
      since = 2, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "root", type = "string" },
        { name = "include", type = "[]string", optional = true },
        { name = "exclude", type = "[]string", optional = true }
      },
      result = "{name, root, state, …}",
      doc = "Adds a workspace and starts serving it; answers it as workspace.list does.",
    },
    ["workspace.configure"] = {
      since = 9, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "settings", type = "map" }
      },
      doc = "Saves the settings at once: all of them or none. An unknown key is invalid params. Logs the event each changed setting's own verb logs, a rename first.",
    },
    ["workspace.embedding_policy"] = {
      since = 6, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "policy", type = "string" }
      },
    },
    ["workspace.focus"] = {
      since = 6, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" }
      },
      doc = "Embeds this workspace's files first.",
    },
    ["workspace.list"] = {
      since = 1, scope = "global", audience = "agent",
      params = {},
      result = "[{name, root, state, include, exclude, section_tokens, embedding_policy, schema, text_extensions, text_collisions, provider, provider_error?, databases?, abstract_chunk, demote_superseded}]",
      doc = "Every workspace. Search only one whose state is ready. schema is {path, active, fields, error, line}; text_extensions are the extensions read as plain text besides .txt, text_collisions those of them the build reads with a registered chunker instead; provider is the workspace's own embedding provider (\"\": the daemon's); databases is {uid, destination, vector_index, view_args, source?, destination_connection?}, each connection {engine, host, database, user, schema, has_password}, never its DSN.",
    },
    ["workspace.remove"] = {
      since = 2, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" }
      },
      doc = "Stops serving a workspace and deletes its index; its files stay.",
    },
    ["workspace.rename"] = {
      since = 2, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "to", type = "string" }
      },
    },
    ["workspace.section_size"] = {
      since = 5, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "tokens", type = "int" }
      },
    },
    ["workspace.set_patterns"] = {
      since = 7, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "include", type = "[]string" },
        { name = "exclude", type = "[]string" }
      },
      doc = "Replaces the workspace's validated globs and reconciles it.",
    },
    ["workspace.set_provider"] = {
      since = 8, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "provider", type = "string" }
      },
      doc = "Gives the workspace a stored embedding provider of its own; it is set up first, and only that workspace re-embeds.",
    },
    ["workspace.set_schema"] = {
      since = 8, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" }
      },
      result = "{path, active, fields, error, line}",
      doc = "Names the workspace's frontmatter schema file; answers its status.",
    },
    ["workspace.set_text_extensions"] = {
      since = 8, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "extensions", type = "[]string" }
      },
      result = "[string]",
      doc = "Declares the workspace's own plain-text extensions; answers them normalized. One the build reads with a registered chunker is refused.",
    },
  },
  errors = {
    { code = -32001, name = "rpc.access_denied", next = "call it on the unix socket: it is for a local peer" },
    { code = -32020, name = "rpc.protocol_mismatch", next = "the daemon serves other protocols: autodoc --version, then restart it, or reconnect declaring one it serves" },
    { code = -32021, name = "rpc.handshake_required", next = "say sys.hello first" },
    { code = -32060, name = "rpc.no_such_workspace", next = "call workspace.list and use a listed name" },
    { code = -32061, name = "rpc.not_found", next = "refresh the listing: the path is wrong or not indexed (index.list), or the provider or ranker is gone" },
    { code = -32062, name = "rpc.conflict", next = "doc.read, then merge or retry; for a name, choose another" },
    { code = -32063, name = "rpc.cursor_expired", next = "re-list: take index.status's cursor (sys.hello's events head for sys.events), list, and resume from it" },
    { code = -32065, name = "rpc.unsupported", next = "this daemon or the workspace's filesystem cannot: do without it, or search with lexical among the stages" },
    { code = -32066, name = "rpc.committed", next = "the write landed: doc.read and compare, never re-send" },
    { code = -32067, name = "rpc.embed_failed", next = "retry later, or search with lexical among the stages" },
    { code = -32068, name = "rpc.provider_refused", next = "switch provider or ranker, or fix its key or base URL" },
    { code = -32069, name = "rpc.switching", next = "a new model is filling: search with lexical among the stages until it is ready" },
    { code = -32070, name = "rpc.rank_unavailable", next = "retry later, or leave rerank out of the stages for an unranked answer" },
    { code = -32071, name = "todos.stale", next = "todos.get it again, re-apply the change, and retry with its new rev" },
    { code = -32072, name = "todos.not_found", next = "list the tasks again" },
    { code = -32073, name = "todos.invalid", next = "fix the field and send it again" },
    { code = -32074, name = "todos.not_a_store", next = "todos.scaffold one, or todos.set_dir to another" },
    { code = -32075, name = "todos.exists", next = "pick another id, or another move_store target" },
    { code = -32076, name = "todos.not_writable", next = "todos.register the project's root, or todos.set_dir" },
    { code = -32077, name = "todos.unclaimed", next = "edit it through AutoVim until the cutover" },
    { code = -32601, name = "rpc.method_not_found", next = "the verb is unknown, or newer than the session's protocol: sys.capabilities lists the verbs it may call" },
    { code = -32602, name = "rpc.invalid_params", next = "the message says which param and why; check the verb's contract. A reindex refused with held: … names the way out" },
    { code = -32603, name = "rpc.internal", next = "a failure the daemon's log details: retry, or report it" },
  },
}
