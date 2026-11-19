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

[ranker]
kind     = "tei"
base_url = "http://127.0.0.1:8081"

[http]
listen = "127.0.0.1:8730"

[[http.client]]
name         = "web-app"
token_sha256 = "<the 64 hex digits of the token's SHA-256>"
[http.client.grants.music]
label_id = ["*"]

[[http.client]]
name      = "reports-app"
token_env = "REPORTS_APP_TOKEN"
[http.client.grants.music]
label_id = ["label-17", "label-42"]

[[source]]
tenant   = "music"
view     = "views/track.view"
refresh  = "24h"
interval = "1m"
lag      = "1m"
access   = ["label_id"]
```

Durations are strings Go's `time.ParseDuration` reads: `"90s"`, `"5m"`, `"24h"`.

## `[postgres]`: where the index lives

The index is kept in a schema of its own, in your business database or in a database of its own
(`index_dsn_env`). The server creates the schema when it is missing, then applies its migrations
there. Your tables are never written.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `dsn_env` | (required) | The name of the environment variable holding the PostgreSQL connection string (a URL or `key=value` form). | missing; not a variable name; the variable is not set or is empty |
| `index_dsn_env` | | The name of the environment variable holding the index's own connection string. Set, the index's schema, its writes, the writer leases and the vectors go there, and the views are read through `dsn_env` alone ([below](#the-index-in-a-database-of-its-own)). Unset, both are the one database. | not a variable name; the variable is not set or is empty; the same variable as `dsn_env` |
| `dsn_sealed` | | A DSN sealed with AutoDoc's keyslot. | always, for now: the keyslot keeps its key beside a local store, which the server doesn't have. Use `dsn_env`. |
| `schema` | `autodoc_search` | The schema the index lives in. | not a plain identifier: lowercase letters, digits and `_`, not starting with a digit or `pg_`, at most 63 bytes |
| `ann_threshold` | `50000` | How many embeddings of its model a tenant has before its semantic searches become approximate, through the model's HNSW index. Below it they're exact. | below 1 |
| `ann_max_scan_tuples` | `1000` | The most index tuples one approximate search scans past its first candidates (pgvector's `hnsw.max_scan_tuples`). It bounds what a filtered search spends before it answers exactly: a selective filter mostly comes back short, and at 1,000,000 embeddings under a 1% filter a budget of 40,000 made the search 7.6× slower than exact, against 1.4× at 1,000. Unfiltered searches are unaffected. | outside 1 to 2147483647 |
| `ann_scan_mem_multiplier` | `2` | The memory one approximate search's scan may use, as a multiple of `work_mem` (pgvector's `hnsw.scan_mem_multiplier`). | outside 1 to 1000 |
| `ann_exact_scope_docs` | `1000` | A filtered search whose filter and access admit at most this many of the tenant's ready documents is answered exactly, without trying the index: the index's candidates for so few come back short, and the exact search then runs after the scan was already paid for. At 56,000 documents and 168,000 embeddings, a search scoped to a small label took 49 ms through the index and 12 ms answered exactly up front. `0` always tries the index first. | below 0 or above 100000 |
| `ann_ef_construction` | `200` | How wide each model's HNSW index is built (pgvector's `ef_construction`). It decides recall more than any search setting: at pgvector's default of 64, a tenant sharing the index found 0.938 to 0.990 of its exact top ten across 20 builds, and 8 of those builds were below 0.95; at 200, 0.994 to 1.000. A wider build takes longer. | outside 4 to 1000, or below twice `ann_m` |
| `ann_m` | `16` | The links each node of the index keeps (pgvector's `m`). 32 did no better than 16 at an ef_construction of 200. | outside 2 to 100 |
| `ann_defer_backlog` | `2000` | A model's wanted index waits while a tenant whose active model it is has this many documents or more not yet ready (its bulk load under way), so the load's inserts don't pay the index's cost. 0 never waits. | below 0 |
| `ann_defer_max` | `"24h"` | The longest a model's wanted index waits for a bulk load: past it, the index is built whatever the backlog. | not a duration, or 0 or less |
| `ann_maintenance_work_mem` | `"512MB"` | The memory the session building an index may use (PostgreSQL's `maintenance_work_mem`, for that session only). A parallel build keeps it in the database's shared memory (`/dev/shm`): give a database in a container `--shm-size` at least this, or the build falls back to running without parallel workers, and the server logs a warning saying so. | not a size in kB (1024 or more), MB or GB |

The server opens two connection pools:

- the index's, on `index_dsn_env`'s DSN when it is set, else on `dsn_env`'s. Every connection's
  `search_path` is `schema, public` (pgvector is usually installed in `public`), and each tenant's
  writer lease is a row of `rag_writer_lease`. Its connections end a transaction left idle for a
  minute, so a hung writer never keeps a standby from its tenant;
- the one your views run on, on `dsn_env`'s DSN, with the role's own `search_path`, so a view runs
  as it does in `psql`.

### The index in a database of its own

When the business database may only be read (a production database, a replica), give the index a
database of its own with `index_dsn_env`:

```toml
[postgres]
dsn_env       = "AUTODOC_SOURCE_DSN"   # the views: read only
index_dsn_env = "AUTODOC_INDEX_DSN"    # the index: its schema, leases and vectors
schema        = "autodoc_search"
```

What each connection's role needs:

- **the source** (`dsn_env`): `SELECT` on the tables the views read and `USAGE` on their schema,
  nothing else. A read-only user is enough, and pgvector isn't needed there;
- **the index** (`index_dsn_env`): pgvector installed in its database, once, by a superuser or the
  database's owner (`CREATE EXTENSION vector`), and the schema: one the role owns, or `CREATE` on
  the database to let the server create it. [views](views.md#the-grants) has the statements; run the
  index's half in the index's database.

`--check` checks each, reading only:

- the index's database: it answers, pgvector is installed there or the role can install it, and the
  role can create the index's tables in the schema, or the schema. It says
  `index: a database of its own: vector 0.8.1, schema autodoc_search`, or refuses naming
  `postgres.index_dsn_env` and saying what's missing;
- the source: each view's order, as without it.

Both may be databases on one PostgreSQL server, or on one managed instance (AlloyDB, Cloud SQL):
the two connections are independent. A pass reads a page of the source, then writes it to the
index; nothing spans the two databases in one transaction. The database check in
[`/v1/health`](operations.md#health-get-v1health) is the index's: a source that stops answering
shows in `/v1/status` as its tenants' `last_pass.error`, and their `cursor.lag_seconds` left out.
The lags are measured on the source's clock, which set each pass's bound.

**Approximate semantic search.** The server builds one HNSW index per embedding model, in the
background, one index at a time across every server on the database.
- Until a model's index is ready, and while a tenant has fewer than `ann_threshold` embeddings of
  it, semantic search is exact.
- Above the threshold it is approximate. The nearest candidates come through the index, are then
  ordered exactly, and ties are ordered within the returned set.
- A filtered search whose scope admits at most `ann_exact_scope_docs` documents is answered
  exactly up front, the index untried.
- A search whose candidates come back short (a selective filter, or a scan that hit
  `ann_max_scan_tuples`) is answered exactly instead, and counted as an underfill.
- A model of more than 4,000 dimensions has no index and stays exact.
- The status reports each model's mode: `exact`, `ann`, `ann-building` or `ann-failed` (with the
  reason).
- An index built with other parameters than `ann_m` and `ann_ef_construction` (an upgrade's, or a
  change of them) is rebuilt while it serves. The new index is built concurrently under a second
  name; once it is valid, the old one is dropped concurrently and the new one renamed. Searches
  stay approximate throughout, and the status says `ann` with the reason. A rebuild that fails
  leaves the old index serving, and is retried with a growing wait.

## `[embedding]`: the enterprise's embedding model (required)

Every tenant's chunks are embedded centrally, with this model, by the server's one embedding
worker.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `provider` | (required) | `"openai"` (any OpenAI-compatible endpoint), `"ollama"`, or `"vertex"` (Vertex AI's gemini-embedding-2, [below](#vertex-ai-on-google-cloud)). | missing, or another value |
| `base_url` | `https://api.openai.com/v1` for openai, `http://127.0.0.1:11434` for ollama, the location's Google host for vertex | The provider's endpoint. For openai, the API's root or its `/v1`: either way the server asks `<root>/v1/embeddings`. | not an `http` or `https` URL with a host |
| `model` | (required) | The model's name. | missing or blank; `--check`: an Ollama server without it |
| `key_env` | none | The name of the environment variable holding the provider's API key, sent as a bearer token. Not for vertex, which authenticates with Application Default Credentials. | given, and the variable is not set or is empty; given for vertex; `--check`: the provider refuses the key |
| `slots` | `4` | How many embedding requests may be in flight at once, across every tenant. | below 1 or above 64 |
| `batch` | `64`; `1` for vertex | How many texts one embedding request carries, at most. Vertex embeds one text a request, so `slots` sets its throughput. | below 1 or above 256; other than 1 for vertex |
| `call_timeout` | `"2m"` | How long one embedding request may take before it is given up. | below `1s` or above `30m` |
| `project` | (required for vertex) | vertex only: the Google Cloud project the calls are made in and billed to. | missing for vertex; given for another provider; `--check`: Google refuses the credentials, or the API isn't enabled in it |
| `location` | (required for vertex) | vertex only: `"global"`, `"us"` or `"eu"`, where gemini-embedding-2 is served. | missing or another value for vertex; given for another provider |
| `dimensions` | `3072` | vertex only: the vectors' size, 128 to 3072. Google normalizes a vector it reduces; 1536 halves the index for a small loss. A change is a model switch. | outside 128 to 3072; given for another provider |

