---
type: about
status: active
created: {{date}}
tags: [kb, about, reference]
abstract: "What belongs in reference/: facts such as glossary entries, routes, source summaries and reference pages."
---

# reference/

- **Holds:** facts that don't argue: glossary entries, API routes, summaries of a source, reference pages.
- **Naming:** `<kebab-subject>.md`.
- **Type:** `reference`, with `kind`: glossary, route, source or reference. `status`: active or stale. A source summary records `source_sha` and `ingested_at` when it has them.
- **Rules:** cite where each fact comes from. Mark a page `stale` rather than leaving it wrong.
