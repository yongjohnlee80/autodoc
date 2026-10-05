---
type: about
status: active
created: {{date}}
tags: [kb, about, prs]
abstract: "What belongs in prs/: PR records, one folder per repository, written by worktree.nvim."
---

# prs/

- **Holds:** PR records under `prs/<repo>/`, where `<repo>` is the repository's slug (`owner__name`).
- **Naming:** `pr-<number>.md`.
- **Type:** `pr`, with `repo` and `pr` (the number) required. Optional: `state` (open, merged or closed), `head`, `base`. `status`: active or closed.
- **Rules:** worktree.nvim writes these files. Edit them by hand only to add notes below its managed part.
