# AutoDoc for AI agents

AutoDoc indexes Markdown, plain-text and YAML files in folders called **workspaces**, and answers searches over them,
by words and by meaning. This file tells an AI agent how to find documents through it. Prefer it
to grepping the tree: it ranks by relevance, finds a file by what it means as well as the words it
shares with the question, follows links between files, and narrows by folder or tag.

## Calling it

A daemon (`autodoc --serve`) serves a unix socket, `$XDG_RUNTIME_DIR/autodoc.sock` by default
(`[server] socket` in `~/.config/autodoc/config.toml` moves it). From a shell, call it with
`autodoc --call`: the verb's name, then its parameters as **one JSON array**. The result is printed
as JSON on stdout.

```sh
autodoc --call workspace.list
autodoc --call search.query '["kb", "why did we store workspaces in sqlite", {"limit": 10}]'
autodoc --call doc.read '["kb", "adrs/0203-architecture.md"]'
```

- `--call` starts the daemon when nothing answers, as the TUI does. When another config's daemon
  already serves the same store, `--call` finds it there (the store's lease-info, confirmed by a
  probe) instead of starting a second one.
- A refusal exits 1 and prints `{"error": {"code": …, "message": …}}` on stderr. The codes are
  listed below.
- Paths are relative to the workspace's root, with `/` separators: `adrs/0203.md`, never an
  absolute path.
- Integers in the parameters are sent as integers; the verbs that take a number need one.

A program that speaks msgpack-rpc itself can dial the socket directly. Find the socket with
`autodoc --print-endpoint` (one line, `unix<TAB><socket>`; add `--ensure` to start the daemon first
when nothing answers), never by rules of your own. The first call on a connection must be
`sys.hello` with `{"protocol": N, "name": "<your client>"}`, where `N` is the protocol your client was
written for. The daemon serves every protocol from its `min_protocol` to its own; another is
refused, and so is every verb until the hello succeeds. To learn the range first, send `sys.hello`
with no protocol: that probe admits nothing and answers `min_protocol` and `server_protocol`. A verb may be written right behind the
hello, before its answer: it waits for the hello (3 s at most). A session keeps the protocol it declared: a
verb added after it answers as an unknown method (-32601), and the results it knew keep their shape
and meaning, though a result may gain keys, which a client ignores. `sys.capabilities` lists the
`verbs` the session may call. The name `autodoc-tui` is admitted at the daemon's own protocol only.
`autodoc --call` says hello as `autodoc-cli`, so what it changes is attributed to it in
`sys.events`; refused by an older daemon it still serves, it says hello again at that daemon's
protocol, on a new connection (a refused hello ends its session).

## What you can do for the user

You work for the person at the keyboard, in their workspace. Say what you'll do before you change
anything, and change their files or settings only when they ask.

- **Co-write documents.** Draft, extend and restructure notes in the workspace: `doc.read`,
  `doc.write` for files, `editor.*` for the page they have open (each edit is one undo step).
- **Configure AutoDoc for their machine.** Look at what it can run (GPU, memory, a local Ollama or
  TEI server), then propose embedding and reranker models and set them up when asked:
  `embedding.models`, `embedding.add`, `embedding.use`, `ranker.models`, `ranker.add`, `ranker.use`,
  `workspace.configure`. These verbs are for people's tools: call them only on the user's request.
- **Research a topic.** Search the workspace first (`search.query`), add what you find elsewhere if
  you can browse, and write it up as a note with its sources.
- **Proof-read as a professional editor.** Grammar, clarity, structure and consistency of terms;
  propose each change, and apply it only once they agree.
- **Make and use tools.** Lua commands and packages for what they repeat (`editor.add_command`,
  docs/lua), and AutoDoc's own verbs from the shell.
- **Find and explain.** Answer from their documents, citing the paths you read; explain AutoDoc's
  state (`index.status`, `workspace.list`, `sys.capabilities`); keep search precise with `.view`
  files and the frontmatter schema.

## A search, step by step

1. **Find the workspace.** `workspace.list` answers every workspace:
   `[{"name", "root", "state", "include", "exclude"}]`. Search only one whose `state` is `ready`.
   Choose the one whose `root` holds the tree you are asked about.
