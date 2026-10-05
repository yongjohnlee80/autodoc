---
type: kb
kind: agents
status: active
created: {{date}}
tags: [kb, agents]
abstract: "The agent contract for this KB. Read RULES.md first, then this file, then KB_OPERATIONS.md."
---

# Agent contract

## 0. Order of authority

1. **`RULES.md`**: the user's standing rules for this KB. It comes first among the KB's own files
   and the files that point into it. It doesn't override your system or safety instructions, or
   the user's own instruction in the conversation.
2. **This file.**
3. **`KB_OPERATIONS.md`**: how to search, read and write through AutoDoc. It is versioned with
   AutoDoc; re-read it when its `revision:` changes.
4. **The folder's `ABOUT.md`**: what belongs in that folder.

## 1. Hard rules

- **`raw/` is immutable.** Read it; never edit, rename or delete anything in it.
- **Conventions are binding.** A document in `conventions/` is a rule, not a suggestion.
- **Cite, don't fabricate.** Every claim about code, a decision or an incident points at its source:
  a file and line, a commit, a document. If you can't find a source, say so.
- **Detail is load-bearing.** Don't compress away the specifics (names, numbers, paths, versions,
  the reason) that make a document useful later.
- **No secrets.** Never write credentials, tokens, passwords or private keys into the KB.

## 2. Search first

Before reading broadly or writing, search through AutoDoc
(`autodoc --call search.query '["'"$AUTODOC_WORKSPACE"'", "<words>", {"limit": 10}]'`) and read the
hits' line ranges. Extend an existing document rather than writing a near-duplicate.
`KB_OPERATIONS.md` has the filters, the listings, history and the fallback.

## 3. Frontmatter

Every document starts with YAML frontmatter valid against `_schema/frontmatter.yaml`:

- `type` picks the document type: adr, adr-rationale, convention, playbook, reference, synthesis,
  review, pr, note, about or kb.
- Every type requires `status`, `created` (YYYY-MM-DD), `tags` (a list) and `abstract` (one to
  three sentences). Each type adds its own fields; `_templates/<type>.md` has them.
- The YAML is the source of truth: no inline `**Tags:**` lines in new documents.
- Check a draft with `doc.validate` (KB_OPERATIONS.md §6).

## 4. Where things go

Each folder's `ABOUT.md` says what belongs there, the file naming and the `type`. In short:

| Folder | Holds |
|---|---|
| `adrs/` | architecture decision records |
| `conventions/` | binding rules |
| `playbooks/` | step-by-step procedures |
| `reference/` | facts: glossary, routes, source summaries |
| `synthesis/` | analyses, lessons, investigations, bug-fix records, benchmarks, incidents, design notes |
| `reviews/<reviewer>/` | code and design reviews |
| `prs/<repo>/` | PR records |
| `notes/` | in-flight work: task notes, scratch, drafts (`author:` says whose) |
| `scripts/` | the KB's helper scripts |
| `archive/` | retired material, frozen |
| `raw/` | immutable source material |

When unsure, search `"<what you're writing>" type:about`.

## 5. This KB's own rules

<!-- Add this KB's own conventions below. AutoDoc never rewrites this file once it exists. -->
