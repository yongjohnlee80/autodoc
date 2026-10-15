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
`sys.hello` with `{"protocol": 22, "name": "<your client>"}`: the protocol your client was written
for. The daemon serves every protocol from `min_protocol` (12) to its own (22); another is
refused, and so is every verb until the hello succeeds. A verb may be written right behind the
hello, before its answer: it waits for the hello (3 s at most). A session keeps the protocol it declared: a
verb added after it answers as an unknown method (-32601), and the results it knew keep their shape
and meaning, though a result may gain keys, which a client ignores. `sys.capabilities` lists the
`verbs` the session may call. The name `autodoc-tui` is admitted at the daemon's own protocol only.
`autodoc --call` says hello as `autodoc-cli`, so what it changes is attributed to it in
`sys.events`; refused by an older daemon it still serves, it says hello again at that daemon's
protocol, on a new connection (a refused hello ends its session).

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
    {"path": "adrs/0203.md", "breadcrumb": "ADR 0203 › Storage", "snippet": "…",
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
their files or settings through AutoDoc. A verb marked *unix socket only* is refused over TCP
(-32001).

A refusal's code says what to do next; the errors table lists each with the messages it carries.

<!-- autodoc:contracts:start -->
<!-- generated from the verb contracts by go generate ./rpc; autodoc --contracts --format md prints the same. Do not edit. -->

Verbs at protocol 22; the daemon serves sessions from protocol 12.

### `doc`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `doc.outline` | `workspace` string, `path` string | `{version, headings: [{id, level, text, line, byte}]}` — A Markdown file's headings in order, with the version they were read at; other kinds have none. Codes: -32060, -32061, -32065. | 8 | agent | read |
| `doc.read` | `workspace` string, `path` string | `{content, version}` — The file's text and its version. A format a build derives (PDF, DOCX) answers its derived Markdown, read-only; when that is longer than one answer carries, it is cut at a paragraph's end and its last line, after a --- rule, begins [autodoc: truncated]: that line is AutoDoc's, not the document's. Codes: -32060, -32061, -32065. | 1 | agent | read |
| `doc.read_raw` | `workspace` string, `path` string | `{content, version}` — doc.read on an HTML file's own bytes, where doc.read gives its derived text. Codes: -32060, -32061, -32065. | 19 | agent | read |
| `doc.remove` | `workspace` string, `path` string, `version` string (the version read) | Codes: -32060, -32061, -32062, -32065. | 1 | for people's tools | write |
| `doc.rename` | `workspace` string, `from` string, `to` string | Codes: -32060, -32061, -32062, -32065. | 1 | for people's tools | write |
| `doc.validate` | `workspace` string, `path` string, `content` bytes (the text, saved or not) | `{diagnostics: [{field, line, rule, message}]}` — The text's frontmatter checked against the workspace's schema (with a version 2 schema, against its document type's fields; a type the schema does not name is rule unknown_type); only Markdown has frontmatter. Codes: -32060. | 8 | agent | read |
| `doc.write` | `workspace` string, `path` string, `content` bytes (binary, or a string), `version` string (the version read; "" to create the file) | `{version}` — Writes the file at version; a stale version is a conflict. Codes: -32060, -32061, -32062, -32066, -32065. | 1 | for people's tools | write |
| `doc.write_raw` | `workspace` string, `path` string, `content` bytes (binary, or a string), `version` string (the version read; "" to create the file) | `{version}` — doc.write on an HTML file's own bytes, which doc.write refuses. Codes: -32060, -32061, -32062, -32066, -32065. | 19 | for people's tools | write |

### `embedding`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `embedding.add` | `provider` map ({name, kind, base_url, model, context?, key?}: an absent key keeps the stored one, "" removes it) | `{name, kind, base_url, model, context, has_key}` Logs `embedding.providers`. Codes: -32065, -32062, -32068. | 3 | for people's tools | settings |
| `embedding.cancel_switch` | — | `{model}` — Ends the model switch under way and deletes the partial target's vectors (refused below protocol 16). Logs `embedding.cancelled`. Codes: -32065. | 4 | for people's tools | settings |
| `embedding.log` | `name` string, `limit` int | `[{at, texts, tokens, millis, outcome}]` — A provider's recent calls. Codes: -32065, -32061. | 3 | for people's tools | read |
| `embedding.model_context` | `stored` string (the stored provider; "" for an unsaved one), `provider` map ({name, kind, base_url, model, context?, key?}) | `{maximum}` — The context window the provider's model reports, in tokens. Codes: -32065, -32061, -32068. | 5 | for people's tools | read |
| `embedding.models` | `provider` any (a stored provider's name, or an unsaved one's spec), `spec`? map (with a name: the edit asked about) | `[string]` — The models a provider offers. Codes: -32065, -32061, -32068. | 3 | for people's tools | read |
| `embedding.providers` | — | `{providers: [{name, kind, base_url, model, context, has_key}], active, error}` — The stored embedding providers, the one in use, and why the one chosen is not in use. Codes: -32065. | 3 | for people's tools | read |
| `embedding.remove` | `name` string | Deletes the provider and its model's vectors (refused below protocol 16). Logs `embedding.providers`. Codes: -32065, -32061. | 3 | for people's tools | settings |
| `embedding.update` | `name` string, `provider` map ({name, kind, base_url, model, context?, key?}: an absent key keeps the stored one, "" removes it) | Logs `embedding.providers`. Codes: -32065, -32061, -32062, -32068. | 3 | for people's tools | settings |
| `embedding.usage` | `name` string, `days` int | `[{day, requests, texts, tokens, failures, limited}]` — A provider's usage by day. Codes: -32065, -32061. | 3 | for people's tools | read |
| `embedding.use` | `name` string | Switches the daemon's provider; the new model fills while the old one answers. Logs `embedding.switched`. Codes: -32065, -32061, -32068. | 3 | for people's tools | settings |

### `file`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `file.locate` | `path` string (absolute and clean) | `{workspace, path} \| nil` — The workspace that indexes the file (the most specific root, symlinks resolved) and its path there; nil for none. Codes: -32001. | 17 | agent; unix socket only | read |
| `file.read` | `path` string (absolute and clean) | `{content, version}` — A UTF-8 text file anywhere on the daemon's disk, as doc.read; a file that is not text is invalid params. Codes: -32001, -32061, -32065. | 17 | agent; unix socket only | read |
| `file.read_raw` | `path` string (absolute and clean) | `{content, version}` — file.read on an HTML file's own bytes. Codes: -32001, -32061, -32065. | 19 | agent; unix socket only | read |
| `file.write` | `path` string (absolute and clean), `content` bytes (UTF-8 text), `version` string (the version read; "" to create the file and its folders) | `{version}` — As doc.write, on a file anywhere on the daemon's disk. A file a workspace indexes (file.locate's answer) is written by that workspace's doc.write, and indexed at once. Codes: -32001, -32061, -32062, -32066, -32065. | 17 | for people's tools; unix socket only | write |
| `file.write_raw` | `path` string (absolute and clean), `content` bytes (UTF-8 text), `version` string (the version read; "" to create the file and its folders) | `{version}` — file.write on an HTML file's own bytes; in a workspace, its doc.write_raw. Codes: -32001, -32061, -32062, -32066, -32065. | 19 | for people's tools; unix socket only | write |

### `graph`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `graph.backlinks` | `workspace` string, `path` string, `options`? map ({kinds} (protocol 14)) | `[{path, raw, anchor, kind, resolved}]` — The files that link to it. Codes: -32060, -32061. | 1 | agent | read |
| `graph.links` | `workspace` string, `path` string, `options`? map ({kinds} (protocol 14)) | `[{path, raw, anchor, kind, resolved}]` — The links the file makes. Codes: -32060, -32061. | 1 | agent | read |
| `graph.neighborhood` | `workspace` string, `path` string, `depth` int, `options`? map ({kinds} (protocol 14)) | `{nodes, edges: [{src, dst, kind}]}` — The files within depth links. Codes: -32060, -32061. | 1 | agent | read |
| `graph.resolve` | `workspace` string, `from` string (the file the link is written in), `raw` string (the link as written) | `{path, reason}` — Where one link reaches now, saved or not, as the index resolves links: path "" with reason missing or ambiguous, or "" when raw is no link the index keeps (a URL, a heading of from). Codes: -32060. | 18 | agent | read |
| `graph.unresolved` | `workspace` string, `options`? map ({kinds} (protocol 14)) | `[{src, raw, reason}]` — Links that name no file. Codes: -32060. | 1 | agent | read |

### `index`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `index.changes` | `workspace` string, `since` int (a cursor), `limit` int | `{cursor, changes: [{path, op, generation}], more}` — What changed after cursor since. Codes: -32060, -32063. | 1 | agent | read |
| `index.documents` | `workspace` string, `opts`? map ({sort, fields, tags, paths, facets, missing, diagnosed, after, limit}) | `{docs: [{path, generation, title, updated, indexed_at, fields, diagnostics?}], more, next}` — The files with their frontmatter, most recently updated first. sort is updated, path or indexed; fields the frontmatter fields to return (default title, type, status, updated, tags, abstract); tags every one; paths folders or files; facets as search's; missing the files that lack one of these fields; diagnosed (protocol 15) the files the schema diagnoses, with their diagnostics; after the previous page's next; limit up to 500, default 100. Codes: -32060. | 13 | agent | read |
| `index.list` | `workspace` string, `after` string (a path; "" from the start), `limit` int | `{docs: [{path, generation, version}], more}` — Every file in path order, after after. Codes: -32060. | 1 | agent | read |
| `index.models` | `workspace` string | `[{fp, provider, name, dims, state, vectors, f32_bytes, bits_bytes, key_bytes}]` — The workspace's models and the room their vectors take. Codes: -32060. | 4 | for people's tools | read |
| `index.purge_model` | `workspace` string, `model` string (a model's fp, as index.models answers it) | Deletes a model's vectors; the active model and the target are refused. Codes: -32060. | 1 | for people's tools | settings |
| `index.reindex` | `workspace` string, `path` string | Indexes one file again; a held file is refused, naming the way out. Codes: -32060, -32061. | 1 | for people's tools | write |
| `index.status` | `workspace` string | `{docs, chunks, pending_jobs, cursor, oldest_retained, diagnosed, failing, held, held_stale, held_unchecked, following, warming?, embeddings?, …}` — diagnosed counts files whose frontmatter has a problem; held the files another build indexed that this one holds, held_stale and held_unchecked among them. Codes: -32060. | 1 | agent | read |

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
| `ranker.update` | `name` string, `ranker` map ({name, kind, base_url, model, key?}: an absent key keeps the stored one, "" removes it) | Logs `ranker.providers`. Codes: -32065, -32061, -32062, -32068. | 11 | for people's tools | settings |
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
| `sys.contracts` | `filter`? string ("" or "*" for every verb, "domain.*" for a domain's, or a verb's name) | `{protocol, min_protocol, verbs: [{verb, since, scope, audience, params, result, errors, event, doc}], errors: [{code, name, next, messages}]}` — These tables: the verbs this session may call, and the codes they may answer. | 20 | agent | read |
| `sys.events` | `since` int (a cursor; -1 answers the head alone), `limit` int (1 to 500) | `{cursor, events: [{seq, kind, workspace, client, detail, at}], more}` — Configuration and lifecycle changes after cursor since, each with the token of the client that made it ("" for the daemon itself). Codes: -32065, -32063. | 8 | agent | read |
| `sys.hello` | `info`? map ({protocol, name}; none, or no protocol, is a probe that admits nothing) | `{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}` — Opens the session at the protocol it declares. protocol is the session's, server_protocol and min_protocol the range the daemon serves, store_id the store's identity, client this connection's token, events the event log's head. Codes: -32020. | 1 | internal | read |
| `sys.shutdown` | — | Stops the daemon. Frozen across protocols. Codes: -32001. | 1 | internal; unix socket only | settings |

### `todos`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `todos.ack` | `at` workspace \| {root}, `consumer` string, `seq` int | `nil` — Advances a consumer's cursor once it has handled the changes up to seq. Codes: -32060, -32072. | 20 | for people's tools | write |
| `todos.add` | `at` workspace \| {root}, `spec` map ({title, id?, description?, status?, due?, priority?, assignee?, tags?, adr?, review?, blocked?}) | `task: {id, version, status, title, …, rev, path, description}` — Creates a task. With an assignee it is an assignment: open becomes in-progress, and the assignee is told. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32073, -32075. | 20 | agent | write |
| `todos.assign` | `at` workspace \| {root}, `id` string, `rev` string, `assignee` string ("" clears it), `reason`? string | `task: {id, version, status, title, …, rev, path, description}` — Assigns a task read at rev ("" clears it); assigning an open task makes it in-progress, and the assignee is told. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32071, -32072. | 20 | agent | write |
| `todos.changes` | `at` workspace \| {root}, `consumer` string, `limit`? int (at most this many; 0 for the default) | `{changes?: [{seq, id, kind, op, from?, to?, fields?, reason?, hand?, at}], resync?, snapshot?, cursor}` — A consumer's committed changes after its cursor, or a resync when it was away too long. Codes: -32060. | 20 | for people's tools | read |
| `todos.claim` | `at` workspace \| {root}, `opts`? map ({legacy_workspace}: the directory auto-core's $WORKSPACE meant) | `nil` — The cutover: the daemon writes the store from now on. Only the release that removes auto-core's writer calls it. Logs `todos.changed`. Codes: -32060, -32076, -32073. | 20 | for people's tools | settings |
| `todos.get` | `at` workspace \| {root}, `id` string | `task: {id, version, status, title, …, rev, path, description}` — One task, with its rev and body. Codes: -32060, -32072, -32074. | 20 | agent | read |
| `todos.list` | `at` workspace \| {root}, `opts`? map ({buckets, tag, assignee, priority, has_errors}; buckets ["archived"] lists the months, ["archived/YYYY/MM"] one month) | `{tasks: [task: {id, version, status, title, …, rev, path, description}], malformed: [{path, reason}], partitions?: [{month, count}], cursor}` — The active buckets' tasks; archives only by month, on request. Codes: -32060, -32074, -32073. | 20 | agent | read |
| `todos.migrate_refs` | `at` workspace \| {root}, `opts` map ({plan: true} lists the store's $WORKSPACE refs and where each exists; {apply: [...]} rewrites exactly the listed ones to $PROJECT_ROOT) | `{candidates?: [{id, path, field, index, ref, to, under_workspace, under_project, suggested}], rewritten, skipped?}` — Migrates Lua-era $WORKSPACE refs: a plan lists them; an apply rewrites exactly the reviewed list to $PROJECT_ROOT, journaled. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32073. | 22 | for people's tools | write |
| `todos.move_store` | `at` workspace \| {root}, `to` string (the store's new directory; nothing may be there yet) | `{store_id, store, root, override?, writable, claimed}` — Moves an at's store to a new directory; its claim, change log and cursors move with it. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32075. | 20 | for people's tools | settings |
| `todos.open` | `at` workspace \| {root}, `opts`? map ({kb_root}) | `{store_id, store, scaffolded: false}` — Checks that an at's store exists. A kb_root is accepted and ignored: $KB_ROOT is the store's own. Codes: -32060, -32074. | 20 | agent | read |
| `todos.refresh` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `{scanned, moved, archived, errors_set, rewritten, malformed}` — Reconciles the active buckets: misplaced files, errors[], the 28-day archive. Never reads archives. Logs `todos.changed`. Codes: -32060, -32076, -32077. | 20 | agent | write |
| `todos.register` | `root` string (an existing directory: a project that is no AutoDoc workspace) | `{new}` — Enrolls a project root that is no AutoDoc workspace, so its .todo-list may be written; allowed before any claim. Codes: -32060, -32073. | 20 | agent | settings |
| `todos.release` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `nil` — The cutover's rollback: the daemon only reads the store again. Logs `todos.changed`. Codes: -32060. | 20 | for people's tools | settings |
| `todos.remove` | `at` workspace \| {root}, `id` string, `rev` string | `nil` — Deletes a task read at rev. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32071, -32072. | 20 | agent | write |
| `todos.remove_malformed` | `at` workspace \| {root}, `path` string (the malformed file, store-relative) | `nil` — Deletes one malformed file; a file using the removed automation fields is never deleted by the daemon. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32072, -32073. | 20 | for people's tools | write |
| `todos.resolve` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `{store_id, store, root, override?, writable, claimed}` — Where an at's store is, and whether the daemon may write it. Codes: -32060. | 20 | agent | read |
| `todos.scaffold` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `{store_id, store, scaffolded}` — Creates the at's .todo-list and its buckets. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32075. | 20 | agent | write |
| `todos.set_dir` | `at` workspace \| {root}, `dir` string (the store's directory; "" clears the override) | `{store_id, store, root, override?, writable, claimed}` — Points an at's root at another store directory, or back at <root>/.todo-list. Logs `todos.changed`. Codes: -32060, -32073. | 20 | for people's tools | settings |
| `todos.set_var` | `name` string, `value` string ("" removes it), `opts`? map ({at, scope}: scope global (the default without at), store (the store's .autodoc-vars.toml, the default with at) or machine (this machine's value for the store)) | `nil` — Sets or removes a variable: globally, in an at's store's file, or for this machine; a store or machine value may override a built-in. Codes: -32060, -32073, -32076, -32077. | 20 | for people's tools | settings |
| `todos.status` | `at` workspace \| {root}, `id` string, `rev` string, `status` string (open, in-progress, deferred, completed or archived) | `task: {id, version, status, title, …, rev, path, description}` — Moves a task read at rev to another status. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32071, -32072, -32073. | 20 | agent | write |
| `todos.stores` | — | `[{id, path, roots, claimed, last_use}]` — The stores the daemon knows. Codes: -32060. | 20 | agent | read |
| `todos.update` | `at` workspace \| {root}, `id` string, `rev` string (the rev the client read), `patch` map (hand-editable fields only: title, description, priority, due, tags, adr, review, blocked) | `task: {id, version, status, title, …, rev, path, description}` — Changes hand-editable fields of a task read at rev; status and assignee have their own verbs. Logs `todos.changed`. Codes: -32060, -32076, -32077, -32071, -32072, -32073. | 20 | agent | write |
| `todos.vars` | `at` workspace \| {root} (a workspace name, or {root = "/abs/project"}) | `[{name, value?, builtin, derived?, source?, machine?}]` — The variables an at's store's refs may start with, the same from every route to the store: each effective value and its source. Codes: -32060. | 20 | agent | read |

### `workspace`

| verb | params | result | since | audience | effect |
| --- | --- | --- | --- | --- | --- |
| `workspace.add` | `name` string, `root` string (an absolute directory), `include`? []string (globs), `exclude`? []string (globs) | `{name, root, state, …}` — Adds a workspace and starts serving it; answers it as workspace.list does. Logs `workspace.added`. Codes: -32062, -32065. | 2 | for people's tools | settings |
| `workspace.configure` | `workspace` string, `settings` map (any of name, include+exclude, schema, text_extensions, section_tokens, embedding_policy, provider, abstract_chunk, demote_superseded, and (where sys.capabilities says databases) destination, vector_index, view_args, source and destination_connection ({engine, dsn, schema} or {remove: true}; a blank dsn keeps the stored one)) | Saves the settings at once: all of them or none. An unknown key is invalid params. Logs the event each changed setting's own verb logs, a rename first. Logs `workspace.renamed \| workspace.patterns \| workspace.schema \| workspace.text_extensions \| workspace.section_size \| workspace.embedding_policy \| workspace.provider \| workspace.databases \| workspace.retrieval`. Codes: -32060, -32062, -32061, -32065, -32068. | 9 | for people's tools | settings |
| `workspace.embedding_policy` | `workspace` string, `policy` string (always, when opened or never) | Logs `workspace.embedding_policy`. Codes: -32060, -32065. | 6 | for people's tools | settings |
| `workspace.focus` | `workspace` string | Embeds this workspace's files first. Codes: -32060, -32065. | 6 | for people's tools | settings |
| `workspace.list` | — | `[{name, root, state, include, exclude, section_tokens, embedding_policy, schema, text_extensions, text_collisions, provider, provider_error?, databases?, abstract_chunk, demote_superseded}]` — Every workspace. Search only one whose state is ready. schema is {path, active, fields, error, line}; text_extensions are the extensions read as plain text besides .txt, text_collisions those of them the build reads with a registered chunker instead; provider is the workspace's own embedding provider ("": the daemon's); databases is {uid, destination, vector_index, view_args, source?, destination_connection?}, each connection {engine, host, database, user, schema, has_password}, never its DSN. | 1 | agent | read |
| `workspace.remove` | `name` string | Stops serving a workspace and deletes its index; its files stay. Logs `workspace.removed`. Codes: -32060, -32065. | 2 | for people's tools | settings |
| `workspace.rename` | `name` string, `to` string | Logs `workspace.renamed`. Codes: -32060, -32062, -32065. | 2 | for people's tools | settings |
| `workspace.section_size` | `workspace` string, `tokens` int (0 for the default) | Logs `workspace.section_size`. Codes: -32060, -32065. | 5 | for people's tools | settings |
| `workspace.set_patterns` | `workspace` string, `include` []string (globs; empty matches no files), `exclude` []string (globs) | Replaces the workspace's validated globs and reconciles it. Logs `workspace.patterns`. Codes: -32060, -32065. | 7 | for people's tools | settings |
| `workspace.set_provider` | `workspace` string, `provider` string (a stored provider; "" for the daemon's) | Gives the workspace a stored embedding provider of its own; it is set up first, and only that workspace re-embeds. Logs `workspace.provider`. Codes: -32060, -32061, -32065, -32068. | 8 | for people's tools | settings |
| `workspace.set_schema` | `workspace` string, `path` string (under the root, or absolute; "" for none) | `{path, active, fields, error, line}` — Names the workspace's frontmatter schema file; answers its status. Logs `workspace.schema`. Codes: -32060, -32065. | 8 | for people's tools | settings |
| `workspace.set_text_extensions` | `workspace` string, `extensions` []string (".log", …) | `[string]` — Declares the workspace's own plain-text extensions; answers them normalized. One the build reads with a registered chunker is refused. Logs `workspace.text_extensions`. Codes: -32060, -32065. | 8 | for people's tools | settings |

### Errors

| code | name | next | messages |
| --- | --- | --- | --- |
| -32001 | `rpc.access_denied` | call it on the unix socket: it is for a local peer |  |
| -32020 | `rpc.protocol_mismatch` | the daemon serves other protocols: autodoc --version, then restart it, or reconnect declaring one it serves |  |
| -32021 | `rpc.handshake_required` | say sys.hello first |  |
| -32060 | `rpc.no_such_workspace` | call workspace.list and use a listed name | "no such workspace" |
| -32061 | `rpc.not_found` | refresh the listing: the path is wrong or not indexed (index.list), or the provider or ranker is gone | "no such embedding provider"; "no such ranker"; "not found" |
| -32062 | `rpc.conflict` | doc.read, then merge or retry; for a name, choose another | "another workspace has this name or root"; "another ranker has this name"; "another embedding provider has this name"; "the document is not at that version" |
| -32063 | `rpc.cursor_expired` | re-list: take index.status's cursor (sys.hello's events head for sys.events), list, and resume from it | "the event cursor is outside the retained log: take a snapshot and resume from the head"; "the change cursor is older than the retained log" |
| -32065 | `rpc.unsupported` | this daemon or the workspace's filesystem cannot: do without it, or search with lexical among the stages | "this server keeps no preferences"; "this server keeps no embedding providers"; "this server keeps no event log"; "this server keeps no rankers"; "semantic search is not ready: no embedding provider is in use yet (the daemon may still be setting it up); search by words, or set one up in System › Search models"; "this edition has no database settings"; "this server's set of workspaces is fixed"; "this server serves no files outside its workspaces"; "this document's text could not be made now: try again later, or open it in its own viewer"; "this document's text could not be derived: open it in its own viewer"; "the workspace's filesystem cannot do this" |
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
| -32077 | `todos.unclaimed` | edit it through AutoVim until the cutover | "the daemon does not write this store yet: auto-core does, until the cutover claims it" |
| -32601 | `rpc.method_not_found` | the verb is unknown, or newer than the session's protocol: sys.capabilities lists the verbs it may call |  |
| -32602 | `rpc.invalid_params` | the message says which param and why; check the verb's contract. A reindex refused with held: … names the way out | "embedding policy must be always, when opened, or never"; "a preference needs a name"; "this preference is set by its own verb: embedding.use, ranker.use or ranker.window"; "no model switch is under way"; "a ranker needs a name, a kind (tei or rerank-api), a base URL, and a model for rerank-api"; "this build supplies its ranker"; "a ranker's window is 10 to 100 candidates"; "a provider needs a name, a kind (ollama, ollama-cloud or openai), a base URL and a model"; "a provider's context window is 256 to 262144 tokens"; "an Ollama Cloud provider needs its API key"; "the root is not a directory"; "not a valid workspace: a name without a path separator, an absolute root, and valid patterns"; "a derived document is read-only: its text is served, its file never written"; "not a file of this workspace"; "the document is over the size limit"; "not a valid path in the workspace"; "unknown search mode"; "search stages must include lexical or semantic: a ranker only re-orders what they find"; "unknown search stage: the stages are lexical, semantic and rerank"; "a search stage is named twice"; "search stages and a search mode together: send one or the other"; "a facet filter names a field the workspace's schema does not declare"; "a facet filter's value is not of its field's type"; "the model is in use" |
| -32603 | `rpc.internal` | a failure the daemon's log details: retry, or report it |  |

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
- Until the cutover a store is written by AutoVim's auto-core, and the daemon's writes answer
  `todos.unclaimed` (-32077): change tasks through AutoVim (its mailbox `todos.*` verbs) then.
- Archives are read a month at a time, on request; `todos.list` without `buckets` never reads them.

## Choosing a surface

| You are | Use |
| --- | --- |
| an agent with a shell | `autodoc --call <verb> '<json params>'`; `autodoc --contracts` says what each verb takes |
| an agent with only the auto-agents mailbox | its `todos.*` verbs; the KB's `kb.*` search verbs |
| a program that speaks msgpack-rpc | the socket (`autodoc --print-endpoint`), `sys.hello`, then the verbs |
| a script inside AutoDoc | Lua: `autodoc.call`, `autodoc.<domain>.<verb>` (Help › Tutorial, pages 7–9) |
| a tool with its own UI | a plugin (`docs/plugins.md`) |

Never edit the daemon's store (`autodoc.db`), its lease, or a `.todo-list`'s `.autodoc-owner`.

## User themes (agent authoring)

Run `autodoc --theme-reference` for the installed format, every required colour role and a complete
example. Start with `autodoc --new-theme mytheme --from sepia`, edit the printed `.qml` file, then
run `autodoc --check-theme /path/themes/mytheme.qml` until it succeeds. These commands need no
daemon or UI. `--config` places new themes beside that config file. Built-in names cannot be
shadowed; every colour key is required. See [docs/themes.md](docs/themes.md).
