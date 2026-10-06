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
| `workspace` | a root AutoDoc indexes; `name` is what a person types and can be renamed; its retrieval settings `abstract_chunk` and `demote_superseded` (000014) | `id` |
| `embedding_provider` | an embedding provider: kind, base URL, model, and its API key sealed with the keyslot (000002); its context window in tokens (000003) | `id`, `name` |
| `embedding_usage` | a provider's requests, texts, tokens and failures by day (000002) | `(provider_id, day)` |
| `embedding_log` | a provider's recent calls, the last 200 kept (000002) | `id` |
| `ranker` | a ranker model: kind (tei, rerank-api), base URL, model, and its API key sealed with the keyslot (000013) | `id`, `name` |
| `ranker_usage` | a ranker's requests, texts, tokens and failures by day (000013) | `(ranker_id, day)` |
| `ranker_log` | a ranker's recent calls, the last 200 kept (000013) | `id` |
| `preference` | a client's preference, by name: the store's, not a workspace's (000002) | `name` |
| `workspace_pattern` | an include or exclude pattern of a workspace, in order | `(workspace_id, kind, ord)` |
| `workspace_connection` | a workspace's source or destination database: engine, sealed DSN, schema (ADR 0214) | `(workspace_id, role)` |
| `document` | a file under the root | `id`, `(workspace_id, path)` |
| `chunk` | a section of a document, live from `gen_from` until `gen_to`; its embed text when a registered chunker gives one (000012); its kind, `section` or `abstract` (000014) | `id`, `(workspace_id, id)` |
| `chunk_fts` | SQLite's full-text index over the chunks (FTS5, external content) | the chunk's `id` |
| `doc_tag` | a tag from a document's frontmatter | `(workspace_id, tag, doc_id)` |
| `doc_alias` | an alias from a document's frontmatter | `(workspace_id, alias, doc_id)` |
| `doc_name` | a name a link can resolve through | `(workspace_id, name_key, doc_id)` |
| `doc_facet` | a typed value of a document's valid frontmatter field under its workspace's schema, defaults included (000007) | `(workspace_id, field, value, doc_id)` |
| `doc_diagnostic` | a problem with a document's frontmatter: field, line, rule, message, in source order (000007) | `(workspace_id, doc_id, ord)` |
| `link` | a link from a document, and the document it resolved to | `id`, `(workspace_id, id)` |
| `link_key` | a frontmatter relation link's names, tried in order when it resolves (000014) | `(workspace_id, link_id, ord)` |
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

## A destination's scripts

A workspace whose destination is Postgres keeps its index in that database, not in the local store
(ADR 0214). Its tables are `core/pgstore/postgres/`, compiled into `core/pgstore` and applied by
golib `dao/deploy` through `pgstore.Migrate`, with a ledger of its own, `autodoc_schema`
(`pgstore.Ledger`). The same rules hold as for the store: a released script never changes, and the
set must be dense and paired.

- **The tenant is the workspace uid.** Several machines' workspaces share one destination, so
  every key starts with `tenant` — the local store's `workspace.uid` for an AutoDoc workspace,
  never a local id, and any tenant string for another importer.
- **Lexical search** is a generated, weighted `tsvector` on `rag_chunk` with a GIN index: title
  (class A), breadcrumb (B) and tags (C) above the body (D), stemmed by the `rag_english`
  configuration the baseline creates (Snowball English, no stop words: every query word stays
  required).
- **Vectors** are pgvector's `vector`, keyed `(tenant, model, text_hash)`. Semantic search scans
  exactly; an approximate-nearest-neighbour index is a measured follow-up, not in the baseline.
- **pgvector must be installed in a schema the connection searches** (public, usually). The
  baseline creates the extension only when the database has none.
- **The cells** run with `AUTODOC_TEST_PGURL` set, each in a scratch database it creates and
  drops; in CI they run on a service container and are required (`AUTODOC_TEST_PG_REQUIRED=1`),
  never silently skipped.

### A destination that holds the old scripts

Before pgstore, a destination's tables were `sql/destination/postgres/` (000001 and 000002),
applied by no production code — only by a test. Those scripts are deleted, as a narrow exception
to the released-script rule, and pgstore's baseline starts the history again under the same
`autodoc_schema` ledger.

**Nothing is dropped automatically, ever.** A database whose ledger records the old
`000001_update_initialize_index.sql` is refused by `Migrate` with `dao/deploy`'s `ErrDowngrade`
before any pending script runs: the refusal happens inside the transaction, so the database is
left exactly as it was, data and ledger both.

**Recovery is an operator's deliberate, by-hand choice.** An operator who owns such a database and
wants it as an AutoDoc destination should prefer pointing the destination at a **fresh schema**.
The old table names (`workspace`, `document`, `chunk`, `doc_tag`, `doc_alias`, `doc_name`,
`doc_facet`, `doc_diagnostic`, `link`, `model`, `embedding`) are unprefixed and may belong to
another product in a shared schema, so never drop them unread: first verify the `autodoc_schema`
ledger in that schema names them, and that their owner agrees. Only then drop the old destination
tables and the `autodoc_schema` ledger rows in that schema — the `vector` extension stays — and
let `pgstore.Migrate` apply the new baseline. Nothing is lost that cannot be rebuilt: a destination
is derived from the workspace's files, and re-indexing into a new destination reproduces it.
