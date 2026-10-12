# Writing a `.view` source

A source is one `.view` file: a SQL query that returns your rows as JSON, and a template that renders
each row into a document. The server reads it page by page, keeps a cursor into it, and indexes
each document into the source's tenant. Back to [Enterprise search](README.md).

A `.view` file is golib's format: YAML frontmatter between `---` lines, then the body template. A
source view is an ordinary one with a few rules on top, so the server can page it without missing a
row, and name each document before its body renders.

## A complete example

Tracks joined to their artists, searchable per label:

```text
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

And its source in `server.toml`:

```toml
[[source]]
tenant = "lm-catalog"
view   = "views/track.view"
access = ["label_id"]
```

## The arguments, and the order

Every source view declares exactly these three arguments, in this order:

```yaml
args: [after_ts, after_id, upto_ts]
```

| | Argument | What the server binds |
|---|---|---|
| `$1` | `after_ts` | the `updated_at` of the last row the tenant's cursor passed |
| `$2` | `after_id` | that row's `id`; **NULL** on the tenant's very first page, when `after_ts` is the zero time |
| `$3` | `upto_ts` | the pass's upper bound: the database's `now()` minus the source's `lag` |

The query returns **the rows after the cursor, up to `upto_ts`, in cursor order**:

```sql
WHERE (updated_at_expr, id) > ($1, $2)
  AND updated_at_expr <= $3
