---
id: "2026-05-29-full-task"
version: 1
status: completed
title: "Fix: the parser's #1 bug"
due: "2026-06-19"
priority: high
assignee: "agent:jarvis"
tags:
  - "repo:autodoc"
  - plain-tag
  - "123"
  - "true"
adr:
  - $KB_ROOT/adrs/0031-foo.md
  - "~/notes/a b.md"
review:
  - $WORKSPACE/reviews/r1.md
blocked:
  - 2026-05-01-other
created: "2026-05-29T03:57:06-07:00"
updated: "2026-05-29T04:00:00-07:00"
status_changed: "2026-05-30T10:00:00-07:00"
completed_at: "2026-05-30T10:00:00-07:00"
errors:
  - code: not-found
    detected: "2026-09-26T11:19:24+09:00"
    field: adr[1]
    message: "path '~/notes/a b.md' does not exist (resolved: /home/x/notes/a b.md)"
  - code: unresolved-variable
    detected: "2026-09-26T11:19:24+09:00"
    field: review[0]
    message: "variable '$WORKSPACE' is not defined on this machine — set it in the Vars section or as an environment variable"
---

<!-- ─── auto-core.todo schema v1 — managed file ───
     HAND-EDIT FREELY in this body (the description) or the
       hand-editable frontmatter fields: title, status, due,
       priority, assignee, tags, adr, review, blocked.
       Direct `status` edits to a valid enum value are honored —
       refresh reconciles the file's directory. BUT side effects
       (mailbox notifications, assignee fan-out) only fire when
       status changes go through the API (`todo.status` /
       `todo.assign`). Prefer the API when other agents need
       to know.
     DO NOT HAND-EDIT frontmatter: id, version, created, updated,
       status_changed, completed_at, archived_at, errors.
         • id + created are frozen at creation.
         • version / updated / lifecycle timestamps / errors are
           written by refresh + API calls. Manual edits silently
           break archive timing and cross-references.
     ─────────────────────────────────────────────── -->

# Fix: the parser's #1 bug

Body with a --- line:
---
and unicode: 한국어 — ok.
