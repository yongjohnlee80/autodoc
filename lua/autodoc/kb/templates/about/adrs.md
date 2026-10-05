---
type: about
status: active
created: {{date}}
tags: [kb, about, adrs]
abstract: "What belongs in adrs/: architecture decision records, one per decision, with their rationale siblings."
---

# adrs/

- **Holds:** architecture decision records. A long rationale goes in a sibling `<id>-<slug>-rationale.md` (type `adr-rationale`, `adr:` naming the ADR).
- **Naming:** `<id>-<kebab-title>.md`. New ADRs take a Unix-time id (`date +%s`); existing numbers stay.
- **Type:** `adr`, with `number` (the id, as a string) required. `status`: proposed, accepted, rejected, superseded or deprecated.
- **Rules:** an accepted ADR changes by a new revision (`revision:`, with what changed) or by a new ADR that `supersedes` or `amends` it. Never rewrite history silently.