ORDER BY updated_at_expr, id
LIMIT 500
```

- **Compare the pair, not the timestamp alone.** `(updated_at, id) > ($1, $2)` is a row
  comparison: rows sharing one `updated_at` are ordered by `id`, so a page boundary inside a run of
  equal timestamps resumes exactly where it stopped. `updated_at > $1` alone skips the rest of the
  run.
- **Order by the same expressions, explicitly.** `ORDER BY` must name the `updated_at` expression,
  then `id`, the same ones the `WHERE` compares. Never `ORDER BY 1`: column 1 is the JSON object,
  whose order is not the cursor's.
- **`LIMIT` is your page size.** Each page is one transaction in the index, with the cursor. 500 is
  a good start.
- **Bound by `$3`.** A business transaction that began before a row already committed can commit
  later with an earlier `updated_at`. Reading only up to `now() − lag` keeps the cursor behind any
  such transaction shorter than the lag. The full refresh catches a longer one.
- The query must use `$1`, `$2` and `$3`, and no other parameter. The first page binds `$2` as
  NULL. `(x, id) > ($1, NULL)` is true for any `x` later than `$1`, so the row comparison handles it
  as it is. If you write the comparison out by hand, `x > $1 OR (x = $1 AND id > $2)` does too.

The server checks each page as it reads it. A row before the cursor, past `upto_ts`, or earlier than
the row before it stops the pass, and nothing of the page is written. The ids within one timestamp
are compared by the database, by their type and collation, so only a real database can check that
part: `--check` does (below).

## `updated_at` must cover what the document shows

`updated_at` is how a pass finds a changed row. It must change when **anything the document
renders** changes, the joined tables included. That is why the example takes
`GREATEST(t.updated_at, a.updated_at)`: renaming an artist must move every one of their tracks
past the cursor. Use the same expression in the JSON, the `WHERE` and the `ORDER BY`.

When a dependency has no timestamp of its own, give it one (a column and a trigger), or keep a
change table and take its greatest timestamp. What a view still misses is caught by the full
refresh: it compares each document's content, not its timestamp, and writes the ones that changed
within one `refresh` period.

`updated_at` should be a `timestamptz` and never NULL: a row whose `updated_at` is NULL is never
returned by the comparison at all (`COALESCE` it if it can be).

## The row's JSON

`process` returns one JSON object per row, in one column. The server reads these fields:

| Field | Required | Meaning |
|---|---|---|
| `id` | yes | The row's identity in the source: a string, or a number (kept as its digits). The cursor and the quarantine key on it, and the document's path is made from it. It must be the same `id` the `WHERE` compares. |
| `updated_at` | yes | The cursor's timestamp, as PostgreSQL writes a `timestamptz` into JSON. A timestamp with no zone is taken as UTC. |
| every `access` field | yes | Each field the source's `access` lists: a string, number or boolean, or a non-empty list of them. A record with several values (a track shared by two labels) is visible to callers allowed any one of them. |
| `title` | no | The document's title; else the Markdown's own (its frontmatter or first heading); else the `id`. |
| `tags` | no | A string or a list of strings, lowercased, added to the Markdown's own tags. |

Every other field is yours, for the template.

**A row that fails is quarantined, never skipped.** A row missing `updated_at` or an access field,
or with a malformed title or tags, or whose body doesn't render, goes to quarantine with its error,
and the cursor moves past it. A document it already had is kept as it was. Every pass retries the
quarantined rows first: a row that now renders is written and leaves quarantine, and a row the
query no longer returns leaves too. The log says, after every pass, how many rows wait in
quarantine. Fix them: while any wait, each pass reads the view from its start until it has seen
each of them (the view selects by position, not by id), which costs up to a full read per pass.

A row with no `id` at all can be neither indexed nor quarantined. It is counted in the log, and a
refresh that read one deletes nothing. A page made only of rows with no `updated_at` stops the pass:
the cursor can't move past it.

## The export: a path from the id alone

```yaml
export: track/{{.id}}.md
```

`export` names each row's document. It may contain **literal text and `{{.id}}` only**, at least
once. No other field, no function, no `if` or `range`. A row's path must be known from its `id`
before its body renders: a full refresh counts a row whose body fails as *seen*, and keeps its
document. A path made from another field could not be named for that row, and a refresh would take
it for a row deleted.

The export's extension sets how the body is chunked:

- **`.md`** (or `format: markdown`): parsed as Markdown, cut at its headings, and its frontmatter's
  `abstract:` becomes a chunk of its own;
- **anything else**: cut as plain text, by paragraph.

The body is a golib template: `{{.field}}`, `{{if}}`, `{{range}}`, and the helpers `default`,
`lower`, `upper`, `trim`, `quote`, `toJson` and `raw`. A NULL or missing field prints as nothing.

## Access fields

`access` in `server.toml` lists the fields every search of the tenant is scoped by. A web
application searches with a scope such as `{"label_id": ["label-17"]}`, and sees only the
documents whose `label_id` is one of those values. The scope is applied in SQL, apart from the
query, so no query text can widen it. When the server starts it records each tenant's access fields
as its policy, so the tenant can never be searched unscoped.

Choose fields your authorization already decides on: a label, a tenant organisation, a region.
Every row must carry each one. A row without one is quarantined, never indexed unscoped.

## What is checked, and when

**When the server starts, or `--check` runs**, each view is refused, naming its source and the
view's field, when:

- `source` is not `postgres`;
- `args` are not exactly `[after_ts, after_id, upto_ts]`;
- `process` doesn't use `$1`, `$2` and `$3`, or uses another parameter;
- `export` is missing, prints anything but `{{.id}}`, or doesn't print it;
- golib refuses the file: a missing field, a template that doesn't parse, `{{` in the query.

**`autodoc --server --check server.toml`** then runs each view against your database, read-only, in
one snapshot:

1. It reads the view from its start, page by page as the server does, up to `now() − lag`, at most
   2000 rows. Every row must carry an `id` and an `updated_at`, and the timestamps must never go
   back.
2. At each page boundary, and between each two rows that share an `updated_at` (up to 200 places),
   it resumes the view from the first row's `(updated_at, id)` and checks that the second row
   comes next.

A view ordered by its JSON column, by `id`, by `id` descending within a timestamp, or whose `WHERE`
compares the timestamp alone, returns another row there, and is refused (exit 2). The check runs
against the live rows because only the database can compare your ids: their type and collation
decide the order. If no two rows read share a timestamp, the id order can't be exercised, and
`--check` says so as a warning. An empty source is a warning too.

```text
$ autodoc --server --check server.toml
source[0] lm-catalog: /etc/autodoc/views/track.view: order confirmed over 2000 rows (the first 2000) in 4 pages: 37 tied rows, 3 page boundaries resumed
/etc/autodoc/server.toml: ok
```

## The grants

The server needs, on your database:

- `SELECT` on the tables its views read, and `USAGE` on their schema;
- its own schema, to keep the index in;
- the `vector` extension, installed once by a superuser or the database's owner.

The least privilege: a DBA creates the role, its schema and the extension, and the role creates
nothing outside its schema:

```sql
CREATE ROLE autodoc_search LOGIN PASSWORD '…';
GRANT CONNECT ON DATABASE app TO autodoc_search;

-- the index: a schema the role owns, and pgvector where its search_path finds it
CREATE SCHEMA autodoc_search AUTHORIZATION autodoc_search;
CREATE EXTENSION IF NOT EXISTS vector SCHEMA public;

-- the views: read-only
GRANT USAGE ON SCHEMA public TO autodoc_search;
GRANT SELECT ON track, artist TO autodoc_search;
```

The server then finds its schema, creates its tables there, and never writes anywhere else. To let
the server create the schema itself instead, also `GRANT CREATE ON DATABASE app TO autodoc_search`.

The views run on a connection with the role's own `search_path`, as in `psql`. So qualify the
tables (`app.track`), or set the role's path (`ALTER ROLE autodoc_search SET search_path = app,
public`). The index's own connections set theirs to the configured schema, then `public`.

## Testing a view

1. Run the query in `psql` as the server's role, with the first page's arguments:

   ```sql
   PREPARE p(timestamptz, bigint, timestamptz) AS <your process query>;
   EXECUTE p('0001-01-01', NULL, now() - interval '1 minute');
   ```

   Then run the next page from the last row's `updated_at` and `id`.
2. Run `autodoc --server --check server.toml`.
3. Start the server and watch its log: each pass says how many rows it read, wrote and quarantined.