2. **Search it.** Use `search.query` (below). Read `hits` in order; they are ranked.
3. **Read what you need.** A hit's `line_start` and `line_end` are the lines its section spans in
   the file (1-based): read just those, with a line-ranged reader. `byte_start` and `byte_end` are
   the same span in bytes, and `breadcrumb` is the headings above it. A hit on a derived document
   (a PDF's text) has no lines. `doc.read` returns the whole file.
4. **Follow links** when the question is about how files relate. Use `graph.links` and
   `graph.backlinks` for one file, and `graph.neighborhood` for a file and its links a few hops
   out.

## `search.query` — `[workspace, query, options?]`

The query is words. Every word must appear in a hit found by words. The semantic side also finds
sections that mean the same thing in other words.

| query | means |
| --- | --- |
| `sqlite store` | sections with both words (stemmed: *stores*, *storing* match) |
| `sto*` | a prefix, on the last word |
| `"workspace manager"` | the phrase, in order |
| `sqlite "embedded database"` | a word and a phrase together |

FTS operators (`AND`, `OR`, `NOT`, `NEAR`, `-`, column filters) are **not** operators here: they are
searched as words.

When the workspace has a frontmatter schema, `field:value` for a field it declares is an exact
filter, not a word: `migration type:adr` finds *migration* among files whose `type` is `adr`. The
value is read as the field's type. A query of filters alone (`type:adr status:active`) lists the
files they admit, in path order, with `mode_used` `"facet"`. For a field the schema does not
declare, `field:value` is searched as words. A version 2 schema declares fields by document type
(a review's `verdict`, an ADR's `number`); every field of every type is a filter, and it matches the
files whose type declares it.

Options, a map, all optional:

| option | value | effect |
| --- | --- | --- |
| `limit` | integer, 1 to 200 (default 20) | how many hits |
| `stages` | list of `"lexical"`, `"semantic"`, `"rerank"` (default: all of them) | the stages to run; see below |
| `mode` | `"auto"`, `"lexical"`, `"semantic"` | the older way to choose: `auto` is every stage, `lexical` is `["lexical", "rerank"]`, `semantic` is `["semantic", "rerank"]`. Send `stages` or `mode`, not both |
| `paths` | list of strings | only under these: a folder (`"adrs"` covers `adrs/…`) or one file |
| `tags` | list of strings | only files that have **every** one of these tags (front matter `tags:` or `#tag`) |
| `facets` | map of field to a string or a list of strings | only files whose frontmatter field has one of the values; every field must match. The field must be one the workspace's schema declares (`workspace.list` shows its `schema`) |

The stages:

| `stages` | the search |
| --- | --- |
| absent (auto) | every stage the daemon can run: words and meaning fused, then ranked, when a model and a ranker are in use |
| `["lexical", "semantic"]` | words and meaning fused; when meaning cannot answer (no model, a model switching, the query not embedded), by words, and `semantic` is skipped |
| `["lexical"]` | words alone |
| `["semantic"]` | meaning alone; when meaning cannot answer, it **fails** (codes -32065, -32067, -32069), never by words |
| add `"rerank"` | a ranker in use re-orders the hits; with none, or one that fails, `rerank` is skipped |

- `stages` must name `lexical` or `semantic`: an empty list, `["rerank"]` alone, an unknown name or
  a name twice is refused with -32602.
- Without `rerank`, the hits are never ranked, even with a ranker in use: compare a search with and
  without it to see what the ranker changed.
- A build may answer ranked or not at all: then a search that asks for `rerank` (or auto) and cannot
  be ranked is refused with -32070. One that leaves `rerank` out is answered, unranked.

The answer:

```json
{
  "hits": [
    {"path": "adrs/0007.md", "breadcrumb": "ADR 0007 › Storage", "snippet": "…",
     "byte_start": 1204, "byte_end": 2310, "line_start": 41, "line_end": 77, "relevance": 0.94, "score": 0.031,
     "via": ["lexical", "semantic", "rank"], "rank_score": 0.87, "generation": 7, "hold": ""}
  ],
  "mode_used": "hybrid",
  "semantic": "ready",
  "rank": {"state": "ready", "model": "BAAI/bge-reranker-v2-m3", "error": ""},
  "stages": {"requested": ["lexical", "semantic", "rerank"], "performed": ["lexical", "semantic", "rerank"], "skipped": {}}
}
```

- At most three hits come from one file, so a long file cannot fill the list.
- `relevance` is 0 to 1 on a fixed scale. 1 means first by words and by meaning. A hit found one
  way alone scores about 0.5 at best, unless many files link to it or it has a tag matching the
  query, which boost it.
- `via` says which way found it.
- `hold` is `""` for a file this daemon indexed. Builds of AutoDoc share one index, and a file
  another build indexed with a chunker or a format this one lacks (a `.go` file a Pro build cut, say)
  is held: searchable, never indexed again. Its `hold` says how far to trust it:
  - `current`: the file is as it was indexed;
  - `stale`: the file changed since, so the section may have moved: read the file before you quote it;
  - `unchecked`: the daemon has not seen the file yet; treat it as `stale`.
- `semantic` says whether meaning was searched too:
  - `ready`: every file is embedded;
  - `partial`: some files are found by words only, until they are embedded;
  - `switching`: a new model is filling, so the search was by words; `mode: "semantic"` is
    refused with code -32069;
  - `error`: no model answered, so the search was by words;
  - `off`: no model is in use.

  With `off`, `error` or `switching`, rephrase with the exact words the files would use.
- `rank` says whether a ranker re-read the top hits and ordered them (a second stage, after words
  and meaning found them):
  - `ready`: the hits are in the ranker's order; `model` names it, and each hit it scored has a
    `rank_score` (higher is more relevant; 0 is a score), and `via` includes `"rank"`;
  - `error`: the ranker did not answer, so the hits are in the order they were found; `error` says
    why;
  - `off`: no ranker is in use, `rerank` was not asked for, or the query had no words to rank by
    (filters alone).

  A build may answer ranked or not at all: then a search that asked for `rerank` (or auto) and the
  ranker cannot rank is refused with code -32070, and no hits. Retry later, or leave `rerank` out.
- `stages` says how the answer was made: `requested` is the stages asked for (auto lists all
  three), `performed` the stages that shaped these hits, and `skipped` why each other one did not
  run, a constant sentence:
  - `semantic`: "no embedding model is in use", "the workspace's embedding policy pauses semantic
    search", "a new embedding model is filling" or "the query could not be embedded";
  - `rerank`: "no ranker is in use" or "the ranker could not rank this search";
  - any stage: "the query has no words". A query of filters alone runs only the scan that lists
    their files, which `performed` names `lexical`.

### Complex queries

Combine the options, and search more than once:

```sh
# the design decisions about storage, only among the ADRs
autodoc --call search.query '["kb", "storage decision", {"paths": ["adrs"], "limit": 10}]'
# files tagged both todo and autodoc that mention the TUI
autodoc --call search.query '["kb", "tui", {"tags": ["todo", "autodoc"]}]'
# two folders, words only, unranked, many hits
autodoc --call search.query '["kb", "migration*", {"paths": ["docs/ops", "sql"], "stages": ["lexical"], "limit": 50}]'
# the same question by meaning alone, ranked
autodoc --call search.query '["kb", "how we move the schema forward", {"stages": ["semantic", "rerank"]}]'
```

For a broad question, search for its key terms in two or three phrasings and read the files that
recur. For a precise one (a name, an error text), search the exact words with
`stages: ["lexical", "rerank"]`.

## The verbs

Every verb is below with its parameters, its answer, the protocol it arrived in and who it is for.
`autodoc --contracts` prints the same as JSON with no daemon (`--format md` as these tables; a verb
or `domain.*` after it narrows them), and `sys.contracts` answers it on a connection, for that
session's protocol. A parameter marked `?` is optional, and only trailing ones are. The verbs whose
first parameter is `workspace` take the workspace's name first.

Writing files (`doc.write`, `doc.rename`, `doc.remove`, `file.write`), changing workspaces and
preferences, and choosing the embedding model or the ranker are for people's tools, not an agent's
search: the tables say **for people's tools**. Do not call them unless the user asks you to change
their files or settings through AutoDoc (the jobs above say when). A verb marked *unix socket only* is refused over TCP
(-32001).

A refusal's code says what to do next; the errors table lists each with the messages it carries.

<!-- autodoc:contracts:start -->
<!-- generated from the verb contracts by go generate ./rpc; autodoc --contracts --format md prints the same. Do not edit. -->

Verbs at protocol 41; the daemon serves sessions from protocol 12.

### `crew`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `crew.adopt` | `from` string (the orphan scope (crew.orphans)), `to` string (the caller's own scope), `run` string, `epoch` int (the caller's epoch for to) | `{}` — Fence an orphan scope and move its mail here. Codes: -32090, -32091. | 37 | for people's tools; unix socket only | write |
| `crew.attention` | `credential` string, `op` string (raise or clear), `kind`? string (for raise: permission or blocked), `text`? string (for raise: what the session waits on) | `{}` — Raise or clear: the session waits on the user. Codes: -32094, -32091. | 37 | agent | write |
| `crew.claim` | `scope` string (autovim:<project>, nvim:<key> or autodoc:<id>), `run` string (the host process's run token, held in memory only), `proc`? string (<boot id>:<pid ns>:<pid>:<start time>; "" when unknown) | `{epoch}` — Claim or reattach a scope with the process's run token. Idempotent. Codes: -32090. | 37 | for people's tools; unix socket only | write |
| `crew.discard` | `from` string (the orphan scope (crew.orphans)), `to` string (the caller's own scope), `run` string, `epoch` int (the caller's epoch for to) | `{}` — Fence an orphan scope and fail its mail, telling senders. Codes: -32090, -32091. | 37 | for people's tools; unix socket only | write |
| `crew.endpoint` | `credential` string, `kind` string (claude, codex, agy or opencode: the session's own), `locator` string (JSON, kind-specific; never a secret. opencode's is the AutoDoc plugin's socket, its process and the session (protocol 40)), `secret`? string (agy only: its language server's token, held in memory for this endpoint, never stored (protocol 38)) | `{generation}` — How to wake the calling session. Codes: -32094, -32096, -32091. | 37 | agent | write |
| `crew.exit` | `scope` string, `run` string, `epoch` int, `session` string, `why`? string (exit (the default) or start_failed) | `{}` — A session ended; its bound deliveries fail (session_ended). Codes: -32091, -32094. | 37 | for people's tools; unix socket only | write |
| `crew.guide` | `kind` string (the CLI: claude, codex, agy, opencode …) | `{markdown, protocol}` — The crew and mail procedure for a CLI kind, for a host's agent guide. | 37 | for people's tools; unix socket only | read |
| `crew.list` | — | `[{codename, scope, kind, presence, reachable}]` — The addressbook. | 37 | agent | read |
| `crew.orphans` | — | `[{scope, profiles, unread}]` — AutoDoc scopes with a free holder and unhandled mail. | 37 | for people's tools; unix socket only | read |
| `crew.register` | `scope` string, `run` string, `epoch` int, `codename` string, `kind` string (claude, codex, agy, opencode …), `workspace` string (the workspace's uid), `instance` string (the host's spawn id), `secret` string (host-generated, 32+ random bytes; only its hash is kept) | `{session, profile}` — Start a session for (scope, codename) with a host-generated secret. Idempotent on (host, epoch, instance, secret). Codes: -32091, -32092, -32093. | 37 | for people's tools; unix socket only | write |
| `crew.release` | `scope` string, `run` string, `epoch` int | `{}` — Give a scope back on a clean exit; the run can't reattach. Codes: -32091. | 37 | for people's tools; unix socket only | write |
| `crew.renew` | `scope` string, `run` string, `epoch` int | `{}` — The host still holds its scope: its lease runs another 90 s. Codes: -32091. | 37 | for people's tools; unix socket only | write |
| `crew.takeover` | `scope` string (autovim:<project>, nvim:<key> or autodoc:<id>), `run` string (the host process's run token, held in memory only), `proc`? string (<boot id>:<pid ns>:<pid>:<start time>; "" when unknown) | `{epoch}` — Claim a held scope after the user agreed; the old owner is fenced and its sessions end. | 37 | for people's tools; unix socket only | write |
| `crew.wait` | `scope` string, `run` string, `epoch` int, `sessions` []string (the host's tier-3 sessions to wait on), `timeout`? int (milliseconds to wait for mail, at most 25000) | `{ready[{session, unhandled}], timed_out}` — One long-poll for the host's tier-3 sessions (at most 25 s): which have mail waiting. A connection holds at most two long-polls. Codes: -32091, -32064. | 37 | for people's tools; unix socket only | read |
| `crew.whoami` | `credential` string (the session context's "<session id>.<secret>"), `store`? string (the store id the session context names) | `{codename, scope, profile, session, store}` — Who the credential belongs to. Codes: -32094, -32095, -32091. | 37 | agent | read |

### `doc`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `doc.outline` | `workspace` string, `path` string | `{version, headings: [{id, level, text, line, byte}]}` — A Markdown file's headings in order, with the version they were read at; other kinds have none. Codes: -32060, -32061, -32065, -32001. | 8 | agent | read |
| `doc.read` | `workspace` string, `path` string | `{content, version}` — The file's text and its version. A format a build derives (PDF, DOCX) answers its derived Markdown, read-only; when that is longer than one answer carries, it is cut at a paragraph's end and its last line, after a --- rule, begins [autodoc: truncated]: that line is AutoDoc's, not the document's. Codes: -32060, -32061, -32065, -32001. | 1 | agent | read |
| `doc.read_raw` | `workspace` string, `path` string | `{content, version}` — doc.read on an HTML file's own bytes, where doc.read gives its derived text. Codes: -32060, -32061, -32065, -32001. | 19 | agent | read |
| `doc.remove` | `workspace` string, `path` string, `version` string (the version read) | Codes: -32060, -32061, -32062, -32065, -32001. | 1 | for people's tools | write |
| `doc.rename` | `workspace` string, `from` string, `to` string | Codes: -32060, -32061, -32062, -32065, -32001. | 1 | for people's tools | write |
| `doc.validate` | `workspace` string, `path` string, `content` bytes (the text, saved or not) | `{diagnostics: [{field, line, rule, message}]}` — The text's frontmatter checked against the workspace's schema (with a version 2 schema, against its document type's fields; a type the schema does not name is rule unknown_type); only Markdown has frontmatter. A task in a todo store is checked by the todo format instead: rule todo-malformed for a file it can't read, todo-<code> for each errors[] entry. Codes: -32060. | 8 | agent | read |
| `doc.write` | `workspace` string, `path` string, `content` bytes (binary, or a string), `version` string (the version read; "" to create the file) | `{version}` — Writes the file at version; a stale version is a conflict. Codes: -32060, -32061, -32062, -32066, -32065, -32001. | 1 | for people's tools | write |
| `doc.write_raw` | `workspace` string, `path` string, `content` bytes (binary, or a string), `version` string (the version read; "" to create the file) | `{version}` — doc.write on an HTML file's own bytes, which doc.write refuses. Codes: -32060, -32061, -32062, -32066, -32065, -32001. | 19 | for people's tools | write |

### `embedding`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `embedding.add` | `provider` map ({name, kind, base_url, model, context?, key?}: an absent key keeps the stored one, "" removes it) | `{name, kind, base_url, model, context, has_key}` Logs `embedding.providers`. Codes: -32065, -32062, -32068. | 3 | for people's tools | settings |
| `embedding.cancel_switch` | — | `{model}` — Ends the model switch under way and deletes the partial target's vectors (refused below protocol 16). Logs `embedding.cancelled`. Codes: -32065. | 4 | for people's tools | settings |
| `embedding.catalog` | `base`? string (the Ollama's URL; absent, embed.DefaultOllamaURL) | `[{name, dims, context, download, memory?, multilingual, fit, recommended, installed}]` — The embedding models AutoDoc knows, measured: where each fits on this machine (gpu, cpu, none, unknown), the one recommended, and whether the Ollama has it. Codes: -32065. | 31 | for people's tools | read |
| `embedding.estimate` | `model` string, `workspaces`? []string (absent: every workspace served), `base`? string (the Ollama's URL; absent, embed.DefaultOllamaURL) | `{basis, fp?, workspaces: [{workspace, texts, logical_bytes, seconds?, why?}]}` — What embedding the workspaces with model takes: its texts and their logical bytes, provisional before it is installed; once installed, against its real fingerprint, and timed on a sample of the workspace's own texts. Codes: -32065, -32060. | 31 | for people's tools | read |
| `embedding.log` | `name` string, `limit` int | `[{at, texts, tokens, millis, outcome}]` — A provider's recent calls. Codes: -32065, -32061. | 3 | for people's tools | read |
| `embedding.model_context` | `stored` string (the stored provider; "" for an unsaved one), `provider` map ({name, kind, base_url, model, context?, key?}) | `{maximum}` — The context window the provider's model reports, in tokens. Codes: -32065, -32061, -32068. | 5 | for people's tools | read |
| `embedding.models` | `provider` any (a stored provider's name, or an unsaved one's spec), `spec`? map (with a name: the edit asked about) | `[string]` — The models a provider offers. Codes: -32065, -32061, -32068. | 3 | for people's tools | read |
| `embedding.providers` | — | `{providers: [{name, kind, base_url, model, context, has_key}], active, error}` — The stored embedding providers, the one in use, and why the one chosen is not in use. Codes: -32065. | 3 | for people's tools | read |
| `embedding.pull` | `model` string, `base`? string (the Ollama's URL; absent, embed.DefaultOllamaURL) | `{job}` — Starts pulling model into the Ollama, as a job the daemon owns: the request ending never stops it. One at a time. Codes: -32065, -32062. | 31 | for people's tools | settings |
| `embedding.pull_cancel` | `job` string (embedding.pull's answer) | `{job, model, state, completed, total, rate, eta?, error?}` — Cancels a pull and answers its state: Ollama stops the download unless another client is pulling the same model, and a later pull resumes it. Codes: -32065, -32061. | 31 | for people's tools | settings |
| `embedding.pull_status` | `job` string (embedding.pull's answer) | `{job, model, state, completed, total, rate, eta?, error?}` — A pull's state (running, done, failed, cancelled), its bytes, and the rate and time left; a job that is not the current one is not found. Codes: -32065, -32061. | 31 | for people's tools | read |
| `embedding.remove` | `name` string | Deletes the provider and its model's vectors (refused below protocol 16). Logs `embedding.providers`. Codes: -32065, -32061. | 3 | for people's tools | settings |
| `embedding.update` | `name` string, `provider` map ({name, kind, base_url, model, context?, key?}: an absent key keeps the stored one while base_url stays on its server (another server needs the key again), "" removes it) | Logs `embedding.providers`. Codes: -32065, -32061, -32062, -32068. | 3 | for people's tools | settings |
| `embedding.usage` | `name` string, `days` int | `[{day, requests, texts, tokens, failures, limited}]` — A provider's usage by day. Codes: -32065, -32061. | 3 | for people's tools | read |
| `embedding.use` | `name` string | Switches the daemon's provider; the new model fills, and search is by words until it has embedded every section. Logs `embedding.switched`. Codes: -32065, -32061, -32068. | 3 | for people's tools | settings |

### `file`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `file.locate` | `path` string (absolute and clean) | `{workspace, path} \| nil` — The workspace that indexes the file (the most specific root, symlinks resolved) and its path there; nil for none. Under a gated script's bounds, the workspace that holds any file the script may read, indexed or not. Codes: -32001. | 17 | agent; unix socket only | read |
| `file.read` | `path` string (absolute and clean) | `{content, version}` — A UTF-8 text file anywhere on the daemon's disk, as doc.read; a file that is not text is invalid params. Codes: -32001, -32061, -32065. | 17 | agent; unix socket only | read |
| `file.read_raw` | `path` string (absolute and clean) | `{content, version}` — file.read on an HTML file's own bytes. Codes: -32001, -32061, -32065. | 19 | agent; unix socket only | read |
| `file.write` | `path` string (absolute and clean), `content` bytes (UTF-8 text), `version` string (the version read; "" to create the file and its folders) | `{version}` — As doc.write, on a file anywhere on the daemon's disk. A file a workspace indexes (file.locate's answer) is written by that workspace's doc.write, and indexed at once. Codes: -32001, -32061, -32062, -32066, -32065. | 17 | for people's tools; unix socket only | write |
| `file.write_raw` | `path` string (absolute and clean), `content` bytes (UTF-8 text), `version` string (the version read; "" to create the file and its folders) | `{version}` — file.write on an HTML file's own bytes; in a workspace, its doc.write_raw. Codes: -32001, -32061, -32062, -32066, -32065. | 19 | for people's tools; unix socket only | write |

### `graph`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `graph.backlinks` | `workspace` string, `path` string, `options`? map ({kinds} (protocol 14)) | `[{path, raw, anchor, kind, resolved, external?}]` — The files that link to it. Codes: -32060, -32061. | 1 | agent | read |
| `graph.links` | `workspace` string, `path` string, `options`? map ({kinds} (protocol 14)) | `[{path, raw, anchor, kind, resolved, external?}]` — The links the file makes. Codes: -32060, -32061. | 1 | agent | read |
| `graph.map` | `workspace` string, `options`? map ({root, depth, kinds}) | `{nodes: [{path, title, type, superseded, tags? (protocol 28)}], edges: [{src, dst, kind}], types? (protocol 28), next? (protocol 36)}` — The workspace's documents and the resolved links among them; with opts.root, those within opts.depth (1–2) of it. From protocol 28: types are the document types the workspace's schema declares, in its order (none without one), and a node's type is its value of the schema's discriminator field (type without one). From protocol 36, a map without a root comes a page at a time: opts.limit documents (default 500, at most 2000) in path order after opts.after (the previous page's next, an opaque cursor), the links whose source is among them (to any document, one on a later page included), and next, "" at the end. Each link comes once; drop one whose document never came. A cursor from other kinds is invalid params. A session below 36 gets the whole map in one reply. Codes: -32060, -32061. | 26 | agent | read |
| `graph.neighborhood` | `workspace` string, `path` string, `depth` int, `options`? map ({kinds} (protocol 14)) | `{nodes, edges: [{src, dst, kind}]}` — The files within depth links. Codes: -32060, -32061. | 1 | agent | read |
| `graph.resolve` | `workspace` string, `from` string (the file the link is written in), `raw` string (the link as written) | `{path, reason}` — Where one link reaches now, saved or not, as the index resolves links: path "" with reason missing or ambiguous, or "" when raw is no link the index keeps (a URL, a heading of from). Codes: -32060. | 18 | agent | read |
| `graph.unresolved` | `workspace` string, `options`? map ({kinds} (protocol 14)) | `[{src, raw, reason}]` — Links that name no file. Codes: -32060. | 1 | agent | read |

### `host`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `host.probe` | `base`? string (the Ollama's URL; absent, embed.DefaultOllamaURL) | `{gpu: {vendor, name, vram, compute}, ram, free, ollama}` — What this machine offers an embedding model, read without a network: its GPU, RAM, free space, and the Ollama version ("" when none answers); 0 or "" is unknown. Codes: -32065. | 31 | for people's tools | read |

### `inbox`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `inbox.ack` | `message` string | `{}` — Mark a report read. Replaying an ack answers ok. Codes: -32132. | 37 | for people's tools; unix socket only | write |
| `inbox.dismiss` | `message` string | `{}` — Decline a request; its sender is told. Replaying a dismiss answers ok. Codes: -32130, -32131, -32132. | 37 | for people's tools; unix socket only | write |
| `inbox.focus` | `session` string (the session whose agent to raise) | `{session, owner}` — Ask the session's current owner to raise its agent: the owner's host hears a focus change. Codes: -32061. | 37 | for people's tools; unix socket only | write |
| `inbox.list` | `filter`? string ("" for the history, or undeliverable), `before`? int (the history's seq to page below; 0 for the newest), `limit`? int (at most 200; default 50) | `{items[]}` — The human's history, newest first, or the generated notices nobody could receive (filter undeliverable). | 37 | for people's tools; unix socket only | read |
| `inbox.reply` | `message` string (the request's id), `body` string, `key` string (the idempotency key: a retry with the same body answers the first reply) | `{message}` — Answer a request; the first answer wins. The key is bound to the request and the body: a retry after a lost answer returns the same response. Codes: -32130, -32131, -32132, -32133, -32134. | 37 | for people's tools; unix socket only | write |
| `inbox.send` | `scope` string (the window's scope, which its host holds), `run` string (the host's run token), `epoch` int (the host's epoch), `to` []string (names in the scope, name@scope, or session:<id>), `body` string (at most 64 KiB), `key` string (the idempotency key: a retry with the same content answers the first send), `kind`? string (message (the default) or request), `subject`? string, `reachable`? bool (refuse, sending nothing, when a recipient can't be woken now) | `{message}` — Send the user's own message or request from the window that holds scope: its sender is the user, set by the daemon alone, and its recipients are woken as for any mail. The same key answers the first send. Codes: -32091, -32110, -32135, -32133, -32134. | 41 | for people's tools; unix socket only | write |
| `inbox.watch` | `cursor` int (the last answer's cursor; 0 for a snapshot), `timeout`? int (milliseconds to wait for a change, at most 25000), `page`? string (a snapshot's next: the rest of that snapshot, answered at once, its cursor echoed) | `{cursor, snapshot?{items, attention, next}, changes[]}` — The human feed: the changes after cursor, waiting up to timeout for one; a snapshot of what is open when the cursor is 0 or no longer retained, its attention then its open items, at most 500 entries and 2 MiB a page (page = next for the rest). Never leases. A connection holds at most two long-polls. Codes: -32064. | 37 | for people's tools; unix socket only | read |

### `index`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `index.changes` | `workspace` string, `since` int (a cursor), `limit` int | `{cursor, changes: [{path, op, generation}], more}` — What changed after cursor since. Codes: -32060, -32063. | 1 | agent | read |
| `index.documents` | `workspace` string, `opts`? map ({sort, fields, tags, paths, facets, missing, diagnosed, after, limit}) | `{docs: [{path, generation, title, updated, indexed_at, fields, diagnostics?}], more, next}` — The files with their frontmatter, most recently updated first. sort is updated, path or indexed; fields the frontmatter fields to return (default title, type, status, updated, tags, abstract); tags every one; paths folders or files; facets as search's; missing the files that lack one of these fields; diagnosed (protocol 15) the files the schema diagnoses, with their diagnostics; after the previous page's next; limit up to 500, default 100. From protocol 35, next is an opaque cursor (a string, "" when none follow) for every session, whatever protocol it declared: pass it back as after unread. A cursor from another sort or other filters, or an integer offset, is invalid params. Codes: -32060. | 13 | agent | read |
| `index.list` | `workspace` string, `after` string (a path; "" from the start), `limit` int | `{docs: [{path, generation, version}], more}` — Every file in path order, after after. Codes: -32060. | 1 | agent | read |
| `index.models` | `workspace` string | `[{fp, provider, name, dims, state, vectors, f32_bytes, bits_bytes, key_bytes}]` — The workspace's models and the room their vectors take. Codes: -32060. | 4 | for people's tools | read |
| `index.purge_model` | `workspace` string, `model` string (a model's fp, as index.models answers it) | Deletes a model's vectors; the active model and the target are refused. Codes: -32060. | 1 | for people's tools | settings |
| `index.reindex` | `workspace` string, `path` string | Indexes one file again; a held file is refused, naming the way out. Codes: -32060, -32061. | 1 | for people's tools | write |
| `index.status` | `workspace` string | `{docs, chunks, pending_jobs, cursor, oldest_retained, diagnosed, failing, held, held_stale, held_unchecked, following, warming?, embeddings?, …}` — diagnosed counts files whose frontmatter has a problem; held the files another build indexed that this one holds, held_stale and held_unchecked among them. Codes: -32060. | 1 | agent | read |

### `mail`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `mail.ack` | `credential` string, `delivery` string, `lease` string (the lease mail.receive gave) | `{}` — Done with a delivery under its lease. Replaying an ack of the same lease answers ok. Codes: -32094, -32091, -32113. | 37 | agent | write |
| `mail.inbox` | `credential` string, `limit`? int (items to show, at most 1000; 0 counts only) | `{unread, items[]}` — A preview of the caller's eligible mail: never leases. Codes: -32094, -32091. | 37 | agent | read |
| `mail.receive` | `credential` string | `{deliveries[], more}` — Lease the caller's next eligible deliveries, at most 8, in seq order; more=true until drained. Codes: -32094, -32091. | 37 | agent | write |
| `mail.renew` | `credential` string, `delivery` string, `lease` string (the lease mail.receive gave) | `{}` — Extend a lease the caller holds by 10 minutes. Codes: -32094, -32091, -32113. | 37 | agent | write |
| `mail.reply` | `credential` string, `delivery` string (the request's delivery), `lease` string, `body` string, `key` string (the reply's idempotency key), `subject`? string | `{message}` — Ack a request's delivery and send its response, in one transaction. One response per request: a retry with the same content answers it. Codes: -32094, -32091, -32113, -32111, -32112. | 37 | agent | write |
| `mail.send` | `credential` string (the session context's "<session id>.<secret>"), `to` []string (names in the scope, name@scope, user, or session:<id>), `body` string (at most 64 KiB), `key` string (the idempotency key: a retry with the same content answers the first send), `kind`? string (message (the default) or request), `subject`? string, `ttl`? int (seconds until the message expires; 0 never) | `{message, deliveries[]}` — Store a message and its deliveries in one transaction. The same key answers the first result while the message is retained. Codes: -32094, -32091, -32110, -32111, -32112. | 37 | agent | write |
| `mail.wait` | `credential` string, `timeout`? int (milliseconds to wait for mail, at most 25000) | `{ready, timed_out}` — Answers when the caller has mail it may receive, or at timeout (at most 25 s). A connection holds at most two long-polls. Codes: -32094, -32091, -32064. | 37 | agent | read |

### `preference`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `preference.list` | — | `{name: value}` — What clients keep between runs. Codes: -32065. | 3 | for people's tools | read |
| `preference.set` | `name` string, `value` string | A preference that has a verb of its own (embedding.use, ranker.use, ranker.window) is refused. Logs `preference.changed`. Codes: -32065. | 3 | for people's tools | settings |

### `ranker`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `ranker.add` | `ranker` map ({name, kind, base_url, model, key?}: an absent key keeps the stored one, "" removes it) | `{name, kind, base_url, model, has_key}` Logs `ranker.providers`. Codes: -32065, -32062, -32068. | 11 | for people's tools | settings |
| `ranker.list` | — | `{rankers: [{name, kind, base_url, model, has_key}], active, window, error, supplied}` — The stored rankers (kind tei or rerank-api), the one in use, how many of the top candidates it ranks, why the one chosen is not in use, and the model of the build's own ranker ("" for none), which is then the one in use. Codes: -32065. | 11 | for people's tools | read |
| `ranker.log` | `name` string, `limit` int | `[{at, texts, tokens, millis, outcome}]` — A ranker's recent calls. Codes: -32065, -32061. | 11 | for people's tools | read |
| `ranker.models` | `ranker` any (a stored ranker's name, or an unsaved one's spec), `spec`? map (with a name: the edit asked about) | `[string]` — A TEI server's one model; none for a rerank-API endpoint, whose model is typed. Codes: -32065, -32061, -32068. | 11 | for people's tools | read |
| `ranker.remove` | `name` string | Logs `ranker.providers`. Codes: -32065, -32061. | 11 | for people's tools | settings |
| `ranker.update` | `name` string, `ranker` map ({name, kind, base_url, model, key?}: an absent key keeps the stored one while base_url stays on its server (another server needs the key again), "" removes it) | Logs `ranker.providers`. Codes: -32065, -32061, -32062, -32068. | 11 | for people's tools | settings |
| `ranker.usage` | `name` string, `days` int | `[{day, requests, texts, tokens, failures, limited}]` — A ranker's usage by day. Codes: -32065, -32061. | 11 | for people's tools | read |
| `ranker.use` | `name` string ("" for none) | Refused while the build supplies its ranker. Logs `ranker.switched`. Codes: -32065, -32061, -32068. | 11 | for people's tools | settings |
| `ranker.window` | `window` int (how many candidates it ranks, 10 to 100) | Refused while the build supplies its ranker. Logs `ranker.switched`. Codes: -32065. | 11 | for people's tools | settings |

### `search`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `search.query` | `workspace` string, `query` string (words, "phrases", a prefix*, field:value filters), `opts`? map ({limit, stages, mode, tags, paths, facets, retrieval}) | `{hits: [{path, breadcrumb, snippet, byte_start, byte_end, line_start?, line_end?, relevance, score, via, rank_score?, superseded_by?, generation, hold}], mode_used, semantic, rank, stages, semantic_error?}` — The search, ranked: see search.query above. Codes: -32060, -32065, -32067, -32069, -32070. | 1 | agent | read |

### `sys`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `sys.capabilities` | — | `{databases, registrations, ranker, verbs}` — verbs is what this session may call at its protocol; the rest is what this edition offers beyond the core (a client hides what is false), the build's registrations ({chunkers: {ext: version}, formats: {ext: {id, version}}, fingerprint}, empty for the community build) and ranker, {supplied, model}. | 9 | agent | read |
| `sys.check` | — | `{quick, integrity, foreign_keys, secrets}` — The store's whole check: integrity_check, foreign_key_check and every sealed secret opened with the store's key; a sound store answers empty lists. SQLite stores only. Codes: -32065. | 39 | internal; unix socket only | read |
| `sys.contracts` | `filter`? string ("" or "*" for every verb, "domain.*" for a domain's, or a verb's name) | `{protocol, min_protocol, verbs: [{verb, since, scope, audience, params, result, errors, event, doc}], errors: [{code, name, next, messages}]}` — These tables: the verbs this session may call, and the codes they may answer. | 20 | agent | read |
| `sys.events` | `since` int (a cursor; -1 answers the head alone), `limit` int (1 to 500) | `{cursor, events: [{seq, kind, workspace, client, detail, at}], more}` — Configuration and lifecycle changes after cursor since, each with the token of the client that made it ("" for the daemon itself). Codes: -32065, -32063. | 8 | agent | read |
| `sys.hello` | `info`? map ({protocol, name}; none, or no protocol, is a probe that admits nothing) | `{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}` — Opens the session at the protocol it declares. protocol is the session's, server_protocol and min_protocol the range the daemon serves, store_id the store's identity, client this connection's token, events the event log's head. Codes: -32020. | 1 | internal | read |
| `sys.profile` | `op` string (start, stop or status), `dir`? string (start's directory; absent, autodoc-profile-serve-<pid> in the runtime directory) | `{on, dir}` — Starts (op start, in dir or the default), stops (op stop) or reports (op status) the daemon's memory monitor, --profile's; dir is where its samples, heap profiles and pprof.sock are, "" while it is off. Codes: -32001, -32602, -32065, -32603. | 32 | internal; unix socket only | settings |
| `sys.shutdown` | — | Stops the daemon. Frozen across protocols. Codes: -32001. | 1 | internal; unix socket only | settings |
| `sys.stats` | — | `{pid, version, started_at, uptime, rss, peak_rss, cpu_seconds, go_memory, go_heap, goroutines, cpus}` — The daemon's own process: its sizes in bytes (rss 0 where the platform cannot say), cpu_seconds its CPU so far, cumulative (a reader takes a rate between two calls), started_at in Unix seconds and uptime in seconds. | 25 | agent | read |

### `todos`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `todos.ack` | `at` workspace \| {root}, `consumer` string, `seq` int | `nil` — Advances a consumer's cursor once it has handled the changes up to seq. Codes: -32060, -32072. | 20 | for people's tools | write |
| `todos.add` | `at` workspace \| {root}, `spec` map ({title, id?, description?, status?, due?, priority?, assignee?, tags?, adr?, review?, blocked?}), `scope`? string (with an assignee, the assigner's scope, as todos.assign's (protocol 30)) | `task: {id, version, status, title, …, rev, path, description}` — Creates a task. With an assignee it is an assignment: open becomes in-progress, and the assignee is told. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32073, -32075. | 20 | agent | write |
| `todos.assign` | `at` workspace \| {root}, `id` string, `rev` string, `assignee` string ("" clears it), `reason`? string, `scope`? string (the assigner's scope, at most 256 bytes: kept on the change and the snapshot, never read (protocol 30)) | `task: {id, version, status, title, …, rev, path, description}` — Assigns a task read at rev ("" clears it); assigning an open task makes it in-progress, and the assignee is told. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32071, -32072. | 20 | agent | write |
| `todos.changes` | `at` workspace \| {root}, `consumer` string, `limit`? int (at most this many; 0 for the default), `scoped`? bool (the consumer reads each assignment's scope and delivers only its own; without it, a scoped assignment comes without its assignee (protocol 33)) | `{changes?: [{seq, id, kind, op, from?, to?, fields?, reason?, scope?, hand?, at}], resync?, snapshot?, cursor}` — A consumer's committed changes after its cursor, or a resync when it was away too long. Codes: -32060. | 20 | for people's tools | read |
| `todos.claim` | `at` workspace \| {root}, `opts`? map ({legacy_workspace}: the directory $WORKSPACE named when the store's refs were written, before AutoDoc owned it) | `nil` — Lets the daemon write a store todos.release gave back. A store never released needs none: its first write claims it. Logs `todos.changed`. Codes: -32060, -32076, -32073. | 20 | for people's tools | settings |
| `todos.get` | `at` workspace \| {root}, `id` string | `task: {id, version, status, title, …, rev, path, description}` — One task, with its rev and body. Codes: -32060, -32072, -32074. | 20 | agent | read |
| `todos.list` | `at` workspace \| {root}, `opts`? map ({buckets, tag, assignee, priority, has_errors}; buckets ["archived"] lists the months, ["archived/YYYY/MM"] one month) | `{tasks: [task: {id, version, status, title, …, rev, path, description}], malformed: [{path, reason}], partitions?: [{month, count}], cursor}` — The active buckets' tasks; archives only by month, on request. Codes: -32060, -32074, -32073. | 20 | agent | read |
| `todos.migrate_refs` | `at` workspace \| {root}, `opts` map ({plan: true} lists the store's $WORKSPACE refs and where each exists; {plan: true, worktrees: true} lists its refs into removed git worktrees and their main-worktree copies; {apply: [...]} rewrites exactly the listed ones) | `{candidates?: [{kind?, id, path, field, index, ref, to, under_workspace, under_project, suggested}], rewritten, skipped?}` — Migrates refs a plan lists, Lua-era $WORKSPACE refs (to $PROJECT_ROOT) or refs into removed git worktrees (kind worktree, to the main worktree's copy): an apply rewrites exactly the reviewed list, journaled. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32073. | 22 | for people's tools | write |
| `todos.move_store` | `at` workspace \| {root}, `to` string (the store's new directory; nothing may be there yet) | `{store_id, store, root, override?, writable, claimed, released?}` — Moves an at's store to a new directory; its claim, change log and cursors move with it. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32075. | 20 | for people's tools | settings |
| `todos.open` | `at` workspace \| {root}, `opts`? map ({kb_root}) | `{store_id, store, scaffolded: false}` — Checks that an at's store exists. A kb_root is accepted and ignored: $KB_ROOT is the store's workspace's. Codes: -32060, -32074. | 20 | agent | read |
| `todos.refresh` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `{scanned, moved, archived, errors_set, rewritten, malformed}` — Reconciles the active buckets: misplaced files, errors[], the 28-day archive. Never reads archives. Logs `todos.changed`. Codes: -32060, -32076, -32077. | 20 | agent | write |
| `todos.register` | `root` string (an existing directory: a project that is no AutoDoc workspace) | `{new}` — Enrolls a project root that is no AutoDoc workspace, so its .todo-list may be written; allowed before any claim. Codes: -32060, -32073. | 20 | agent | settings |
| `todos.release` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `nil` — Gives the store back: the daemon only reads it, and a write no longer claims it, until todos.claim. Logs `todos.changed`. Codes: -32060. | 20 | for people's tools | settings |
| `todos.remove` | `at` workspace \| {root}, `id` string, `rev` string | `nil` — Deletes a task read at rev. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32071, -32072. | 20 | agent | write |
| `todos.remove_malformed` | `at` workspace \| {root}, `path` string (the malformed file, store-relative) | `nil` — Deletes one malformed file; a file using the removed automation fields is never deleted by the daemon. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32072, -32073. | 20 | for people's tools | write |
| `todos.resolve` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `{store_id, store, root, override?, writable, claimed, released?}` — Where an at's store is, and whether the daemon may write it. Codes: -32060. | 20 | agent | read |
| `todos.scaffold` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `{store_id, store, scaffolded}` — Creates the at's .todo-list and its buckets. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32075. | 20 | agent | write |
| `todos.set_dir` | `at` workspace \| {root}, `dir` string (the store's directory; "" clears the override) | `{store_id, store, root, override?, writable, claimed, released?}` — Points an at's root at another store directory, or back at <root>/.todo-list. Logs `todos.changed`. Codes: -32060, -32073. | 20 | for people's tools | settings |
| `todos.set_kb` | `at` workspace \| {root}, `workspace` string (the workspace the store belongs to from now on) | `nil` — Records the workspace an at's store belongs to: links a store outside every workspace, or moves one; its refs resolve with that workspace's variables. Logs `todos.changed`. Codes: -32060, -32073. | 24 | for people's tools | settings |
| `todos.set_var` | `name` string, `value` string ("" removes it), `opts`? map ({at, scope}: scope global (the default without at), store (the at's store's workspace's KB file, a path relative to the KB root; the default with at) or machine (this machine's value for that workspace)) | `nil` — Sets or removes a variable: globally, in an at's store's workspace's KB file, or for this machine; a KB or machine value may override a built-in. Codes: -32060, -32073, -32076, -32077. | 20 | for people's tools | settings |
| `todos.status` | `at` workspace \| {root}, `id` string, `rev` string, `status` string (open, in-progress, deferred, completed or archived) | `task: {id, version, status, title, …, rev, path, description}` — Moves a task read at rev to another status. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32071, -32072, -32073. | 20 | agent | write |
| `todos.stores` | — | `[{id, path, roots, claimed, released?, last_use}]` — The stores the daemon knows. Codes: -32060. | 20 | agent | read |
| `todos.update` | `at` workspace \| {root}, `id` string, `rev` string (the rev the client read), `patch` map (hand-editable fields only: title, description, priority, due, tags, adr, review, blocked) | `task: {id, version, status, title, …, rev, path, description}` — Changes hand-editable fields of a task read at rev; status and assignee have their own verbs. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32071, -32072, -32073. | 20 | agent | write |
| `todos.vars` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `[{name, value?, builtin, derived?, source?, machine?, conflict?}]` — The variables an at's store's refs may start with: its workspace's, the same from every route to the store; each effective value and its source. Codes: -32060. | 20 | agent | read |

### `workspace`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `workspace.add` | `name` string, `root` string (an absolute directory), `include`? []string (globs), `exclude`? []string (globs) | `{name, root, state, …}` — Adds a workspace and starts serving it; answers it as workspace.list does. Logs `workspace.added`. Codes: -32062, -32065. | 2 | for people's tools | settings |
| `workspace.configure` | `workspace` string, `settings` map (any of name, include+exclude, schema, text_extensions, section_tokens, embedding_policy, provider, abstract_chunk, demote_superseded, graph_only, todo_search, and (where sys.capabilities says databases) destination, vector_index, view_args, source and destination_connection ({engine, dsn, schema} or {remove: true}; a blank dsn keeps the stored one)) | Saves the settings at once: all of them or none. An unknown key is invalid params. Logs the event each changed setting's own verb logs, a rename first. Logs `workspace.renamed \| workspace.patterns \| workspace.schema \| workspace.text_extensions \| workspace.section_size \| workspace.embedding_policy \| workspace.provider \| workspace.databases \| workspace.retrieval \| workspace.graph_only`. Codes: -32060, -32062, -32061, -32065, -32068. | 9 | for people's tools | settings |
| `workspace.embedding_policy` | `workspace` string, `policy` string (always, when opened or never) | Logs `workspace.embedding_policy`. Codes: -32060, -32065. | 6 | for people's tools | settings |
| `workspace.focus` | `workspace` string | Embeds this workspace's files first. Codes: -32060, -32065. | 6 | for people's tools | settings |
| `workspace.list` | — | `[{id, name, root, state, include, exclude, section_tokens, embedding_policy, schema, text_extensions, text_collisions, provider, provider_error?, databases?, abstract_chunk, demote_superseded, graph_only, todo_search, todo_excluded?}]` — Every workspace. Search only one whose state is ready. id is the workspace's own id in the store, which a rename keeps: what to key a record of the workspace on. schema is {path, active, fields, error, line}; text_extensions are the extensions read as plain text besides .txt, text_collisions those of them the build reads with a registered chunker instead; provider is the workspace's own embedding provider ("": the daemon's); databases is {uid, destination, vector_index, view_args, source?, destination_connection?}, each connection {engine, host, database, user, schema, has_password, sealed?}, never its DSN; sealed is a connection whose DSN does not open with the store's key (a store restored without its key): enter it again. | 1 | agent | read |
| `workspace.of` | `path` string (an absolute file path) | `{workspace?, root?}` — The workspace a file belongs to, the owner its refs resolve with: a todo store's file, the store's workspace; any other file, the deepest workspace containing it; none otherwise. Codes: -32060. | 24 | agent | read |
| `workspace.remove` | `name` string | Stops serving a workspace and deletes its index; its files stay. Logs `workspace.removed`. Codes: -32060, -32065. | 2 | for people's tools | settings |
| `workspace.rename` | `name` string, `to` string | Logs `workspace.renamed`. Codes: -32060, -32062, -32065. | 2 | for people's tools | settings |
| `workspace.scaffold` | `workspace` string, `files` list ([{path, content, managed?, immutable?}], at most 64) | `[{path, outcome, error?}]` — Creates a KB type's files under the workspace's root, create-only: a file that exists is kept. The root KB_OPERATIONS.md, marked managed, replaces a copy that declares an older autodoc_version. An immutable folder's descriptor is skipped when that folder holds anything. It writes whether or not the workspace indexes a file; one it indexes reaches the index as any new file does. outcome is created, kept, updated, skipped or refused (with error). Codes: -32060, -32065. | 27 | for people's tools | write |
| `workspace.section_size` | `workspace` string, `tokens` int (0 for the default) | Logs `workspace.section_size`. Codes: -32060, -32065. | 5 | for people's tools | settings |
| `workspace.set_patterns` | `workspace` string, `include` []string (globs; empty matches no files), `exclude` []string (globs) | Replaces the workspace's validated globs and reconciles it. Logs `workspace.patterns`. Codes: -32060, -32065. | 7 | for people's tools | settings |
| `workspace.set_provider` | `workspace` string, `provider` string (a stored provider; "" for the daemon's) | Gives the workspace a stored embedding provider of its own; it is set up first, and only that workspace re-embeds. Logs `workspace.provider`. Codes: -32060, -32061, -32065, -32068. | 8 | for people's tools | settings |
| `workspace.set_schema` | `workspace` string, `path` string (under the root, or absolute; "" for none) | `{path, active, fields, error, line}` — Names the workspace's frontmatter schema file; answers its status. Logs `workspace.schema`. Codes: -32060, -32065. | 8 | for people's tools | settings |
| `workspace.set_text_extensions` | `workspace` string, `extensions` []string (".log", …) | `[string]` — Declares the workspace's own plain-text extensions; answers them normalized. One the build reads with a registered chunker is refused. Logs `workspace.text_extensions`. Codes: -32060, -32065. | 8 | for people's tools | settings |
| `workspace.set_var` | `workspace` string, `name` string, `value` string ("" removes it), `opts`? map ({scope, machine_only}: scope machine (this machine's absolute value; the default) or kb (the KB's .autodoc-vars.toml, stored relative to the KB root); machine_only with scope machine also declares the name machine-only in the KB's file, so other machines report it as a value to set and a migration conflict on it is settled) | `nil` — Sets or removes a workspace's variable: this machine's value (scope machine, absolute) or the KB's (scope kb, relative to the KB root, carried by git); machine_only declares the name machine-only in the KB's file. Codes: -32060, -32073. | 24 | for people's tools | settings |
| `workspace.vars` | `workspace` string | `[{name, value?, builtin, derived?, source?, machine?, conflict?}]` — A workspace's variables: the ones every document of it, its todo stores' tasks included, resolves $NAME refs with; each effective value and its source. "" is no workspace's: $HOME and the global variables, which a file no workspace owns resolves with. Codes: -32060. | 24 | agent | read |

### Errors

| code | name | next | messages |
| --- | --- | --- | --- |
| -32001 | `rpc.access_denied` | call it on the unix socket: it is for a local peer |  |
| -32020 | `rpc.protocol_mismatch` | the daemon serves other protocols: autodoc --version, then restart it, or reconnect declaring one it serves |  |
| -32021 | `rpc.handshake_required` | say sys.hello first |  |
| -32022 | `rpc.store_damaged` | the store is damaged and the daemon is stopping: autodoc --check-store reports it; restore it from a backup (docs/ops/backup-and-restore.md) |  |
| -32060 | `rpc.no_such_workspace` | call workspace.list and use a listed name | "no such workspace" |
| -32061 | `rpc.not_found` | refresh the listing: the path is wrong or not indexed (index.list), or the provider or ranker is gone | "no such pull: it ended and another began, or the daemon restarted"; "no such embedding provider"; "no such ranker"; "not found"; "no such live session" |
| -32062 | `rpc.conflict` | doc.read, then merge or retry; for a name, choose another | "another workspace has this name or root"; "a model is being pulled: wait for it, or cancel it"; "another ranker has this name"; "another embedding provider has this name"; "the document is not at that version" |
| -32063 | `rpc.cursor_expired` | re-list: take index.status's cursor (sys.hello's events head for sys.events), list, and resume from it | "the event cursor is outside the retained log: take a snapshot and resume from the head"; "the change cursor is older than the retained log" |
| -32064 | `rpc.long_polls_busy` | a connection holds at most two long-polls (crew.wait, mail.wait, inbox.watch): finish one first, or open another connection | "a connection holds at most two long-polls" |
| -32065 | `rpc.unsupported` | this daemon or the workspace's filesystem cannot: do without it, or search with lexical among the stages | "this server keeps no preferences"; "this server keeps no embedding providers"; "this server keeps no event log"; "this server's store has no check here: a PostgreSQL store is checked by its database server"; "this server keeps no profiler"; "this server keeps no rankers"; "semantic search is not ready: no embedding provider is in use yet (the daemon may still be setting it up); search by words, or set one up in System › Search models"; "this edition has no database settings"; "this server's set of workspaces is fixed"; "this server serves no files outside its workspaces"; "this document's text could not be made now: try again later, or open it in its own viewer"; "this document's text could not be derived: open it in its own viewer"; "the workspace's filesystem cannot do this" |
| -32066 | `rpc.committed` | the write landed: doc.read and compare, never re-send | "the write landed, then a follow-up step failed: read the document and compare" |
| -32067 | `rpc.embed_failed` | retry later, or search with lexical among the stages | "the query could not be embedded" |
| -32068 | `rpc.provider_refused` | switch provider or ranker, or fix its key or base URL | "the ranker's usage limit is reached"; "the ranker refused the key"; "the ranker has no such model"; "the server's model is not a re-ranker"; "the ranker did not answer: check its base URL, and that it is running"; "the ranker's answer is not one score for each text"; "the ranker refused the request"; "the request is over the size a ranker is sent"; "the store's keyslot does not open this provider's key"; "the store's keyslot file is readable by others: make it 0600"; "the provider's usage limit is reached: switch provider, or wait"; "the provider refused the key"; "the provider has no such model: List models shows the ones it has"; "the provider did not answer: check its base URL, and that it is running"; "the model gave no usable vectors: it may be a chat model, not an embedding model"; "the provider rejected the probe text" |
| -32069 | `rpc.switching` | a new model is filling: search with lexical among the stages until it is ready | "a new model is filling: search by words until it is ready" |
| -32070 | `rpc.rank_unavailable` | retry later, or leave rerank out of the stages for an unranked answer | "the ranker is unavailable: this build answers ranked results or none" |
| -32071 | `todos.stale` | todos.get it again, re-apply the change, and retry with its new rev | "the task changed since it was read" |
| -32072 | `todos.not_found` | list the tasks again | "no such task" |
| -32073 | `todos.invalid` | fix the field and send it again | "a value the todo format or its rules refuse" |
| -32074 | `todos.not_a_store` | todos.scaffold one, or todos.set_dir to another | "there is no todo store there" |
| -32075 | `todos.exists` | pick another id, or another move_store target | "that already exists" |
| -32076 | `todos.not_writable` | todos.register the project's root, or todos.set_dir | "not a writable todo store: a workspace's or a registered root's .todo-list, or a set_dir override" |
| -32077 | `todos.unclaimed` | todos.claim it to write it again | "this todo store was released (todos.release): AutoDoc only reads it" |
| -32090 | `crew.scope_busy` | wait for its lease to lapse, or crew.takeover after the user agrees | "another live owner holds the scope" |
| -32091 | `crew.stale_epoch` | claim the scope again; this owner was fenced | "the scope changed hands since this epoch" |
| -32092 | `crew.codename_busy` | offer the user to take it over, or pick another codename | "the codename already has a live session" |
| -32093 | `crew.instance_reused` | register a new instance | "this instance was registered with another secret, codename, kind or workspace, or has ended" |
| -32094 | `crew.bad_credential` | the session ended: its host registers a new one | "no such live session" |
| -32095 | `crew.context_mismatch` | fix AUTODOC_CREW_CONTEXT or the working directory's .autodoc-crew | "two session contexts disagree, or one is for another store" |
| -32096 | `crew.bad_endpoint` | register an endpoint of a known kind, with its locator | "the daemon refuses that endpoint" |
| -32110 | `mail.no_recipient` | crew.list the addressbook, and send to a name it shows | "an address names no active profile or live session" |
| -32111 | `mail.key_reused` | a retry sends the same content; new content takes a new key | "the key already names another message" |
| -32112 | `mail.too_large` | send a shorter body, and put the rest in a file the recipient can read | "the body is past 64 KiB" |
| -32113 | `mail.lease_lost` | mail.receive again: the delivery comes back if it is still unhandled | "the caller no longer holds the delivery's lease" |
| -32130 | `inbox.already_answered` | inbox.list shows who answered, and when | "the request was already answered or dismissed" |
| -32131 | `inbox.not_a_request` | inbox.ack a report | "that message is no request to the user" |
| -32132 | `inbox.expired` | its sender was told; nothing waits on it | "the message expired first" |
| -32133 | `inbox.key_reused` | retry the same reply with the same key, or use a new key | "the key is bound to another request or reply" |
| -32134 | `inbox.too_large` |  | "the body is past 64 KiB" |
| -32135 | `inbox.unreachable` | type the text into its prompt, or send without reachable to leave it queued | "the agent can't be woken now: nothing was sent" |
| -32601 | `rpc.method_not_found` | the verb is unknown, or newer than the session's protocol: sys.capabilities lists the verbs it may call |  |
| -32602 | `rpc.invalid_params` | the message says which param and why; check the verb's contract. A reindex refused with held: … names the way out | "embedding policy must be always, when opened, or never"; "a preference needs a name"; "this preference is set by its own verb: embedding.use, ranker.use or ranker.window"; "the monitor did not start: the daemon's log says why (another monitor owns the directory, or it cannot be written)"; "no model switch is under way"; "a ranker needs a name, a kind (tei or rerank-api), a base URL, and a model for rerank-api"; "the base URL moves to another server: type the API key again, or remove it"; "this build supplies its ranker"; "a ranker's window is 10 to 100 candidates"; "a provider needs a name, a kind (ollama, ollama-cloud or openai), a base URL and a model"; "a provider's context window is 256 to 262144 tokens"; "an Ollama Cloud provider needs its API key"; "the root is not a directory"; "not a valid workspace: a name without a path separator, an absolute root, and valid patterns"; "a derived document is read-only: its text is served, its file never written"; "not a file of this workspace"; "the document is over the size limit"; "not a valid path in the workspace"; "unknown search mode"; "search stages must include lexical or semantic: a ranker only re-orders what they find"; "unknown search stage: the stages are lexical, semantic and rerank"; "a search stage is named twice"; "search stages and a search mode together: send one or the other"; "a facet filter names a field the workspace's schema does not declare"; "a facet filter's value is not of its field's type"; "the model is in use" |
| -32603 | `rpc.internal` | a failure the daemon's log details: retry, or report it | "the monitor stopped, but its last samples could not be written: the daemon's log says why" |

<!-- autodoc:contracts:end -->

## Todos: what by hand, what by verb

A workspace's tasks are Markdown files with YAML frontmatter under `.todo-list/`, in the buckets
`open/`, `in-progress/`, `deferred/` and `completed/`, and `archived/YYYY/MM/`. The daemon owns
their format and their rules; the `todos.*` verbs are how a change keeps them. Each verb takes
**where the store is** first: the workspace's name (`"kb"`), or `{"root": "/abs/project"}` for a
project that is no AutoDoc workspace.

```sh
autodoc --call todos.list '["kb"]'                                  # the active buckets
autodoc --call todos.list '["kb", {"buckets": ["archived"]}]'       # the archive's months
autodoc --call todos.get '["kb", "2026-10-11-ship-the-todos"]'      # one task, with its rev
autodoc --call todos.update '["kb", "2026-10-11-ship-the-todos", "<rev>", {"priority": "high"}]'
autodoc --contracts 'todos.*'                                       # every todos verb
```

| You want to | Do |
| --- | --- |
| read tasks | `todos.list`, `todos.get`; or read the files |
| change a task's **body** (the Markdown after the frontmatter) | edit the file |
| change its title, description, priority, due, tags, adr, review, blocked | `todos.update` with the `rev` you read |
| change its status | `todos.status` (it moves the file to its bucket and stamps the times) |
| assign it | `todos.assign`: assigning an open task makes it in-progress, and the assignee is told |
| add or remove one | `todos.add`, `todos.remove` |
| touch `id`, `version`, `created`, `updated`, `status_changed`, `completed_at`, `archived_at`, `errors` | never: the daemon writes them |

- Every write takes the `rev` you read. `todos.stale` (-32071) means the task changed since: get
  it again, re-apply your change, retry.
- A workspace's store is AutoDoc's from its first write: `todos.add` makes it if it isn't there,
  and nothing needs claiming first. Until then it reads as an empty store: `todos.list` gives no
  tasks and cursor 0, `todos.get` answers `todos.not_found`, and the reads make nothing. Only a store `todos.release` gave back answers
  `todos.unclaimed` (-32077); `todos.claim` lets AutoDoc write it again.
- Archives are read a month at a time, on request; `todos.list` without `buckets` never reads them.

## Choosing a surface

| You are | Use |
| --- | --- |
| an agent with a shell | `autodoc --call <verb> '<json params>'`; `autodoc --contracts` says what each verb takes |
| an agent whose tool bridges the daemon for it | the bridge's `todos.*` verbs; the KB's `kb.*` search verbs |
| a program that speaks msgpack-rpc | the socket (`autodoc --print-endpoint`), `sys.hello`, then the verbs |
| a script inside AutoDoc | Lua: `autodoc.call`, `autodoc.<domain>.<verb>` (Help › Tutorial, pages 9–11) |
| a tool with its own UI | a plugin (`docs/plugins.md`) |

Never edit the daemon's store (`autodoc.db`), its lease, or a `.todo-list`'s `.autodoc-owner`.

## AutoDoc's own guidance, with no daemon

The binary carries its guidance, so it can be read on any install, the Neovim plugin or not:

- `autodoc --kb-operations` prints `KB_OPERATIONS.md`, how an agent searches, reads and writes a KB,
  as a KB's scaffold writes it (stamped with this build's version).
- `autodoc --tutorial` lists the tutorial's pages; `autodoc --tutorial 6` prints page 6 (the search
  models).

## User themes (agent authoring)

Run `autodoc --theme-reference` for the installed format, every required colour role and a complete
example. Start with `autodoc --new-theme mytheme --from sepia`, edit the printed `.qml` file, then
run `autodoc --check-theme /path/themes/mytheme.qml` until it succeeds. These commands need no
daemon or UI. `--config` places new themes beside that config file. Built-in names cannot be
shadowed; every colour key is required. See [docs/themes.md](docs/themes.md).

## Diagrams and charts (agent authoring)

Before you write a Mermaid diagram or a Vega-Lite chart, read `autodoc --diagram-reference`: the
Mermaid kinds and the Vega-Lite subset AutoDoc's GUI draws, read from golib's own tables. Before you
show the user one, check that AutoDoc's GUI draws it:
`autodoc --check-diagram notes/flow.mmd notes/sales.chart.json notes/report.md`. It lays each one
out with golib's renderer, as the Rendered view does, and prints `file:line: ok` or `file:line: not
drawn: <golib's error>`; a `.md` is checked fence by fence. Exit 0 means every diagram is drawn, 1
that one is not, 2 that no file was given, 3 that this build has no GUI. A Mermaid diagram golib
does not draw still draws in an export (mermaid.js), so say which the user will see. Both commands
need the GUI build, and exit 3 without it.