### How the server embeds

- **Once per distinct text.** The worker embeds the chunk texts that have no vector under the
  model, each distinct text once per tenant, in batches of `batch` texts, at most `slots` requests
  at a time whichever tenants they are for. Vectors are normalized and stored in the index's schema
  (pgvector).
- **A document is ready** for search by meaning once every one of its chunks has a vector. Until
  then it is found by words, and ingestion never waits for the worker: an ingester's commit only
  wakes it. It also looks every 30 seconds.
- **Failures are retried.** A provider that is down, rate-limits or refuses the key is asked again
  after a wait that doubles from a second to a minute; the log says so each time. That includes a
  provider that does not answer when the server starts: the server runs, ingests, and answers by
  words until it does.
- **A slow provider is asked for less at once.** A request that runs out of `call_timeout` halves
  the batch and the requests in flight, down to one text and one request, and the texts are asked
  for again at once at the smaller size: a provider on CPU, or loading its model, embeds
  something rather than nothing. The log says so once, with what to change: `batch`,
  `call_timeout`, or the provider's cores. Once four requests in a row answer in under half their
  time, the batch doubles, then the requests in flight rise by one, back to `batch` and `slots`
  (logged once). Only a request of one text that still runs out of time is the provider failing.
  The status shows the pace now ([operations](operations.md)).
