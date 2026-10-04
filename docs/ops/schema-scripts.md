# The store's schema: SQL scripts

AutoDoc keeps its workspaces and every workspace's index in one store, a
SQLite file. The store's schema is a set of SQL scripts, one directory per
engine, compiled into the binary.

```
sql/deployments/
└── sqlite/
    ├── 000001_update_initialize_tables.sql     the baseline: no revert
    ├── 000002_update_<slug>.sql                a change …
    └── 000002_revert_<slug>.sql                … and its undo
```

- Numbers are dense and never reused. An `update` changes the schema; its
  `revert` undoes exactly that change. The pairs start at 000002.
- **A released script never changes.** The store records the digest each
  script was applied with, and a test holds the digest of every script a
  tagged release shipped, so an edit cannot ship. A fix is the next script.
- A second engine is a second directory with the same names. A script with no
  work on one engine is a comment saying why.
- The scripts hold the schema (the DDL) and nothing else. The program reads
  and writes rows through golib `dao`, one schema declaration for each table,
  never through SQL of its own.

## The ledger

```sql
schema_version (script TEXT PRIMARY KEY, sha256 TEXT, applied_at INTEGER)
```

The store's schema is the set of rows: each applied update script, by file
name, with its digest. A revert deletes its script's row. golib `dao/deploy`
creates the ledger and writes it, in the transaction that applies the
scripts; no script names it.

## The baseline: 000001

`000001_update_initialize_tables.sql` creates the whole store. Every statement
is `IF NOT EXISTS`. It has no revert: undoing it would drop the store, and
with it the workspaces, which nothing else records.

## The workspace key

Everything a workspace owns carries `workspace_id`, and every key and every
unique constraint on such a table starts with it:

- deleting a workspace removes all of it (`ON DELETE CASCADE`);
- a reference between two tables of one workspace is
  `(workspace_id, id)`, so a row can never point into another workspace;
- a later engine can partition these tables by `workspace_id`, which
  requires the partition key in every unique constraint.

A new workspace-owned table follows the same rule.

## The tables

| table | what a row is | key |
| --- | --- | --- |
| `schema_version` | an applied update script; golib `dao/deploy` creates and keeps it | `script` |
| `workspace` | a root AutoDoc indexes; `name` is what a person types and can be renamed | `id` |
| `embedding_provider` | an embedding provider: kind, base URL, model, and its API key sealed with the keyslot (000002); its context window in tokens (000003) | `id`, `name` |
| `embedding_usage` | a provider's requests, texts, tokens and failures by day (000002) | `(provider_id, day)` |
| `embedding_log` | a provider's recent calls, the last 200 kept (000002) | `id` |
| `preference` | a client's preference, by name: the store's, not a workspace's (000002) | `name` |
| `workspace_pattern` | an include or exclude pattern of a workspace, in order | `(workspace_id, kind, ord)` |
| `workspace_connection` | a workspace's source or destination database: engine, sealed DSN, schema (ADR 0214) | `(workspace_id, role)` |
| `document` | a file under the root | `id`, `(workspace_id, path)` |
| `chunk` | a section of a document, live from `gen_from` until `gen_to` | `id`, `(workspace_id, id)` |
| `chunk_fts` | SQLite's full-text index over the chunks (FTS5, external content) | the chunk's `id` |
| `doc_tag` | a tag from a document's frontmatter | `(workspace_id, tag, doc_id)` |
| `doc_alias` | an alias from a document's frontmatter | `(workspace_id, alias, doc_id)` |
| `doc_name` | a name a link can resolve through | `(workspace_id, name_key, doc_id)` |
| `doc_facet` | a typed value of a document's valid frontmatter field under its workspace's schema, defaults included (000007) | `(workspace_id, field, value, doc_id)` |
| `doc_diagnostic` | a problem with a document's frontmatter: field, line, rule, message, in source order (000007) | `(workspace_id, doc_id, ord)` |
| `link` | a link from a document, and the document it resolved to | `id`, `(workspace_id, id)` |
| `model` | an embedding model a workspace used; one is active | `(workspace_id, fp)` |
| `embedding` | a text's vector under a model | `(workspace_id, text_hash, model_fp)` |
| `index_job` | a file waiting to be indexed | `(workspace_id, path)` |
| `change` | a change-log entry a client follows by cursor | `(workspace_id, seq)` |
| `event` | a configuration or lifecycle change, daemon-wide, with the client that made it; the last 1000 kept (000009) | `seq` |

Each script says, in its comments, what every table and column is for.

## Who applies the scripts

The daemon, at every start, through golib `dao/deploy`: pending scripts run in
number order, the whole set in one transaction. A failure leaves the store as
it was and the daemon does not start. A store that records a script this
binary does not have is refused, so an older binary never writes a newer
store. A set with a gap or two scripts under one number is refused before
anything runs.

## The rule every update script keeps

- it only adds: new tables, new columns with defaults, new indexes;
- it never renames, drops or changes the type of something in the same
  release that stops using the old form;
- it is idempotent where the engine allows (`IF NOT EXISTS`); the ledger makes
  every script run once regardless.
