---
type: about
status: active
created: {{date}}
tags: [kb, about, schema]
abstract: "What belongs in _schema/: the KB's one frontmatter schema, managed by AutoDoc."
---

# _schema/

- **Holds:** `frontmatter.yaml`, the schema (version 2) that validates every document's frontmatter, with `type` picking the fields.
- **Rules:** AutoDoc replaces `frontmatter.yaml` when a newer AutoDoc ships a newer copy, so don't edit it. Put this KB's own rules in `AGENTS.md`.
- **Use:** the KB's AutoDoc workspace points at it (`workspace.set_schema`); `doc.validate` and `index.documents` with `missing` check documents against it.
