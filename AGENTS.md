# AutoDoc for AI agents

AutoDoc indexes Markdown, plain-text and YAML files in folders called **workspaces**, and answers searches over them,
by words and by meaning. This file tells an AI agent how to find documents through it. Prefer it
to grepping the tree: it ranks by relevance, finds a note by what it means as well as the words it
shares with the question, follows links between notes, and narrows by folder or tag.

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

- `--call` starts the daemon when nothing answers, as the TUI does.
- A refusal exits 1 and prints `{"error": {"code": …, "message": …}}` on stderr. The codes are
  listed below.
- Paths are relative to the workspace's root, with `/` separators: `adrs/0203.md`, never an
  absolute path.
- Integers in the parameters are sent as integers; the verbs that take a number need one.

A program that speaks msgpack-rpc itself can dial the socket directly. The first call on a
connection must be `sys.hello` with `{"protocol": 8, "name": "<your client>"}`. Any other protocol
number is refused, and so is every verb until the hello succeeds.

## A search, step by step

1. **Find the workspace.** `workspace.list` answers every workspace:
   `[{"name", "root", "state", "include", "exclude"}]`. Search only one whose `state` is `ready`.
   Choose the one whose `root` holds the tree you are asked about.
2. **Search it.** Use `search.query` (below). Read `hits` in order; they are ranked.
3. **Read what you need.** `doc.read` returns the whole note. A hit's `byte_start` and `byte_end`
   are its section in the note's bytes, and `breadcrumb` is the headings above it.
4. **Follow links** when the question is about how notes relate. Use `graph.links` and
   `graph.backlinks` for one note, and `graph.neighborhood` for a note and its links a few hops
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
filter, not a word: `migration type:adr` finds *migration* among notes whose `type` is `adr`. The
value is read as the field's type. A query of filters alone (`type:adr status:active`) lists the
notes they admit, in path order, with `mode_used` `"facet"`. For a field the schema does not
declare, `field:value` is searched as words.

Options, a map, all optional:

| option | value | effect |
| --- | --- | --- |
| `limit` | integer, 1 to 200 (default 20) | how many hits |
| `mode` | `"auto"` (default), `"lexical"`, `"semantic"` | auto fuses words and meaning when a model is in use, else words alone; `"semantic"` fails when no model answers |
| `paths` | list of strings | only under these: a folder (`"adrs"` covers `adrs/…`) or one file |
| `tags` | list of strings | only notes that have **every** one of these tags (front matter `tags:` or `#tag`) |
| `facets` | map of field to a string or a list of strings | only notes whose frontmatter field has one of the values; every field must match. The field must be one the workspace's schema declares (`workspace.list` shows its `schema`) |

The answer:

```json
{
  "hits": [
    {"path": "adrs/0203.md", "breadcrumb": "ADR 0203 › Storage", "snippet": "…",
     "byte_start": 1204, "byte_end": 2310, "relevance": 0.94, "score": 0.031,
     "via": ["lexical", "semantic"], "generation": 7}
  ],
  "mode_used": "hybrid",
  "semantic": "ready"
}
```

- At most three hits come from one note, so a long note cannot fill the list.
- `relevance` is 0 to 1 on a fixed scale. 1 means first by words and by meaning. A hit found one
  way alone scores about 0.5 at best, unless many notes link to it or it has a tag matching the
  query, which boost it.
- `via` says which way found it.
- `semantic` says whether meaning was searched too:
  - `ready`: every note is embedded;
  - `partial`: some notes are found by words only, until they are embedded;
  - `switching`: a new model is filling, so the search was by words; `mode: "semantic"` is
    refused with code -32069;
  - `error`: no model answered, so the search was by words;
  - `off`: no model is in use.

  With `off`, `error` or `switching`, rephrase with the exact words the notes would use.

### Complex queries

Combine the options, and search more than once:

```sh
# the design decisions about storage, only among the ADRs
autodoc --call search.query '["kb", "storage decision", {"paths": ["adrs"], "limit": 10}]'
# notes tagged both todo and autodoc that mention the TUI
autodoc --call search.query '["kb", "tui", {"tags": ["todo", "autodoc"]}]'
# two folders, words only, many hits
autodoc --call search.query '["kb", "migration*", {"paths": ["docs/ops", "sql"], "mode": "lexical", "limit": 50}]'
```

For a broad question, search for its key terms in two or three phrasings and read the notes that
recur. For a precise one (a name, an error text), search the exact words with `mode: "lexical"`.

## The other verbs

Every verb but `workspace.*`, `preference.*`, `embedding.*` and `sys.*` takes the workspace's name
first.

| verb | parameters | answers |
| --- | --- | --- |
| `workspace.list` | — | `[{name, root, state, include, exclude, schema}]`; `schema` is `{path, active, fields, error, line}` |
| `workspace.set_patterns` | workspace, include list, exclude list | replace validated globs and reconcile that workspace; an empty include matches no files |
| `workspace.set_schema` | workspace, path | name the frontmatter schema file (under the root, or absolute; `""` for none); answers the `schema` status |
| `search.query` | workspace, query, options? | see above |
| `doc.read` | workspace, path | `{content, version}`: the note's text, and its version |
| `doc.outline` | workspace, path | `{version, headings: [{id, level, text, line, byte}]}`: a Markdown note's headings in order, with the version they were read at; other kinds have none |
| `doc.validate` | workspace, path, content | `{diagnostics: [{field, line, rule, message}]}`: the text's frontmatter checked against the workspace's schema; only Markdown has frontmatter |
| `index.list` | workspace, after, limit | `{docs: [{path, generation, version}], more}`: every note in path order, after `after` (`""` from the start) |
| `index.status` | workspace | `{docs, pending_jobs, cursor, diagnosed, embeddings: {model, pending, semantic, …}, …}`; `diagnosed` counts notes whose frontmatter has a problem |
| `index.changes` | workspace, since, limit | `{cursor, changes: [{path, op, generation}], more}`: what changed after cursor `since` |
| `graph.links` | workspace, path | `[{path, raw, anchor, kind, resolved}]`: the links the note makes |
| `graph.backlinks` | workspace, path | the same shape: the notes that link to it |
| `graph.neighborhood` | workspace, path, depth | `{nodes, edges: [{src, dst, kind}]}`: the notes within `depth` links |
| `graph.unresolved` | workspace | `[{src, raw, reason}]`: links that name no note |
| `sys.hello` | `{protocol, name}` | `{protocol, server, version, pid, addr}` |

Writing notes (`doc.write`, `doc.rename`, `doc.remove`), changing workspaces and choosing the
embedding model are for the user's tools, not an agent's search. Do not call them unless the user
asks you to change their notes through AutoDoc.

## Errors

| code | meaning | what to do |
| --- | --- | --- |
| -32060 | no such workspace | call `workspace.list` and use a listed name |
| -32061 | no such note | the path is wrong or not indexed; `index.list` shows the paths |
| -32602 | invalid parameters | the message says which; check the table above |
| -32065 | not supported here | `mode: "semantic"` with no model in use; search without it |
| -32067 | the query could not be embedded | retry, or search with `mode: "lexical"` |
| -32069 | a new model is filling | search with `mode: "auto"` or `"lexical"` |
| -32020 | protocol mismatch | the daemon is another version: `autodoc --version`, then restart it |
