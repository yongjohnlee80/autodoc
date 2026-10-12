# Tutorial: search a music catalog from a web application

This tutorial runs enterprise search end to end on your machine, in about ten minutes:

1. a PostgreSQL with pgvector, holding a small music catalog;
2. a `.view` that turns each track into a document;
3. `autodoc --server` indexing the catalog and embedding it;
4. searches from curl and from a Go program, each scoped to a record label;
5. a track changed in the database, found by its new words.

It needs Docker, `curl`, Go, and an embedding model. The tutorial uses
[Ollama](https://ollama.com) with `nomic-embed-text` (`ollama pull nomic-embed-text`); any
OpenAI-compatible endpoint works too (see [server-config](server-config.md)).

> **This tutorial is a test.** AutoDoc's CI runs every command here, in order, against a real
> PostgreSQL with pgvector, and checks what they print. Each code block is marked for it, which a
> reader can ignore: `tutorial` blocks are run, `file=` blocks are written to that file, `expect`
> blocks must appear in the output of the command before them, and `absent` blocks must not. The CI
> brings its own database instead of the `compose` and `setup` steps, and its own embedding endpoint
> instead of Ollama.

## 1. The database

Save this as `docker-compose.yml`, and start it with `docker compose up -d`:

```yaml compose
services:
  db:
    image: pgvector/pgvector:pg18
    environment:
      POSTGRES_USER: catalog
      POSTGRES_PASSWORD: catalog
      POSTGRES_DB: catalog
    ports: ["5432:5432"]
```

The server reads its database URL and the web application's token from the environment, never from
its configuration file:

```sh setup
export AUTODOC_SERVER_DSN='postgres://catalog:catalog@localhost:5432/catalog?sslmode=disable'
export LM_WEB_TOKEN=$(openssl rand -hex 32)
```

Now the catalog: artists, and tracks on two record labels, `A` and `B`. Every table a document is
rendered from has an `updated_at`, which the server follows to keep the index current.

```sql tutorial
CREATE EXTENSION IF NOT EXISTS vector;
CREATE TABLE artist (
  id         bigint PRIMARY KEY,
  name       text NOT NULL,
  updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE track (
  id         bigint PRIMARY KEY,
  label_id   text NOT NULL,
  title      text NOT NULL,
  artist_id  bigint NOT NULL REFERENCES artist,
  lyric      text,
  updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO artist (id, name) VALUES (1, 'Ann Arbor'), (2, 'The Basalts');
INSERT INTO track (id, label_id, title, artist_id, lyric) VALUES
  (1, 'A', 'Crimson River', 1, 'the crimson river runs down to the sea'),
  (2, 'A', 'Golden Morning', 1, 'wake me when the morning comes'),
  (3, 'B', 'Midnight Ocean', 2, 'a lullaby for the midnight ocean');
```

## 2. The view

A `.view` file is a query and a template ([views](views.md)). Each row of the query is one record;
the template renders it into the document that is searched. Save this as `views/track.view`:

```text file=views/track.view
---
version: 1
name: track
source: postgres
args: [after_ts, after_id, upto_ts]
process: |
  SELECT jsonb_build_object(
    'id',         t.id,
    'label_id',   t.label_id,
    'title',      t.title,
    'artist',     a.name,
    'lyric',      t.lyric,
    'updated_at', GREATEST(t.updated_at, a.updated_at))
  FROM track t
  JOIN artist a ON a.id = t.artist_id
  WHERE (GREATEST(t.updated_at, a.updated_at), t.id) > ($1, $2)
    AND GREATEST(t.updated_at, a.updated_at) <= $3
  ORDER BY GREATEST(t.updated_at, a.updated_at), t.id
  LIMIT 500
export: track/{{.id}}.md
---
# {{.title}}
by {{.artist}}

{{if .lyric}}{{.lyric}}{{else}}*(Instrumental)*{{end}}
```

- **`label_id` is the access field:** every search must say which labels its caller may see.
- **`updated_at` is the latest of the track's and its artist's,** so renaming an artist re-indexes
  their tracks.

## 3. The server

Save this as `server.toml`:

```toml file=server.toml
[postgres]
dsn_env = "AUTODOC_SERVER_DSN"
schema  = "autodoc_search"

[embedding]
provider = "ollama"
base_url = "http://localhost:11434"
model    = "nomic-embed-text"

[http]
listen = "127.0.0.1:8730"

[[http.client]]
name      = "lm-web"
token_env = "LM_WEB_TOKEN"

[http.client.grants.lm-catalog]
label_id = ["A"]

[[source]]
tenant   = "lm-catalog"
view     = "views/track.view"
access   = ["label_id"]
interval = "5s"
lag      = "0s"
```

- **The `lm-web` client may search label `A` only.**
- **`interval` and `lag` are short here,** so a change shows in seconds. In production, keep the
  default lag (1m): it waits out transactions still open in your database.

Check it before starting: the views' order against the live data, and the embedding model.

```sh tutorial
autodoc --server --check server.toml
```

```text expect
order confirmed over 3 rows
server.toml: ok
```

Then start it, and leave it running:

```sh server
autodoc --server server.toml
```

It creates the `autodoc_search` schema, indexes the three tracks, and embeds them. Wait until
search can answer by meaning:

```sh tutorial
for i in $(seq 60); do
  curl -s http://127.0.0.1:8730/v1/search \
    -H "Authorization: Bearer $LM_WEB_TOKEN" -H 'Content-Type: application/json' \
    -d '{"tenant": "lm-catalog", "query": "river", "scope": {"label_id": ["A"]}}' \
    | grep -q '"semantic":"ready"' && break
  sleep 1
done
```

## 4. Search

From curl, scoped to label `A`:

```sh tutorial
curl -s http://127.0.0.1:8730/v1/search \
  -H "Authorization: Bearer $LM_WEB_TOKEN" -H 'Content-Type: application/json' \
  -d '{"tenant": "lm-catalog", "query": "crimson river", "scope": {"label_id": ["A"]}}'
```

```text expect
"path":"track/1.md"
"source_id":"1"
```

```text absent
"source_id":"3"
```

The hit's `source_id` is the track's `id`: your application links back to its own record by it.
Track 3 is on label `B`, outside the scope: no query can reach it. Asking for label `B` is refused,
because the token isn't granted it:

```sh tutorial
curl -s http://127.0.0.1:8730/v1/search \
  -H "Authorization: Bearer $LM_WEB_TOKEN" -H 'Content-Type: application/json' \
  -d '{"tenant": "lm-catalog", "query": "ocean", "scope": {"label_id": ["B"]}}'
```

```text expect
"code":"scope_forbidden"
```

From Go, the same request. Save this as `client/main.go`:

```go file=client/main.go
// The tutorial's client: searches the catalog within label A, and prints each hit's path and id.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

func main() {
	body, _ := json.Marshal(map[string]any{
		"tenant": "lm-catalog",
		"query":  strings.Join(os.Args[1:], " "),
		"scope":  map[string][]string{"label_id": {"A"}},
	})
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:8730/v1/search", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+os.Getenv("LM_WEB_TOKEN"))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer res.Body.Close()
	var out struct {
		Hits []struct {
			Path     string `json:"path"`
			SourceID string `json:"source_id"`
		} `json:"hits"`
		Error struct{ Code, Message string } `json:"error"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil || res.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, res.Status, out.Error.Code, out.Error.Message, err)
		os.Exit(1)
	}
	for _, h := range out.Hits {
		fmt.Println(h.Path, h.SourceID)
	}
}
```

```sh tutorial
go run client/main.go morning
```

```text expect
track/2.md 2
```

A Go web application can also search in-process, with no HTTP hop, through the
`enterprise/search` package: see [web-integration](web-integration.md).

## 5. A change in the database

Change track 2's title and lyric:

```sql tutorial
UPDATE track SET title = 'Saffron Morning', lyric = 'saffron light at the break of day', updated_at = now() WHERE id = 2;
```

Within one `interval` the server reads the changed row, re-indexes it and embeds the new text.
Searching by words alone (`"mode": "lexical"`), the new word finds it, marked in the snippet
between `\u0002` and `\u0003` ([highlighting](web-integration.md#highlighting)):

```sh tutorial
for i in $(seq 60); do
  curl -s http://127.0.0.1:8730/v1/search \
    -H "Authorization: Bearer $LM_WEB_TOKEN" -H 'Content-Type: application/json' \
    -d '{"tenant": "lm-catalog", "query": "saffron", "mode": "lexical", "scope": {"label_id": ["A"]}}' \
    | grep -q '"source_id":"2"' && break
  sleep 1
done
curl -s http://127.0.0.1:8730/v1/search \
  -H "Authorization: Bearer $LM_WEB_TOKEN" -H 'Content-Type: application/json' \
  -d '{"tenant": "lm-catalog", "query": "saffron", "mode": "lexical", "scope": {"label_id": ["A"]}}'
```

```text expect
"source_id":"2"
\u0002saffron\u0003
```

## Where next

- [web-integration](web-integration.md): the library and the HTTP API in full, the errors, and
  highlighting the matched words.
- [views](views.md): writing your own `.view` files, and the rules `--check` holds them to.
- [server-config](server-config.md): every field of `server.toml`.
