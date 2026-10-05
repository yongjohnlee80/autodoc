---
type: about
status: active
created: {{date}}
tags: [kb, about, templates]
abstract: "What belongs in _templates/: one starting document per type, generated from the schema."
---

# _templates/

- **Holds:** `<type>.md` for each type in `_schema/frontmatter.yaml`, each with the type's required fields and its optional ones as comments.
- **Naming:** `<type>.md`.
- **Use:** copy a template to start a document so its frontmatter starts valid. `{{date}}` and `{{title}}` are Obsidian template variables.
- **Rules:** AutoDoc creates missing templates and never overwrites one that exists.
