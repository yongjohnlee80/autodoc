# Searching from a web application

A web application searches an enterprise-search tenant in one of two ways. Both run the same code
and give the same answer:

- **The Go library**, `github.com/yongjohnlee80/autodoc/enterprise/search`: in-process, on your
  application's own database pool, with no hop. For Go web applications.
- **The HTTP API** that `autodoc --server` serves, `POST /v1/search`: JSON over HTTP, with a bearer
  token per client. For any other stack, and for Go applications that would rather not hold a
  database pool for search.

Either way, **every search carries a scope**: what the caller may see. Your application works it
out from its own session and authorisation, and passes it on each search. There is no search
without one.

The [tutorial](tutorial.md) runs both end to end.

## Scopes

Each `[[source]]` names its **access fields** (`access = ["label_id"]` in `server.toml`), and the
server records them as the tenant's policy. A scope gives, for **every** access field, the values
the caller may see:

```json
{"label_id": ["A", "B"]}
```

- **Within a field, any value admits.** A record whose `label_id` is A is visible to a caller
  scoped to A, or to A and B. A record holding two labels belongs to both.
- **Across fields, every one must admit.** With `access = ["label_id", "territory"]`, a scope of
  `{"label_id": ["A"], "territory": ["US"]}` sees label A's records in the US.
- **The scope is given whole.** Every access field is named, each with at least one non-empty
  value, and no other field. A scope that misses a field, or names one the tenant doesn't have,
  is refused rather than guessed at.
- **The scope is checked against the policy as it is when the search runs,** in the same database
  snapshot the search reads. A server restarted with a new access field makes the next search
  without it refuse.
- **Nothing in the request can widen the scope.** A query word or a filter facet naming an access
  field is refused (`label_id:B`, in any case). Narrow by tags and path prefixes in the filter; the
  scope is applied apart from them, in the database.

## The Go library

```go
import (
	"github.com/yongjohnlee80/golib/dao/postgres"
	gsearch "github.com/yongjohnlee80/golib/search"
	"github.com/yongjohnlee80/golib/search/embed"

	"github.com/yongjohnlee80/autodoc/core/pgstore"
	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// once, at start: a pool in the server's schema, and the server's embedding model, as its
// [embedding] names it (an OpenAI-compatible base_url without its trailing /v1; or
// embed.NewOllama(ctx, base, key, model, nil) for provider = "ollama")
pool, err := postgres.Open(ctx, dsn, pgstore.WithSchema("autodoc_search"))
provider, err := embed.NewOpenAI(ctx, "https://embeddings.example.internal", key, "text-embedding-3-small", nil)
catalog, err := search.Open(ctx, pool, "lm-catalog", search.QueryEmbedder(provider))

// per request: the caller's scope, from your own authorisation
res, err := catalog.Search(ctx, search.Scope{"label_id": labelsOf(user)}, search.Request{
	Query:  r.FormValue("q"),
	Filter: gsearch.Filter{Tags: []string{"single"}},
	Limit:  20,
})
for _, h := range res.Hits {
	// h.SourceID is the row's id: link to your own record
	// h.Snippet marks the matched words (see "Highlighting" below)
}
```

- **`Open(ctx, conn, tenant, qe, opts…)`** binds a `Searcher` to a tenant. `conn` is a pool whose
  connections have the server's schema on their search path (`pgstore.WithSchema`). A tenant the
  server has recorded no policy for is refused (`search.ErrNoPolicy`). A Searcher is safe for
  concurrent use; open one per tenant at start.
- **`qe` embeds the queries.** It must use **the server's model**: the same provider, model name
  and dimensions as the server's `[embedding]`. `search.QueryEmbedder(p)` builds it from a golib
  provider, answering its fingerprint and the normalized vector, as the server stores them. A
  different model can't search by meaning: answers come by words, with `Semantic` `"error"`.
  `nil` searches by words only.
- **`Search(ctx, scope, request)`** answers within the scope.
  - `Request.Mode` is `""` (auto: hybrid when vectors can answer, else words), `"lexical"` or
    `"semantic"`.
  - `Limit` is 1 to 200; 0 is 20.
  - A request with no words and only a filter lists what the filter admits.
- **`Result`** has the hits, `ModeUsed`, and `Semantic`:
  - `"ready"`: every record can be found by meaning;
  - `"partial"`: some are still being embedded;
  - `"switching"`: the server is changing models, so the answer is by words;
  - `"error"`: the query couldn't be embedded, so the answer is by words.

### Errors

Each is a sentinel; test with `errors.Is`.

| Error | Means |
|---|---|
| `search.ErrScope` | The scope isn't the tenant's access: a field missing, extra or without values; a value empty, repeated or `*`. |
| `search.ErrProtectedFacet` | The query or the filter names an access field. |
| `search.ErrUnknownFacet` | The filter has a facet; a tenant keeps none but its access fields. Filter by tags or paths. |
| `search.ErrLimit` | The limit is below 0 or above the most. |
| `search.ErrNoPolicy` | The tenant has no policy: the server hasn't recorded it (check the tenant's name and that the server has started). |
| `gsearch.ErrUnknownMode` | The mode isn't one of the three. |
| `gsearch.ErrSwitching`, `gsearch.ErrEmbedFailed` | Semantic mode only: it can't answer by meaning now. Auto mode answers by words instead. |

## The HTTP API

