---
id: "2026-07-02-block"
version: 1
status: deferred
title: |
  First line
  second line
created: "2026-07-02T00:00:00-07:00"
updated: "2026-07-02T00:00:00-07:00"
status_changed: "2026-07-02T00:00:00-07:00"
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

# First line
second line


no header, no h1, just a body
with: colons and # hashes
