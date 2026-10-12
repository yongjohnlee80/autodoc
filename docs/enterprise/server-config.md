# The server configuration

`autodoc --server server.toml` reads one TOML file. This page lists every field, its default, and
what is refused. Back to [Enterprise search](README.md).

## Validation

The whole file is checked before the server opens anything, and every view it names is loaded
and checked too ([Writing a `.view` source](views.md)).

- A file that is wrong is refused with **exit code 2**. Every refused field is named, all at once:

  ```text
  autodoc: server.toml: postgres.dsn_env: the environment variable AUTODOC_SERVER_DSN is not set; source[0].interval: 10ms: want at least 1s
  ```

- **An unknown field is refused**, so a misspelt optional field can't silently go missing, and a
  secret written in plain text under some other key is refused too.
- A file that can't be read is exit 1.
- `autodoc --server --check server.toml` checks the file and the views the same way, then runs each
  view against your database, read-only, to confirm its order, asks the embedding provider to embed
  one probe text, and exits 0 saying what it checked. It also prints the warnings below.

**No secret is ever written in the file.** The DSN and the embedding key are read from environment
variables the file names. An HTTP client's token is kept as its SHA-256, or read from the
environment. A refusal names the variable, never its value, and errors from the database driver are
redacted of the DSN and its password.

## An example

```toml
[postgres]
dsn_env = "AUTODOC_SERVER_DSN"
schema  = "autodoc_search"

[embedding]
provider = "openai"
base_url = "https://embeddings.example.internal/v1"
model    = "text-embedding-3-small"
key_env  = "AUTODOC_EMBED_KEY"
slots    = 4

[http]
listen = "127.0.0.1:8730"

[[http.client]]
name         = "lm-web"
token_sha256 = "<the 64 hex digits of the token's SHA-256>"
[http.client.grants.lm-catalog]
label_id = ["*"]

[[http.client]]
name      = "lm-reports"
token_env = "LM_REPORTS_TOKEN"
[http.client.grants.lm-catalog]
label_id = ["label-17", "label-42"]

[[source]]
tenant   = "lm-catalog"
view     = "views/track.view"
refresh  = "24h"
interval = "1m"
lag      = "1m"
access   = ["label_id"]
```

Durations are strings Go's `time.ParseDuration` reads: `"90s"`, `"5m"`, `"24h"`.

## `[postgres]`: where the index lives

The index is kept in a schema of its own in your business database. The server creates the schema
when it is missing, then applies its migrations there. Your tables are never written.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `dsn_env` | (required) | The name of the environment variable holding the PostgreSQL connection string (a URL or `key=value` form). | missing; not a variable name; the variable is not set or is empty |
| `dsn_sealed` | | A DSN sealed with AutoDoc's keyslot. | always, for now: the keyslot keeps its key beside a local store, which the server doesn't have. Use `dsn_env`. |
| `schema` | `autodoc_search` | The schema the index lives in. | not a plain identifier: lowercase letters, digits and `_`, not starting with a digit or `pg_`, at most 63 bytes |

The server opens three connection pools on the DSN:

- the index's, where every connection's `search_path` is `schema, public` (pgvector is usually
  installed in `public`);
- the one your views run on, with the role's own `search_path`, so a view runs as it does in `psql`;
- one pinned connection per source, which holds that tenant's writer lock.

## `[embedding]`: the enterprise's embedding model (required)

Every tenant's chunks are embedded centrally, with this model, by the server's one embedding
worker.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `provider` | (required) | `"openai"` (any OpenAI-compatible endpoint) or `"ollama"`. | missing, or another value |
| `base_url` | `https://api.openai.com/v1` for openai, `http://127.0.0.1:11434` for ollama | The provider's endpoint. For openai, the API's root or its `/v1`: either way the server asks `<root>/v1/embeddings`. | not an `http` or `https` URL with a host |
| `model` | (required) | The model's name. | missing or blank; `--check`: an Ollama server without it |
| `key_env` | none | The name of the environment variable holding the provider's API key, sent as a bearer token. | given, and the variable is not set or is empty; `--check`: the provider refuses the key |
| `slots` | `4` | How many embedding requests may be in flight at once, across every tenant. | below 1 or above 64 |

### How the server embeds

- **Once per distinct text.** The worker embeds the chunk texts that have no vector under the
  model, each distinct text once per tenant, in batches of 64 texts, at most `slots` requests at a
  time whichever tenants they are for. Vectors are normalized and stored in the index's schema
  (pgvector).
- **A document is ready** for search by meaning once every one of its chunks has a vector. Until
  then it is found by words, and ingestion never waits for the worker: an ingester's commit only
  wakes it. It also looks every 30 seconds.
- **Failures are retried.** A provider that is down, rate-limits or refuses the key is asked again
  after a wait that doubles from a second to a minute; the log says so each time. That includes a
  provider that does not answer when the server starts: the server runs, ingests, and answers by
  words until it does.