`autodoc --server` serves it when `server.toml` has an `[http]` table
([server-config](server-config.md#http-and-httpclient-the-search-api-optional)). Each client has a
bearer token and grants: by tenant, the values of each access field it may ask for.

```sh
curl -s http://127.0.0.1:8730/v1/search \
  -H "Authorization: Bearer $LM_WEB_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"tenant": "lm-catalog", "query": "love song", "scope": {"label_id": ["A"]}}'
```

### The request

| Field | | Meaning |
|---|---|---|
| `tenant` | required | The `[[source]]` tenant to search. |
| `scope` | required | The scope, as above. Every value must be granted to the token; a `["*"]` grant admits any value the request names. |
| `query` | | The words, at most 1 KiB. |
| `mode` | | `"auto"` (the default), `"lexical"` or `"semantic"`. |
| `filter` | | `{"tags": […], "paths": […]}`: narrows within the scope. |
| `limit` | | 1 to 200; 0 or absent is 20. |

The body is one JSON object of at most 64 KiB, with no other field.

### The answer

```json
{"tenant": "lm-catalog", "mode_used": "hybrid", "semantic": "ready",
 "hits": [{"path": "track/42.md", "source_id": "42", "breadcrumb": "Love Song",
           "snippet": "a \u0002love\u0003 song", "generation": 3, "byte_start": 0, "byte_end": 97,
           "score": 0.031, "relevance": 0.87, "via": ["lexical", "semantic"]}]}
```

- **`source_id`** is the id of the row the hit was rendered from: link back to your record by it.
  It's `""` for a record indexed before the server kept source ids (AutoDoc before M3), until the
  tenant's next full refresh writes it.
- **`path`** is the view's `export` path (`track/{{.id}}.md`).
- **`via`** says which ways found the hit: `lexical`, `semantic`, or `facet` for a filter-only list.
- **`relevance`** is 0 to 1, comparable across queries; **`score`** orders hits within one answer.

### Errors

An error answers `{"error": {"code": "…", "message": "…"}}`. The checks run in this order, so a
caller without a valid token learns nothing about tenants or bodies:

| Status | `code` | When |
|---|---|---|
| 404 | `not_found` | Any path but `/v1/search`. |
| 405 | `method_not_allowed` | Any method but POST (`Allow: POST`). |
| 401 | `unauthorized` | No `Authorization: Bearer …`, or no client has that token (`WWW-Authenticate: Bearer`). |
| 415 | `unsupported_media_type` | The body isn't `application/json`. |
| 413 | `too_large` | The body is over 64 KiB. |
| 408 | `request_timeout` | The body didn't arrive within 5 s. |
| 400 | `bad_request` | Not one JSON search request: an unknown field, a second value, a query over 1 KiB. |
| 403 | `forbidden` | The token isn't granted the tenant. A tenant that doesn't exist answers the same. |
| 403 | `scope_forbidden` | A scope field or value the token isn't granted. |
| 400 | `scope_required`, `protected_facet`, `unknown_facet`, `bad_limit`, `bad_mode` | The library's refusals, above. |
| 503 | `switching` | Semantic mode while the tenant switches models (`Retry-After`). Use auto mode to be answered by words meanwhile. |
| 502 | `embed_failed` | Semantic mode, and the query couldn't be embedded. |
| 503 | `unavailable` | The search didn't finish within 25 s, or the server is stopping (`Retry-After`). |
| 500 | `internal` | Anything else. The cause is in the server's log, never in the answer. |

### Time and shutdown

- **Each request has 30 s:** its body by 5 s, its search by 25 s, and its answer written by 30 s.
- **On SIGTERM the server stops taking requests and answers those in flight** before it closes
  anything they use.
- **Tokens and query text never reach the log.** It names the client, the tenant, the status, the
  hit count and the query's length.

## Highlighting

A snippet marks each matched word between `\u0002` and `\u0003` (golib's highlight markers): control
characters no document text contains. To show them as `<mark>`, split on the markers and escape
**every** segment, so a record's own text can never inject markup:

```js
function highlight(snippet) {
  const esc = (s) => s.replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]);
  return snippet.split("\u0002").map((part, i) => {
    if (i === 0) return esc(part);
    const [hit, rest = ""] = part.split("\u0003");
    return "<mark>" + esc(hit) + "</mark>" + esc(rest);
  }).join("");
}
```

```go
// Highlight is s as HTML: every segment escaped, the matched words in <mark>.
func Highlight(s string) template.HTML {
	var b strings.Builder
	for i, part := range strings.Split(s, "\x02") {
		if i == 0 {
			b.WriteString(html.EscapeString(part))
			continue
		}
		hit, rest, _ := strings.Cut(part, "\x03")
		b.WriteString("<mark>" + html.EscapeString(hit) + "</mark>" + html.EscapeString(rest))
	}
	return template.HTML(b.String())
}
```

## A minimal Go client of the HTTP API

```go
body, _ := json.Marshal(map[string]any{
	"tenant": "lm-catalog", "query": q, "scope": map[string][]string{"label_id": labels},
})
req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/v1/search", bytes.NewReader(body))
req.Header.Set("Authorization", "Bearer "+token)
req.Header.Set("Content-Type", "application/json")
res, err := http.DefaultClient.Do(req)
// decode {"hits": [{"path", "source_id", "snippet", …}]}, or {"error": {"code", "message"}}
```

The [tutorial](tutorial.md) has it whole, and runs it.
