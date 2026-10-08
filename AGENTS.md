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
`sys.hello` with `{"protocol": 13, "name": "<your client>"}`: the protocol your client was written
for. The daemon serves every protocol from `min_protocol` (12) to its own (13); another is
refused, and so is every verb until the hello succeeds. A session keeps the protocol it declared: a
verb added after it answers as an unknown method (-32601), and the results it knew keep their shape
and meaning, though a result may gain keys, which a client ignores. `sys.capabilities` lists the
`verbs` the session may call. The name `autodoc-tui` is admitted at the daemon's own protocol only.

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

## The other verbs

Every verb but `workspace.*`, `preference.*`, `embedding.*`, `ranker.*`, `file.*` and `sys.*` takes
the workspace's name first.

| verb | parameters | answers |
| --- | --- | --- |
| `workspace.list` | — | `[{name, root, state, include, exclude, schema, text_extensions, text_collisions, provider, databases}]`; `provider` is the workspace's own embedding provider (`""`: the daemon's), with `provider_error` when it is not set up; `schema` is `{path, active, fields, error, line}`; `text_extensions` are the extensions read as plain text besides `.txt`; `text_collisions` are those of them the daemon's build reads with a registered chunker instead; `databases` is `{uid, destination, vector_index, view_args, source?, destination_connection?}`, each connection `{engine, host, database, user, schema, has_password}`, never its DSN |
| `workspace.configure` | workspace, settings map | save any of `name`, `include`+`exclude`, `schema`, `text_extensions`, `section_tokens`, `embedding_policy`, `provider`, and (where `sys.capabilities` says `databases`) `destination`, `vector_index`, `view_args`, `source` and `destination_connection` (`{engine, dsn, schema}` or `{remove: true}`; a blank `dsn` keeps the stored one) at once: all of it or none. An unknown key is InvalidParams. Logs the event each changed setting's own verb logs, a rename first |
| `workspace.set_patterns` | workspace, include list, exclude list | replace validated globs and reconcile that workspace; an empty include matches no files |
| `workspace.set_provider` | workspace, provider | give the workspace a stored embedding provider of its own (`""`: the daemon's again); set up first, and only that workspace re-embeds |
| `workspace.set_text_extensions` | workspace, list of extensions | declare the workspace's own plain-text extensions (`.log`); answers them normalized. Which files are indexed is still the patterns'. An extension the daemon's build reads with a registered chunker is refused |
| `workspace.set_schema` | workspace, path | name the frontmatter schema file (under the root, or absolute; `""` for none); answers the `schema` status |
| `search.query` | workspace, query, options? | see above |
| `doc.read` | workspace, path | `{content, version}`: the file's text, and its version. A PDF or DOCX a build derives answers its derived Markdown, read-only; when that is longer than one answer carries, it is cut at a paragraph's end and its last line, after a `---` rule, begins `[autodoc: truncated]`: that line is AutoDoc's, not the document's, so stop quoting before it |
| `doc.outline` | workspace, path | `{version, headings: [{id, level, text, line, byte}]}`: a Markdown file's headings in order, with the version they were read at; other kinds have none |
| `doc.validate` | workspace, path, content | `{diagnostics: [{field, line, rule, message}]}`: the text's frontmatter checked against the workspace's schema (with a version 2 schema, against its document type's fields; a type the schema does not name is rule `unknown_type`); only Markdown has frontmatter |
| `file.read` | absolute path | `{content, version}` of a UTF-8 text file anywhere on the daemon's disk, as `doc.read`. On the unix socket only (-32001 over TCP); a relative or unclean path, or a file that is not text, is InvalidParams. Protocol 17 |
| `file.write` | absolute path, content, version | `{version}`, as `doc.write`: at `version`, or `""` to create the file and its folders. Unix socket only. Protocol 17 |
| `file.locate` | absolute path | `{workspace, path}`: the workspace that indexes the file (the most specific root), its path there; `nil` for none. Unix socket only. Protocol 17 |
| `index.list` | workspace, after, limit | `{docs: [{path, generation, version}], more}`: every file in path order, after `after` (`""` from the start) |
| `index.documents` | workspace, options? | `{docs: [{path, generation, title, updated, indexed_at, fields}], more, next}`: the files with their frontmatter, most recently updated first. Options: `sort` (`updated`, `path` or `indexed`), `fields` (the frontmatter fields to return; default `title`, `type`, `status`, `updated`, `tags`, `abstract`), `tags` (every one), `paths` (folders or files), `facets` (as search's), `missing` (files that lack one of these fields), `after` (the previous page's `next`), `limit` (up to 500, default 100). `updated` is the frontmatter's, else when the index last read a change to the file. Protocol 13 |
| `index.status` | workspace | `{docs, pending_jobs, cursor, diagnosed, held, held_stale, held_unchecked, embeddings: {model, pending, semantic, …}, …}`; `diagnosed` counts files whose frontmatter has a problem; `held` the files another build indexed that this one holds (see `hold` above), `held_stale` and `held_unchecked` among them |
| `index.changes` | workspace, since, limit | `{cursor, changes: [{path, op, generation}], more}`: what changed after cursor `since` |
| `graph.links` | workspace, path | `[{path, raw, anchor, kind, resolved}]`: the links the file makes |
| `graph.backlinks` | workspace, path | the same shape: the files that link to it |
| `graph.neighborhood` | workspace, path, depth | `{nodes, edges: [{src, dst, kind}]}`: the files within `depth` links |
| `graph.unresolved` | workspace | `[{src, raw, reason}]`: links that name no file |
| `graph.resolve` | workspace, from, raw | `{path, reason}`: where one link, as written in the file at `from`, reaches now, saved or not, resolved as the index resolves links; `path` `""` with `reason` `missing` or `ambiguous`, or `""` when `raw` is no link the index keeps (a URL, a heading of `from`). Protocol 18 |
| `sys.hello` | `{protocol, name}` | `{protocol, server_protocol, min_protocol, server, version, instance, pid, addr, store_id, client, events}`: `protocol` is the session's, `server_protocol` and `min_protocol` the range the daemon serves, `store_id` the store's identity, `client` this connection's token, `events` the event log's head |
| `ranker.list` | — | `{rankers: [{name, kind, base_url, model, has_key}], active, window, error, supplied}`: the stored rankers (`kind` `tei` or `rerank-api`), the one in use, how many of the top candidates it ranks, why the one chosen is not in use, and the model of the build's own ranker (`""` for none), which is then the one in use |
| `sys.capabilities` | — | `{databases, registrations, ranker, verbs}`: `verbs` is what this session may call at its protocol; the rest is what this edition offers beyond the core (a client hides what is false, and the daemon refuses its settings), the build's registrations, `{chunkers: {ext: version}, formats: {ext: {id, version}}, fingerprint}`, empty for the community build, and `ranker`, `{supplied, model}`: whether the build supplies its own ranker |
| `sys.events` | since, limit (1 to 500) | `{cursor, events: [{seq, kind, workspace, client, detail, at}], more}`: configuration and lifecycle changes after cursor `since` (a model switch, a workspace's rules, schema, database settings (`workspace.databases`, never a connection) or removal), each with the token of the client that made it (`""` for the daemon itself). `since` −1 answers the head alone; an expired cursor is -32063 |

Writing files (`doc.write`, `doc.rename`, `doc.remove`, `file.write`), changing workspaces and choosing the
embedding model or the ranker (`ranker.add`, `update`, `remove`, `use`, `window`) are for the user's
tools, not an agent's search. Do not call them unless the user
asks you to change their files through AutoDoc.

## Errors

| code | meaning | what to do |
| --- | --- | --- |
| -32060 | no such workspace | call `workspace.list` and use a listed name |
| -32061 | no such file | the path is wrong or not indexed; `index.list` shows the paths |
| -32602 | invalid parameters | the message says which; check the table above. `held: …` is a reindex of a file this build cannot index again: the message names the way out |
| -32065 | not supported here | `stages: ["semantic"]` (or `mode: "semantic"`) with no model in use; search with `lexical` among the stages |
| -32067 | the query could not be embedded | retry, or search with `lexical` among the stages |
| -32069 | a new model is filling | search with `lexical` among the stages, or with no `stages` |
| -32070 | the ranker is unavailable, and this build answers ranked results or none | retry later, or leave `rerank` out of the stages for an unranked answer |
| -32020 | protocol mismatch | the daemon is another version: `autodoc --version`, then restart it |
