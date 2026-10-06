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

## 0. Pick the method

**Known target? Go straight to it.** When you already have the path, a wikilink, an identifier, a
commit or an exact error string (from memory, a task's `adr:`/`review:` fields, a link), read it or
`rg` for it directly. Searching for what you can name costs more than reading it.

**Topic lookup? Search, and let the first answer tell you the mode.** Each answer's
`stages.performed` and `stages.skipped` say which stages ran. Pick the method from that, once per
session:

| The first search shows | Use |
|---|---|
| `semantic` and `rerank` performed | AutoDoc search, `limit 3` (§1). The best case. |
| `semantic` performed, `rerank` skipped | AutoDoc search, `limit 10`: without the reranker the answer often sits below the top 3. |
| `semantic` skipped (no embedding model, a model switching, the query not embedded) | The words-only ladder (§1, then §7). Keep AutoDoc for listings, facets and relations (§2, §3, §5), which need no model. |
| no answer (daemon down, workspace not ready) | `rg` (§7). |

Why, measured 2026-10-06 on two KBs with AutoDoc 0.1.17:

- the global KB: 2,200 docs, 32 lookups, 96 agent runs; Ollama `snowflake-arctic-embed2` and a
  reranker;
- the LabelManager KB: 18 lookups; `bge` and `bge-reranker-v2-m3`.

What the measurements showed:

- **Ranking with the reranker.**
  - Global KB: the answer document was in the top 3 for 94% of questions, the same as the top 10.
  - LabelManager: 18 of 18 in the top 3, and first on 15 of 18 when scoped with `paths`.
  - Limit 3 costs about 60% fewer tokens than limit 10, for no loss in accuracy.
  - Without the reranker (global KB): 72% in the top 3 and 91% in the top 10.
- **Ranking with grep.** A keyword grep ranking over the tree got 47% in the top 5 (skipping
  `archive/` and `raw/`).
- **Cost to an agent (global KB).**
  - Agents found every answer by either method; the method changed the cost.
  - AutoDoc search-first pulled about 11% fewer KB tokens into context than grep over the same tree,
    and 23% fewer than grep over the pre-AutoDoc layout.
  - It also used fewer tool calls and finished faster. The heaviest lookups improved most.
- **Without an embedding model** a search runs on words alone (LabelManager KB).
  - A plain-language question found the answer 1 time in 18.
  - Two or three distinctive keywords put it first 12 times and found it 14 times. A substring grep
    found 17, because words-only search misses unstarred prefixes and wording that differs.
  - Hence the ladder: AutoDoc first (cheap), then one retry, then grep.

## 1. Search first

```sh
autodoc --call search.query '["'"$AUTODOC_WORKSPACE"'", "<words>", {"limit": 3}]'
```

- **Ask in plain words.** Leave `stages` at its default, which combines meaning, words and the
  reranker. A short natural-language query of about 4 to 8 words works best, for example
  `"merge a stacked PR with --delete-branch"`. Don't paste a whole paragraph.
- **Scope when you can.** `"paths": ["conventions", "adrs"]` keeps the search to the folders that hold
  the answer. `archive/` is indexed unless the workspace excludes it (the user's choice), and its
  retired indexes and logs mention everything, so leave it out of `paths` unless you want history.
- Each hit gives `path`, `breadcrumb`, `snippet` and `line_start`..`line_end`.
- **Read only that line range** with your own Read tool, then stop if it answers you. Read a whole
  document only when it must be read end to end (a convention, an ADR, a review you are acting on).
  Reading ranges instead of whole files is where most of the saving comes from: a median 2.6k tokens
  for a whole KB file, against about 220 for a hit's range.
- No good hit? Search again with other words (the document's likely vocabulary) before concluding
  the KB has nothing.
- Search before writing, so you extend a document instead of duplicating it.
- **The words-only ladder** (`"stages": ["lexical"]`, or any search on a machine without a model):
  1. Pass two or three distinctive terms (`"--delete-branch stacked"`), never a sentence. Every word
     must appear as a whole word in one section. `"a phrase"` matches in order, and `sto*` is a
     prefix.
  2. No hit? Retry once: drop a word, or star a prefix (`subscri*`).
  3. Still nothing? Use `rg` with the terms OR'd (§7), and say in your work that you fell back.

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
rg -il -e '<term>' -e '<other term>' "$AUTO_AGENTS_KB_ROOT" --glob '*.md' --glob '!archive/**' --glob '!raw/**'
rg -n -i -C1 '<term>' "$AUTO_AGENTS_KB_ROOT/<folder>"
```

- **Never grep the KB root unscoped. Skip `archive/` and `raw/`.** Retired material lives there,
  often old indexes and logs that mention everything. Unscoped, they took grep's top hit in 14 of
  18 lookups on one KB, and skipping them doubled grep's ranking on the other.
- Search for the term the answer document would use, not the question's words; narrow with
  `--glob '<folder>/**'` when you know the folder (`ABOUT.md` says what each holds).
- Read the matching lines' section, not the whole file, and stop when it answers you.

## 8. What a semantic-less answer means

The answer's `stages.skipped` names each stage that didn't run and why (no model, a model switching,
the query not embedded, no ranker). A search with `semantic` skipped matched by words only: a miss
there doesn't mean the KB has nothing on the subject. Retry with two or three distinctive terms, or
switch to grep (§0, §7), before concluding that.
