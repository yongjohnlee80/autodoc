---
type: kb
kind: operations
status: active
created: {{date}}
autodoc_version: {{autodoc_version}}
revision: 2
tags: [kb, autodoc, operations]
abstract: "How agents search, read and write this KB through AutoDoc. Shipped and versioned by AutoDoc; re-read it when its revision changes."
---

# KB operations

This file is AutoDoc's, and is replaced when a newer AutoDoc ships a newer copy. Put this KB's own
rules in `AGENTS.md`. `RULES.md` comes before both.

`$AUTODOC_WORKSPACE` is this KB's AutoDoc workspace and `$AUTO_AGENTS_KB_ROOT` its root. Paths on
the wire are relative to the root, with `/` separators.

- If `$AUTODOC_WORKSPACE` is unset, run `autodoc --call workspace.list '[]'` and use the `name` whose
  `root` is `$AUTO_AGENTS_KB_ROOT`.

## 0. Pick the method from what this machine has

Your first search tells you which stages ran: the answer's `stages.performed` and `stages.skipped`.
Pick the method from that, once per session:

| The first search shows | Use |
|---|---|
| `semantic` and `rerank` performed | AutoDoc search for every lookup (§1). This is the best case. |
| `semantic` performed, `rerank` skipped | AutoDoc search still. Ranking is weaker but well ahead of grep. |
| `semantic` skipped (no embedding model, a model switching, the query not embedded) | `rg` over the tree (§7). Keep AutoDoc for listings, facets and relations (§2, §3, §5), which need no model. |
| no answer (daemon down, workspace not ready) | `rg` over the tree (§7). |

Why, measured on a 2,200-document KB (32 real lookups, 2026-10-06, AutoDoc 0.1.17):

- **Ranking.** With an embedding model and a reranker, the answer document was in the top 5 for 94%
  of questions; with an embedding model alone, 78%. A keyword grep ranking over the tree (skipping
  `archive/` and `raw/`) got 47%.
- **Cost to an agent.** Agents found every answer with both methods; the method changed the cost.
  AutoDoc search-first pulled about 11% fewer KB tokens into context than grep over the same tree
  (23% fewer than grep over the pre-AutoDoc layout), used fewer tool calls, and finished faster. The
  heaviest lookups improved most.
- **Without an embedding model**, a search runs on words alone, and a word search needs every word to
  appear (§1). A natural-language question then finds nothing (0 hits on all 32). Grep, which agents
  used successfully on every question, is the safer first move there. A words-only AutoDoc search with
  a few keywords wasn't measured.

## 1. Search first

```sh
autodoc --call search.query '["'"$AUTODOC_WORKSPACE"'", "<words>", {"limit": 10}]'
```

- **Ask in plain words.** Leave `stages` at its default, which combines meaning, words and the
  reranker. A short natural-language query of about 4 to 8 words works best, for example
  `"merge a stacked PR with --delete-branch"`. Don't paste a whole paragraph.
- Each hit gives `path`, `breadcrumb`, `snippet` and `line_start`..`line_end`.
- **Read only that line range** with your own Read tool, then stop if it answers you. Open the whole
  document only when the range isn't enough. Reading ranges instead of whole files is where most of
  the saving comes from.
- No good hit? Search again with other words (the document's likely vocabulary) before concluding
  the KB has nothing.
- Search before writing, so you extend a document instead of duplicating it.
- **Words-only search (`"stages": ["lexical"]`, or any search on a machine without a model) is not
  for questions.** Every word must appear in a hit, so pass two or three distinctive terms
  (`"--delete-branch stacked"`), not a sentence. `"a phrase"` matches in order. `sto*` is a prefix.

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

## 7. Grep: without a model, or when AutoDoc can't answer

Use `rg` over `$AUTO_AGENTS_KB_ROOT` when §0 says so, and say in your work that you did.

```sh
rg -n -i '<distinctive term>' "$AUTO_AGENTS_KB_ROOT" --glob '*.md' --glob '!archive/**' --glob '!raw/**'
```

- **Skip `archive/` and `raw/`.** Retired material lives there, often old indexes and logs that
  mention everything and crowd out the live document. Skipping them doubled grep's ranking in the
  measurement above.
- Search for the term the answer document would use, not the question's words; narrow with
  `--glob '<folder>/**'` when you know the folder (`ABOUT.md` says what each holds).
- Read the matching lines' section, not the whole file, and stop when it answers you.

## 8. What a semantic-less answer means

The answer's `stages.skipped` names each stage that didn't run and why (no model, a model switching,
the query not embedded, no ranker). A search with `semantic` skipped matched by words only: a miss
there doesn't mean the KB has nothing on the subject. Retry with two or three distinctive terms, or
switch to grep (§0, §7), before concluding that.
