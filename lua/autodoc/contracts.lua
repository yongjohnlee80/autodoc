-- The daemon's verb contracts, for autodoc.nvim: generated from the contracts this build serves by
-- go generate ./rpc (internal/contractsgen). Do not edit; autodoc --contracts prints the same.
-- autodoc.contract binds them as functions; the plugin and the binary ship from one commit.
return {
  protocol = 41,
  min_protocol = 12,
  verbs = {
    ["crew.adopt"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "from", type = "string" },
        { name = "to", type = "string" },
        { name = "run", type = "string" },
        { name = "epoch", type = "int" }
      },
      result = "{}",
      doc = "Fence an orphan scope and move its mail here.",
    },
    ["crew.attention"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "op", type = "string" },
        { name = "kind", type = "string", optional = true },
        { name = "text", type = "string", optional = true }
      },
      result = "{}",
      doc = "Raise or clear: the session waits on the user. alive: the session's permission-prompt reporter is running (since 41).",
    },
    ["crew.claim"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "scope", type = "string" },
        { name = "run", type = "string" },
        { name = "proc", type = "string", optional = true }
      },
      result = "{epoch}",
      doc = "Claim or reattach a scope with the process's run token. Idempotent.",
    },
    ["crew.discard"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "from", type = "string" },
        { name = "to", type = "string" },
        { name = "run", type = "string" },
        { name = "epoch", type = "int" }
      },
      result = "{}",
      doc = "Fence an orphan scope and fail its mail, telling senders.",
    },
    ["crew.endpoint"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "kind", type = "string" },
        { name = "locator", type = "string" },
        { name = "secret", type = "string", optional = true }
      },
      result = "{generation}",
      doc = "How to wake the calling session.",
    },
    ["crew.exit"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "scope", type = "string" },
        { name = "run", type = "string" },
        { name = "epoch", type = "int" },
        { name = "session", type = "string" },
        { name = "why", type = "string", optional = true }
      },
      result = "{}",
      doc = "A session ended; its bound deliveries fail (session_ended).",
    },
    ["crew.guide"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "kind", type = "string" }
      },
      result = "{markdown, protocol}",
      doc = "The crew and mail procedure for a CLI kind, for a host's agent guide.",
    },
    ["crew.list"] = {
      since = 37, scope = "global", audience = "agent",
      params = {},
      result = "[{codename, scope, kind, presence, reachable, prompts_reported}]",
      doc = "The addressbook. prompts_reported (since 41): the session's permission prompts are reported now, so a window may press Enter into it when it shows no permission attention.",
    },
    ["crew.orphans"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {},
      result = "[{scope, profiles, unread}]",
      doc = "AutoDoc scopes with a free holder and unhandled mail.",
    },
    ["crew.register"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "scope", type = "string" },
        { name = "run", type = "string" },
        { name = "epoch", type = "int" },
        { name = "codename", type = "string" },
        { name = "kind", type = "string" },
        { name = "workspace", type = "string" },
        { name = "instance", type = "string" },
        { name = "secret", type = "string" }
      },
      result = "{session, profile}",
      doc = "Start a session for (scope, codename) with a host-generated secret. Idempotent on (host, epoch, instance, secret).",
    },
    ["crew.release"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "scope", type = "string" },
        { name = "run", type = "string" },
        { name = "epoch", type = "int" }
      },
      result = "{}",
      doc = "Give a scope back on a clean exit; the run can't reattach.",
    },
    ["crew.renew"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "scope", type = "string" },
        { name = "run", type = "string" },
        { name = "epoch", type = "int" }
      },
      result = "{}",
      doc = "The host still holds its scope: its lease runs another 90 s.",
    },
    ["crew.takeover"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "scope", type = "string" },
        { name = "run", type = "string" },
        { name = "proc", type = "string", optional = true }
      },
      result = "{epoch}",
      doc = "Claim a held scope after the user agreed; the old owner is fenced and its sessions end.",
    },
    ["crew.wait"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "scope", type = "string" },
        { name = "run", type = "string" },
        { name = "epoch", type = "int" },
        { name = "sessions", type = "[]string" },
        { name = "timeout", type = "int", optional = true }
      },
      result = "{ready[{session, unhandled}], timed_out}",
      doc = "One long-poll for the host's tier-3 sessions (at most 25 s): which have mail waiting. A connection holds at most two long-polls.",
    },
    ["crew.whoami"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "store", type = "string", optional = true }
      },
      result = "{codename, scope, profile, session, store}",
      doc = "Who the credential belongs to.",
    },
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
      doc = "The text's frontmatter checked against the workspace's schema (with a version 2 schema, against its document type's fields; a type the schema does not name is rule unknown_type); only Markdown has frontmatter. A task in a todo store is checked by the todo format instead: rule todo-malformed for a file it can't read, todo-<code> for each errors[] entry.",
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
    ["embedding.catalog"] = {
      since = 31, scope = "global", audience = "user",
      params = {
        { name = "base", type = "string", optional = true }
      },
      result = "[{name, dims, context, download, memory?, multilingual, fit, recommended, installed}]",
      doc = "The embedding models AutoDoc knows, measured: where each fits on this machine (gpu, cpu, none, unknown), the one recommended, and whether the Ollama has it.",
    },
    ["embedding.estimate"] = {
      since = 31, scope = "global", audience = "user",
      params = {
        { name = "model", type = "string" },
        { name = "workspaces", type = "[]string", optional = true },
        { name = "base", type = "string", optional = true }
      },
      result = "{basis, fp?, workspaces: [{workspace, texts, logical_bytes, seconds?, why?}]}",
      doc = "What embedding the workspaces with model takes: its texts and their logical bytes, provisional before it is installed; once installed, against its real fingerprint, and timed on a sample of the workspace's own texts.",
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
    ["embedding.pull"] = {
      since = 31, scope = "global", audience = "user",
      params = {
        { name = "model", type = "string" },
        { name = "base", type = "string", optional = true }
      },
      result = "{job}",
      doc = "Starts pulling model into the Ollama, as a job the daemon owns: the request ending never stops it. One at a time.",
    },
    ["embedding.pull_cancel"] = {
      since = 31, scope = "global", audience = "user",
      params = {
        { name = "job", type = "string" }
      },
      result = "{job, model, state, completed, total, rate, eta?, error?}",
      doc = "Cancels a pull and answers its state: Ollama stops the download unless another client is pulling the same model, and a later pull resumes it.",
    },
    ["embedding.pull_status"] = {
      since = 31, scope = "global", audience = "user",
      params = {
        { name = "job", type = "string" }
      },
      result = "{job, model, state, completed, total, rate, eta?, error?}",
      doc = "A pull's state (running, done, failed, cancelled), its bytes, and the rate and time left; a job that is not the current one is not found.",
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
      doc = "Switches the daemon's provider; the new model fills, and search is by words until it has embedded every section.",
    },
    ["file.locate"] = {
      since = 17, scope = "local-peer", audience = "agent",
      params = {
        { name = "path", type = "string" }
      },
      result = "{workspace, path} | nil",
      doc = "The workspace that indexes the file (the most specific root, symlinks resolved) and its path there; nil for none. Under a gated script's bounds, the workspace that holds any file the script may read, indexed or not.",
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
      result = "[{path, raw, anchor, kind, resolved, external?}]",
      doc = "The files that link to it.",
    },
    ["graph.links"] = {
      since = 1, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "path", type = "string" },
        { name = "options", type = "map", optional = true }
      },
      result = "[{path, raw, anchor, kind, resolved, external?}]",
      doc = "The links the file makes.",
    },
    ["graph.map"] = {
      since = 26, scope = "workspace", audience = "agent",
      params = {
        { name = "workspace", type = "string" },
        { name = "options", type = "map", optional = true }
      },
      result = "{nodes: [{path, title, type, superseded, tags? (protocol 28)}], edges: [{src, dst, kind}], types? (protocol 28), next? (protocol 36)}",
      doc = "The workspace's documents and the resolved links among them; with opts.root, those within opts.depth (1–2) of it. From protocol 28: types are the document types the workspace's schema declares, in its order (none without one), and a node's type is its value of the schema's discriminator field (type without one). From protocol 36, a map without a root comes a page at a time: opts.limit documents (default 500, at most 2000) in path order after opts.after (the previous page's next, an opaque cursor), the links whose source is among them (to any document, one on a later page included), and next, \"\" at the end. Each link comes once; drop one whose document never came. A cursor from other kinds is invalid params. A session below 36 gets the whole map in one reply.",
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
    ["host.probe"] = {
      since = 31, scope = "global", audience = "user",
      params = {
        { name = "base", type = "string", optional = true }
      },
      result = "{gpu: {vendor, name, vram, compute}, ram, free, ollama}",
      doc = "What this machine offers an embedding model, read without a network: its GPU, RAM, free space, and the Ollama version (\"\" when none answers); 0 or \"\" is unknown.",
    },
    ["inbox.ack"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "message", type = "string" }
      },
      result = "{}",
      doc = "Mark a report read. Replaying an ack answers ok.",
    },
    ["inbox.dismiss"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "message", type = "string" }
      },
      result = "{}",
      doc = "Decline a request; its sender is told. Replaying a dismiss answers ok.",
    },
    ["inbox.focus"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "session", type = "string" }
      },
      result = "{session, owner}",
      doc = "Ask the session's current owner to raise its agent: the owner's host hears a focus change.",
    },
    ["inbox.list"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "filter", type = "string", optional = true },
        { name = "before", type = "int", optional = true },
        { name = "limit", type = "int", optional = true }
      },
      result = "{items[]}",
      doc = "The human's history, newest first, or the generated notices nobody could receive (filter undeliverable).",
    },
    ["inbox.reply"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "message", type = "string" },
        { name = "body", type = "string" },
        { name = "key", type = "string" }
      },
      result = "{message}",
      doc = "Answer a request; the first answer wins. The key is bound to the request and the body: a retry after a lost answer returns the same response.",
    },
    ["inbox.send"] = {
      since = 41, scope = "local-peer", audience = "user",
      params = {
        { name = "scope", type = "string" },
        { name = "run", type = "string" },
        { name = "epoch", type = "int" },
        { name = "to", type = "[]string" },
        { name = "body", type = "string" },
        { name = "key", type = "string" },
        { name = "kind", type = "string", optional = true },
        { name = "subject", type = "string", optional = true },
        { name = "fire", type = "bool", optional = true }
      },
      result = "{message}",
      doc = "Send the user's own message or request from the window that holds scope: its sender is the user, set by the daemon alone, and its recipients are woken as for any mail. With fire: inbox.unreachable means nothing was left, so the text may be sent some other way; any other error leaves the outcome unknown (it may still be delivered), so it must not be. The key is the user's, across windows (use a fresh random one per send); the same key answers the first send, ringing again with fire.",
    },
    ["inbox.watch"] = {
      since = 37, scope = "local-peer", audience = "user",
      params = {
        { name = "cursor", type = "int" },
        { name = "timeout", type = "int", optional = true },
        { name = "page", type = "string", optional = true }
      },
      result = "{cursor, snapshot?{items, attention, next}, changes[]}",
      doc = "The human feed: the changes after cursor, waiting up to timeout for one; a snapshot of what is open when the cursor is 0 or no longer retained, its attention then its open items, at most 500 entries and 2 MiB a page (page = next for the rest). Never leases. A connection holds at most two long-polls.",
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
      doc = "The files with their frontmatter, most recently updated first. sort is updated, path or indexed; fields the frontmatter fields to return (default title, type, status, updated, tags, abstract); tags every one; paths folders or files; facets as search's; missing the files that lack one of these fields; diagnosed (protocol 15) the files the schema diagnoses, with their diagnostics; after the previous page's next; limit up to 500, default 100. From protocol 35, next is an opaque cursor (a string, \"\" when none follow) for every session, whatever protocol it declared: pass it back as after unread. A cursor from another sort or other filters, or an integer offset, is invalid params.",
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
    ["mail.ack"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "delivery", type = "string" },
        { name = "lease", type = "string" }
      },
      result = "{}",
      doc = "Done with a delivery under its lease. Replaying an ack of the same lease answers ok.",
    },
    ["mail.inbox"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "limit", type = "int", optional = true }
      },
      result = "{unread, items[]}",
      doc = "A preview of the caller's eligible mail: never leases.",
    },
    ["mail.receive"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" }
      },
      result = "{deliveries[], more}",
      doc = "Lease the caller's next eligible deliveries, at most 8, in seq order; more=true until drained.",
    },
    ["mail.renew"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "delivery", type = "string" },
        { name = "lease", type = "string" }
      },
      result = "{}",
      doc = "Extend a lease the caller holds by 10 minutes.",
    },
    ["mail.reply"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "delivery", type = "string" },
        { name = "lease", type = "string" },
        { name = "body", type = "string" },
        { name = "key", type = "string" },
        { name = "subject", type = "string", optional = true }
      },
      result = "{message}",
      doc = "Ack a request's delivery and send its response, in one transaction. One response per request: a retry with the same content answers it.",
    },
    ["mail.send"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "to", type = "[]string" },
        { name = "body", type = "string" },
        { name = "key", type = "string" },
        { name = "kind", type = "string", optional = true },
        { name = "subject", type = "string", optional = true },
        { name = "ttl", type = "int", optional = true }
      },
      result = "{message, deliveries[]}",
      doc = "Store a message and its deliveries in one transaction. The same key answers the first result while the message is retained.",
    },
    ["mail.wait"] = {
      since = 37, scope = "global", audience = "agent",
      params = {
        { name = "credential", type = "string" },
        { name = "timeout", type = "int", optional = true }
      },
      result = "{ready, timed_out}",
      doc = "Answers when the caller has mail it may receive, or at timeout (at most 25 s). A connection holds at most two long-polls.",
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
    ["sys.check"] = {
      since = 39, scope = "local-peer", audience = "internal",
      params = {},
      result = "{quick, integrity, foreign_keys, secrets}",
      doc = "The store's whole check: integrity_check, foreign_key_check and every sealed secret opened with the store's key; a sound store answers empty lists. SQLite stores only.",
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
    ["sys.profile"] = {
      since = 32, scope = "local-peer", audience = "internal",
      params = {
        { name = "op", type = "string" },
        { name = "dir", type = "string", optional = true }
      },
      result = "{on, dir}",
      doc = "Starts (op start, in dir or the default), stops (op stop) or reports (op status) the daemon's memory monitor, --profile's; dir is where its samples, heap profiles and pprof.sock are, \"\" while it is off.",
    },
    ["sys.shutdown"] = {
      since = 1, scope = "local-peer", audience = "internal",
      params = {},
      doc = "Stops the daemon. Frozen across protocols.",
    },
    ["sys.stats"] = {
      since = 25, scope = "global", audience = "agent",
      params = {},
      result = "{pid, version, started_at, uptime, rss, peak_rss, cpu_seconds, go_memory, go_heap, goroutines, cpus}",
      doc = "The daemon's own process: its sizes in bytes (rss 0 where the platform cannot say), cpu_seconds its CPU so far, cumulative (a reader takes a rate between two calls), started_at in Unix seconds and uptime in seconds.",
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
        { name = "spec", type = "map" },
        { name = "scope", type = "string", optional = true }
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
        { name = "reason", type = "string", optional = true },
        { name = "scope", type = "string", optional = true }
      },
      result = "task: {id, version, status, title, …, rev, path, description}",
      doc = "Assigns a task read at rev (\"\" clears it); assigning an open task makes it in-progress, and the assignee is told.",
    },
    ["todos.changes"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "consumer", type = "string" },
        { name = "limit", type = "int", optional = true },
        { name = "scoped", type = "bool", optional = true }
      },
      result = "{changes?: [{seq, id, kind, op, from?, to?, fields?, reason?, scope?, hand?, at}], resync?, snapshot?, cursor}",
      doc = "A consumer's committed changes after its cursor, or a resync when it was away too long.",
    },
    ["todos.claim"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "opts", type = "map", optional = true }
      },
      result = "nil",
      doc = "Lets the daemon write a store todos.release gave back. A store never released needs none: its first write claims it.",
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
    ["todos.migrate_refs"] = {
      since = 22, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "opts", type = "map" }
      },
      result = "{candidates?: [{kind?, id, path, field, index, ref, to, under_workspace, under_project, suggested}], rewritten, skipped?}",
      doc = "Migrates refs a plan lists, Lua-era $WORKSPACE refs (to $PROJECT_ROOT) or refs into removed git worktrees (kind worktree, to the main worktree's copy): an apply rewrites exactly the reviewed list, journaled.",
    },
    ["todos.move_store"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "to", type = "string" }
      },
      result = "{store_id, store, root, override?, writable, claimed, released?}",
      doc = "Moves an at's store to a new directory; its claim, change log and cursors move with it.",
    },
    ["todos.open"] = {
      since = 20, scope = "global", audience = "agent",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "opts", type = "map", optional = true }
      },
      result = "{store_id, store, scaffolded: false}",
      doc = "Checks that an at's store exists. A kb_root is accepted and ignored: $KB_ROOT is the store's workspace's.",
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
      doc = "Gives the store back: the daemon only reads it, and a write no longer claims it, until todos.claim.",
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
      result = "{store_id, store, root, override?, writable, claimed, released?}",
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
      result = "{store_id, store, root, override?, writable, claimed, released?}",
      doc = "Points an at's root at another store directory, or back at <root>/.todo-list.",
    },
    ["todos.set_kb"] = {
      since = 24, scope = "global", audience = "user",
      params = {
        { name = "at", type = "workspace | {root}" },
        { name = "workspace", type = "string" }
      },
      result = "nil",
      doc = "Records the workspace an at's store belongs to: links a store outside every workspace, or moves one; its refs resolve with that workspace's variables.",
    },
    ["todos.set_var"] = {
      since = 20, scope = "global", audience = "user",
      params = {
        { name = "name", type = "string" },
        { name = "value", type = "string" },
        { name = "opts", type = "map", optional = true }
      },
      result = "nil",
      doc = "Sets or removes a variable: globally, in an at's store's workspace's KB file, or for this machine; a KB or machine value may override a built-in.",
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
      result = "[{id, path, roots, claimed, released?, last_use}]",
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
      result = "[{name, value?, builtin, derived?, source?, machine?, conflict?}]",
      doc = "The variables an at's store's refs may start with: its workspace's, the same from every route to the store; each effective value and its source.",
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
      result = "[{id, name, root, state, include, exclude, section_tokens, embedding_policy, schema, text_extensions, text_collisions, provider, provider_error?, databases?, abstract_chunk, demote_superseded, graph_only, todo_search, todo_excluded?}]",
      doc = "Every workspace. Search only one whose state is ready. id is the workspace's own id in the store, which a rename keeps: what to key a record of the workspace on. schema is {path, active, fields, error, line}; text_extensions are the extensions read as plain text besides .txt, text_collisions those of them the build reads with a registered chunker instead; provider is the workspace's own embedding provider (\"\": the daemon's); databases is {uid, destination, vector_index, view_args, source?, destination_connection?}, each connection {engine, host, database, user, schema, has_password, sealed?}, never its DSN; sealed is a connection whose DSN does not open with the store's key (a store restored without its key): enter it again.",
    },
    ["workspace.of"] = {
      since = 24, scope = "global", audience = "agent",
      params = {
        { name = "path", type = "string" }
      },
      result = "{workspace?, root?}",
      doc = "The workspace a file belongs to, the owner its refs resolve with: a todo store's file, the store's workspace; any other file, the deepest workspace containing it; none otherwise.",
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
    ["workspace.scaffold"] = {
      since = 27, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "files", type = "list" }
      },
      result = "[{path, outcome, error?}]",
      doc = "Creates a KB type's files under the workspace's root, create-only: a file that exists is kept. The root KB_OPERATIONS.md, marked managed, replaces a copy that declares an older autodoc_version. An immutable folder's descriptor is skipped when that folder holds anything. It writes whether or not the workspace indexes a file; one it indexes reaches the index as any new file does. outcome is created, kept, updated, skipped or refused (with error).",
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
    ["workspace.set_var"] = {
      since = 24, scope = "global", audience = "user",
      params = {
        { name = "workspace", type = "string" },
        { name = "name", type = "string" },
        { name = "value", type = "string" },
        { name = "opts", type = "map", optional = true }
      },
      result = "nil",
      doc = "Sets or removes a workspace's variable: this machine's value (scope machine, absolute) or the KB's (scope kb, relative to the KB root, carried by git); machine_only declares the name machine-only in the KB's file.",
    },
    ["workspace.vars"] = {
      since = 24, scope = "global", audience = "agent",
      params = {
        { name = "workspace", type = "string" }
      },
      result = "[{name, value?, builtin, derived?, source?, machine?, conflict?}]",
      doc = "A workspace's variables: the ones every document of it, its todo stores' tasks included, resolves $NAME refs with; each effective value and its source. \"\" is no workspace's: $HOME and the global variables, which a file no workspace owns resolves with.",
    },
  },
  errors = {
    { code = -32001, name = "rpc.access_denied", next = "call it on the unix socket: it is for a local peer" },
    { code = -32020, name = "rpc.protocol_mismatch", next = "the daemon serves other protocols: autodoc --version, then restart it, or reconnect declaring one it serves" },
    { code = -32021, name = "rpc.handshake_required", next = "say sys.hello first" },
    { code = -32022, name = "rpc.store_damaged", next = "the store is damaged and the daemon is stopping: autodoc --check-store reports it; restore it from a backup (docs/ops/backup-and-restore.md)" },
    { code = -32060, name = "rpc.no_such_workspace", next = "call workspace.list and use a listed name" },
    { code = -32061, name = "rpc.not_found", next = "refresh the listing: the path is wrong or not indexed (index.list), or the provider or ranker is gone" },
    { code = -32062, name = "rpc.conflict", next = "doc.read, then merge or retry; for a name, choose another" },
    { code = -32063, name = "rpc.cursor_expired", next = "re-list: take index.status's cursor (sys.hello's events head for sys.events), list, and resume from it" },
    { code = -32064, name = "rpc.long_polls_busy", next = "a connection holds at most two long-polls (crew.wait, mail.wait, inbox.watch): finish one first, or open another connection" },
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
    { code = -32077, name = "todos.unclaimed", next = "todos.claim it to write it again" },
    { code = -32090, name = "crew.scope_busy", next = "wait for its lease to lapse, or crew.takeover after the user agrees" },
    { code = -32091, name = "crew.stale_epoch", next = "claim the scope again; this owner was fenced" },
    { code = -32092, name = "crew.codename_busy", next = "offer the user to take it over, or pick another codename" },
    { code = -32093, name = "crew.instance_reused", next = "register a new instance" },
    { code = -32094, name = "crew.bad_credential", next = "the session ended: its host registers a new one" },
    { code = -32095, name = "crew.context_mismatch", next = "fix AUTODOC_CREW_CONTEXT or the working directory's .autodoc-crew" },
    { code = -32096, name = "crew.bad_endpoint", next = "register an endpoint of a known kind, with its locator" },
    { code = -32110, name = "mail.no_recipient", next = "crew.list the addressbook, and send to a name it shows" },
    { code = -32111, name = "mail.key_reused", next = "a retry sends the same content; new content takes a new key" },
    { code = -32112, name = "mail.too_large", next = "send a shorter body, and put the rest in a file the recipient can read" },
    { code = -32113, name = "mail.lease_lost", next = "mail.receive again: the delivery comes back if it is still unhandled" },
    { code = -32130, name = "inbox.already_answered", next = "inbox.list shows who answered, and when" },
    { code = -32131, name = "inbox.not_a_request", next = "inbox.ack a report" },
    { code = -32132, name = "inbox.expired", next = "its sender was told; nothing waits on it" },
    { code = -32133, name = "inbox.key_reused", next = "retry the same reply with the same key, or use a new key" },
    { code = -32134, name = "inbox.too_large" },
    { code = -32135, name = "inbox.unreachable", next = "type the text into its prompt, or send without fire to leave it queued" },
    { code = -32601, name = "rpc.method_not_found", next = "the verb is unknown, or newer than the session's protocol: sys.capabilities lists the verbs it may call" },
    { code = -32602, name = "rpc.invalid_params", next = "the message says which param and why; check the verb's contract. A reindex refused with held: … names the way out" },
    { code = -32603, name = "rpc.internal", next = "a failure the daemon's log details: retry, or report it" },
  },
}
