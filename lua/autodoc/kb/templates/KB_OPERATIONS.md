---
type: kb
kind: operations
status: active
created: {{date}}
autodoc_version: {{autodoc_version}}
revision: 1
tags: [kb, autodoc, operations]
abstract: "How agents search, read and write this KB through AutoDoc. Shipped and versioned by AutoDoc; re-read it when its revision changes."
---

# KB operations

This file is AutoDoc's, and is replaced when a newer AutoDoc ships a newer copy. Put this KB's own
rules in `AGENTS.md`. `RULES.md` comes before both.

`$AUTODOC_WORKSPACE` is this KB's AutoDoc workspace and `$AUTO_AGENTS_KB_ROOT` its root. Paths on
the wire are relative to the root, with `/` separators.

## 1. Search first

```sh
autodoc --call search.query '["'"$AUTODOC_WORKSPACE"'", "<words>", {"limit": 10}]'
```

- Each hit gives `path`, `breadcrumb`, `snippet` and `line_start`..`line_end`.
- Read that line range with your own Read tool. Open the whole document only when the range isn't
  enough.
- Search before writing, so you extend a document instead of duplicating it.
- `"<words>"` is words, not operators: every word must appear in a hit found by words, and
  `"a phrase"` matches in order. `sto*` is a prefix.

## 2. Filters, facets and stages

Options go in the third element, all optional:

| Option | Effect |
|---|---|
| `"paths": ["adrs"]` | only under these folders or files |
| `"tags": ["autodoc"]` | only files with every one of these tags |
| `"facets": {"type": "review", "status": "completed"}` | only files whose frontmatter field has one of the values |
| `"stages": ["lexical"]` | words only; `["semantic"]` meaning only; add `"rerank"` to re-rank. Compare methods by changing this |
| `"limit": 20` | up to 200 hits |

`field:value` in the query is the same filter as a facet, for any field the schema declares (`type`,
`status`, `repo`, `author`, `kind`, `verdict`, …): `migration type:adr status:accepted`. A query of
filters alone lists the files they admit.

## 3. Listings and recency

`index.documents` lists files with their frontmatter. Use it where you would have read an index page:

```sh
autodoc --call index.documents '["'"$AUTODOC_WORKSPACE"'", {"sort": "updated", "paths": ["adrs"], "limit": 50}]'
autodoc --call index.documents '["'"$AUTODOC_WORKSPACE"'", {"missing": ["abstract"], "limit": 100}]'
```

- `sort` is `updated` (newest first), `path` or `indexed`.
- `missing` lists files that lack a field: the gaps to fill.
- `facets`, `tags` and `paths` narrow it as they narrow a search. Page with `after` (the previous
  answer's `next`).

## 4. History

- **When the KB is a git repo, its git log is the durable audit trail:**
  `git -C "$AUTO_AGENTS_KB_ROOT" log --stat -- <path>`.
- A KB need not be a git repo. Then the durable history is whatever the user keeps.
- `index.changes` is recent index activity only. It is bounded (at least 7 days and 100,000 rows)
  and its cursor may expire.
- `sys.events` is the daemon's recent configuration log, not a permanent record.

## 5. Relations

- `graph.links` (what a file links to) and `graph.backlinks` (what links to it), both
  `[workspace, path]`.
- `graph.neighborhood` `[workspace, path, depth]`: a file and its links a few hops out.

## 6. Writing

- Write files with your own tools, in this KB's folders. AutoDoc watches the KB and indexes what you
  write.
- **Every document carries frontmatter valid against `_schema/frontmatter.yaml`.** Start from
  `_templates/<type>.md`. Required for every type: `type`, `status`, `created`, `tags`, `abstract`.
  Each type adds its own fields.
- **Check a draft before writing it:**
  `autodoc --call doc.validate '["'"$AUTODOC_WORKSPACE"'", "<path>", "<full text>"]'`. It answers
  `{diagnostics: [{field, line, rule, message}]}`. Diagnostics never block a write, but fix them.
- `ABOUT.md` in each folder says what goes there, how files are named and which `type` they carry.
  "Where does X go" is a search: `"<X>" type:about`.
- YAML frontmatter is the source of truth. Don't add inline `**Tags:**` or `**Abstract:**` lines.
- Link with paths relative to the KB root (`[[adrs/<file>]]`) or relative Markdown links.

## 7. When AutoDoc can't answer

If the daemon is down or the workspace isn't ready, search with `rg` over
`$AUTO_AGENTS_KB_ROOT`, and say in your work that you did.

## 8. What a semantic-less answer means

The answer's `stages.skipped` names each stage that didn't run and why (no model, a model switching,
the query not embedded, no ranker). A search with `semantic` skipped matched by words only: a miss
there doesn't mean the KB has nothing on the subject. Try other words before concluding that.