- **A text the provider rejects** (too long, say) is set aside for an hour and logged, and the rest
  of its batch is embedded. Its document is found by words meanwhile.
- **One writer per tenant.** The worker writes a tenant only while this server's ingester holds the
  tenant's lock, so a standby server never touches it.
- **Changing the model is a switch, with no gap in search.** The model's identity is its provider,
  name, digest (Ollama reports one) and dimensions, so a new `model`, or a model that now answers
  with another size, is a new model. At the next start the worker records it as the tenant's
  target and fills it, while the old model stays recorded as active and searches answer by words
  (`semantic: "switching"`). Once every text is embedded, every document is marked by the new model,
  it becomes active, and the old model's vectors are deleted. A restart in the middle carries on;
  configuring the old model again before the switch finishes cancels it.
- **The key is a secret.** It is read from `key_env` when the provider is set up, and taken out of
  every error the provider returns before it reaches the log.

`--check` sets the provider up as the server does, which embeds one probe text, and reports the
model and its dimensions. A key the provider refuses (`401`/`403`) is refused, naming
`embedding.key_env`; an Ollama server without the model is refused, naming `embedding.model`. A
provider that can't be reached, or fails otherwise, is a warning: the server would start without
it and keep trying.

## `[http]` and `[[http.client]]`: the search API (optional)

Without an `[http]` table the server answers no HTTP request. A Go web application can still search
through the library ([web-integration](web-integration.md)). With it, the server serves
`POST /v1/search` once its embedding worker has started.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `listen` | `127.0.0.1:8730` | The address the API listens on. | not `host:port`; not loopback and no TLS |
| `tls_cert`, `tls_key` | none | The certificate and key files, relative to this file. Required to listen beyond loopback; or keep it on loopback behind your own proxy. | only one given; a file can't be read |

Each `[[http.client]]` is one caller of the API, with its token and what it may search. There must
be at least one.

| Field | Meaning | Refused when |
|---|---|---|
| `name` | Names the client in the log. | missing; another client has it |
| `token_sha256` | The lowercase hex SHA-256 of the client's bearer token. The token itself is never in the file. | not 64 hex digits (a token pasted here by mistake is refused without being echoed) |
| `token_env` | Instead: the name of the environment variable holding the token. | the variable is not set or is empty |
| `grants.<tenant>.<field>` | The values of each access field the client may ask for, in that tenant. `["*"]` allows any value, which a request must still name. | see below |

Exactly one of `token_sha256` and `token_env` is given. The grants are refused, naming the client
and the field, when:

- the client has no grants at all;
- a granted tenant is not a configured `[[source]]`;
- an access field of a granted tenant has no grant (every one needs one);
- a grant names a field that isn't one of the tenant's access fields;
- a grant is empty, has an empty or repeated value, or mixes `"*"` with values (`"*"` stands
  alone).

**Use high-entropy tokens:** at least 32 random bytes, such as `openssl rand -hex 32`. Then put its
hash in the file:

```sh
token=$(openssl rand -hex 32)
printf %s "$token" | sha256sum | cut -d' ' -f1    # → token_sha256
```

A hash protects where the token is stored, not a token short enough to guess. `--check` warns when
a `token_env` value is shorter than 32 bytes.

## `[[source]]`: what is indexed

Each source is one `.view` file, indexed into a tenant of its own. There must be at least one.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `tenant` | (required) | The tenant the source is indexed into. It keys every index row, names the source in the log, and keys its writer lock. | missing; not letters, digits, `.`, `_` and `-` starting with a letter or digit, at most 64; another source has it |
| `view` | (required) | The `.view` file, relative to this file. | missing; the view is refused ([its rules](views.md)) |
| `interval` | `1m` | How often the rows changed since the cursor are read. | under `1s` |
| `refresh` | `24h` | How often every row is read again: content changes the rows' `updated_at` missed are written, and the documents of rows gone are deleted. | shorter than `interval` |
| `lag` | `1m` | How far behind the database's clock a pass reads. A transaction open longer than this could commit a row behind the cursor, which only the next refresh would read. `"0s"` is none. | negative |
| `access` | (required) | The row fields every search of the tenant must be scoped by: say, `["label_id"]`. Each row must carry every one. | missing or empty; a name empty, repeated or containing `=` |

When the server starts it records each tenant's access fields and view name as the tenant's
policy, which search reads (from M3): a tenant can never be searched unscoped.

## Exit codes

| Code | When |
|---|---|
| 0 | stopped by SIGINT or SIGTERM, with every lock released; or `--check` found nothing wrong |
| 1 | the file can't be read; the database can't be reached; a tenant's writer lock was lost (restart it: it waits for the lock and takes it again) |
| 2 | the configuration or a view is refused, the field named; `--check` refused a view's order, or the embedding provider refused the key or has no such model |