- **A text the provider rejects** (too long, say) is set aside for an hour and logged, and the rest
  of its batch is embedded. Its document is found by words meanwhile.
- **One writer per tenant.** The worker writes a tenant only while this server's ingester holds the
  tenant's writer lease, so a standby server never touches it.
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

## `[ranker]`: a reranker after recall (optional)

With a `[ranker]`, every search gets a second stage: a cross-encoder reorders recall's best
candidates, the window. Without one, searches answer in recall order, as they always have.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `kind` | (required) | `"tei"` (a Hugging Face Text Embeddings Inference server serving a reranker), `"rerank-api"` (a Cohere-style `POST <base_url>/rerank`: Cohere, Jina, Voyage, vLLM, Infinity, llama.cpp's server), or `"vertex"` (the Vertex AI Ranking API, [below](#vertex-ai-on-google-cloud)). There is no Ollama kind: Ollama has no rerank API. | missing, or another value |
| `base_url` | (required; Google's host for vertex) | The ranker's endpoint. For rerank-api, with its version (`https://api.cohere.com/v2`). | missing for tei and rerank-api, or not an `http` or `https` URL with a host |
| `model` | none | rerank-api's: the model the endpoint serves; vertex's: the ranking model, pinned (`semantic-ranker-default-005`, or `-fast-005`). A TEI server serves one and names it. | missing for rerank-api or vertex; an `@` alias for vertex; given for tei |
| `key_env` | none | The name of the environment variable holding the ranker's API key, sent as a bearer token. Not for vertex. | given, and the variable is not set or is empty; given for vertex; `--check`: the ranker refuses the key |
| `project` | (required for vertex) | vertex only: the Google Cloud project the calls are made in and billed to. | missing for vertex; given for another kind |
| `window` | `40` | How many of recall's best candidates a search reranks. | below 10 or above 100 |
| `timeout` | `"750ms"` | How long a search's ranker call may take. Past it, the search answers in recall order. | below `100ms` or above `10s` |
| `slots` | `4` | How many ranker calls may be in flight across every search. | below 1 or above 64 |

### How the server reranks

- **The texts are the search's own.** Each candidate's text is its chunk's breadcrumb and body, as
  the search read it, in its own snapshot and scope: nothing is read again, and a ranker is never
  sent a text the search's scope doesn't admit. Two candidates with the same text cost one.
- **The window, then recall.** The search asks recall for the larger of the window and its limit,
  the ranker reorders the window, and any hits past it follow in recall order, unscored. A
  request may set its own `window`, or skip the ranker with `"rerank": false`
  ([web-integration](web-integration.md#the-request)).
- **Across tenants, once.** A search across tenants reranks the merged top window in one call, each
  hit's text from its own tenant. Its window is at least 4 a tenant by default, at most 100.
- **A ranker that fails never fails a search.** Unreachable, refused, rate limited, a bad answer,
  past `timeout`, or no slot free within it: the search answers in recall order, and its `rank`
  says why. A ranker that is down, or refused, is asked nothing until a probe answers again: the
  server probes it at start, and after a failure, waiting from a second to a minute between
  probes.
- **What the ranker receives.** Every reranked search sends it its query and its window's texts. A
  TEI server on the server's own machine keeps them there; a hosted rerank API receives them under
  its provider's terms. The log and the status carry codes and counts, never a query, a text or an
  answer; the key is taken out of every error, as the embedding provider's is.

`--check` makes the ranker as the server does and ranks a two-text probe. A key the ranker refuses
is refused, naming `ranker.key_env`; a model it doesn't serve, or a TEI server serving another kind
of model (an embedding model, say), is refused, naming `ranker.model`. A ranker that can't be
reached, or fails otherwise, is a warning: the server would start without it and keep probing.

## Vertex AI on Google Cloud

On a GCP VM, `provider = "vertex"` embeds with gemini-embedding-2 and `kind = "vertex"` reranks
with the Ranking API, inside the project:

```toml
[embedding]
provider   = "vertex"
project    = "my-project"
location   = "us"                # global, us or eu
model      = "gemini-embedding-2"
dimensions = 1536
slots      = 16

[ranker]
kind    = "vertex"
project = "my-project"
model   = "semantic-ranker-default-005"
```

- **No key in the configuration.** The server asks for short-lived OAuth tokens through
  Application Default Credentials: on the VM, its attached service account; elsewhere, the file
  `GOOGLE_APPLICATION_CREDENTIALS` names, or `gcloud auth application-default login`. A token
  never reaches the log.
- **Each side in Google's prompt format.** A document is sent as `title: none | text: <chunk>`,
  a query as `task: search result | query: <query>`, as Google's documentation gives them for
  retrieval. The format is part of the model's identity (`vertex|gemini-embedding-2|doc-v1|1536`),
  so a later format is a model switch, as another `dimensions` is.
- **One text a request.** The model's method takes one text, so `batch` is 1 and `slots` is the
  throughput: about 70,000 chunks take some ten minutes at 16 slots.
- **A down ranker is probed once a minute**, never faster: each probe is a billed count.

The service account, its roles, the APIs to enable and what it costs are in
[operations](operations.md#on-google-cloud-vertex-ai).

## `[http]` and `[[http.client]]`: the search API (optional)

Without an `[http]` table the server answers no HTTP request. A Go web application can still search
through the library ([web-integration](web-integration.md)). With it, the server serves
`POST /v1/search` once its embedding worker has started.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `listen` | `127.0.0.1:8730` | The address the API listens on. | not `host:port`; not loopback with neither TLS nor `proxy` |
| `max_tenant_searches` | the store pool's size less two | How many tenants' searches the requests naming several tenants run at once, across every request; beyond it a tenant waits its turn under its request's deadline. | outside 1 to 1000 |
| `tls_cert`, `tls_key` | none | The certificate and key files, relative to this file: to listen beyond loopback over HTTPS. | only one given; a file can't be read |
| `proxy` | `false` | `true`: a proxy in front terminates TLS, so the API may listen beyond loopback over plain HTTP. `--check` warns that requests cross the network between the two in clear text: keep it private ([operations](operations.md#behind-a-proxy)). | given with `tls_cert` |

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

## `[[http.operator]]`: who reads the status

An operator reads `GET /v1/status` for its tenants ([operations](operations.md#status-get-v1status)), and can't search. A search client can't read status: its grants are per access value, and status is tenant-wide.

| Field | Meaning | Refused when |
|---|---|---|
| `name` | Names the operator in the log. | missing; a client or another operator has it |
| `token_sha256`, `token_env` | As a client's: exactly one. | as a client's; **the same token as a client's** (one token, one role) |
| `tenants` | The tenants whose status it reads, or `["*"]` for every one. | missing or empty; not a configured tenant; a tenant twice; `"*"` with others |

## `[log]`: the level and the metrics lines

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `level` | `""` | The least severe record the server logs: `debug`, `info`, `notice`, `warning`, `error` or `critical`, in any case. `""` logs every one. `autodoc --server --log-level LEVEL server.toml` sets it for one run, over the file's. | any other value |
| `metrics_interval` | `1m` | How often the server logs its metrics lines ([operations](operations.md#metrics-in-the-log)). `"0s"` logs none. | under `10s`, other than `0s` |

The metrics lines and each successful request's line are `Info`: a `level` of `notice` or above
leaves them out of the log (a failed request's line is a `Warning`, and stays); `GET /v1/metrics`,
when enabled, still serves the totals.

## `[metrics]`: the Prometheus endpoint (optional)

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `enabled` | `false` | Serves `GET /v1/metrics` ([operations](operations.md#metrics-for-a-scraper-get-v1metrics)), behind the operators' tokens. | `true` with no `[http]` table, or with no `[[http.operator]]` |

## `[[source]]`: what is indexed

Each source is one `.view` file, indexed into a tenant of its own. There must be at least one.

| Field | Default | Meaning | Refused when |
|---|---|---|---|
| `tenant` | (required) | The tenant the source is indexed into. It keys every index row, names the source in the log, and keys its writer lease. | missing; not letters, digits, `.`, `_` and `-` starting with a letter or digit, at most 64; another source has it |
| `view` | (required) | The `.view` file, relative to this file. | missing; the view is refused ([its rules](views.md)) |
| `deletes` | (none) | A deletes view, relative to this file: the tombstones of the source's rows deleted outright, which every pass applies ([deletes](views.md#deletes)). Without one, such a row's document leaves the index at the next complete refresh. | the view is refused ([its rules](views.md)) |
| `interval` | `1m` | How often the rows changed since the cursor are read. | under `1s` |
| `refresh` | `24h` | How often every row is read again: content changes the rows' `updated_at` missed are written, and the documents of rows gone are deleted. | shorter than `interval` |
| `refresh_retry` | `15m` | How soon a refresh that failed, or couldn't read every row, is tried again: the documents of rows gone wait only for a refresh that completes. | under `1m`; over `refresh` |
| `lag` | `1m` | How far behind the database's clock a pass reads. A transaction open longer than this could commit a row behind the cursor, which only the next refresh would read. `"0s"` is none. | negative |
| `access` | (required) | The row fields every search of the tenant must be scoped by: say, `["label_id"]`. Each row must carry every one. | missing or empty; a name empty, repeated or containing `=` |

When the server starts it records each tenant's access fields, view name and declared facets as
the tenant's policy, which search reads: a tenant can never be searched unscoped.

### `[source.facets]`: what a search may filter on (optional)

```toml
[[source]]
tenant = "releases"
view   = "views/release.view"
access = ["label_id"]
[source.facets]
status    = "keyword"
territory = "keyword"
released  = "date"
bpm       = "number"
```

Each name is a row field, and each value is its type:

| Type | A row's value | A search filters it by |
|---|---|---|
| `keyword` | a string, number or boolean, or a list of them, kept as given (case included) | `filter.facets`: equality, values ORed, fields ANDed |
| `number` | a JSON number, or a string that parses as one (NaN and infinities refused) | `filter.ranges`: `gt`, `gte`, `lt`, `lte` |
| `date` | `YYYY-MM-DD` (midnight UTC) or RFC 3339, or a list of them | `filter.ranges`, as for a number |

- A missing key, a `null`, an empty list or an empty string is no value. A value that doesn't
  parse as its type is the row's error, naming the facet: the row is quarantined, as any row that
  can't be indexed is.
- **Refused when the file loads** (exit 2), naming the source and the facet: a name that is an
  access field (in any case), `tags`, `path` or `title`; a name that isn't a plain identifier
  (letters, digits and `_`, not starting with a digit); a type other than the three.
- **A changed declaration refreshes the tenant at once.** A record's facet values are kept with
  its document, and a pass reads only changed rows, so every record is read again. The refresh
  is due until one completes with the source's declaration, whichever server loaded the new
  configuration, and across restarts. The status shows `"refresh_due": "facets"` until then.
  Searches answer from the documents as they were until the refresh reaches them: a filter on a
  value a document doesn't have yet doesn't match it. The refresh embeds nothing again, since the
  records' text is unchanged.

## Exit codes

| Code | When |
|---|---|
| 0 | stopped by SIGINT or SIGTERM, with every writer lease released; or `--check` found nothing wrong |
| 1 | the file can't be read; the database can't be reached; a tenant's writer lease was lost (restart it: it waits for the lease and takes it again) |
| 2 | the configuration or a view is refused, the field named; `--check` refused a view's order, or the embedding provider refused the key or has no such model |
