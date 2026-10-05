---
type: about
status: active
created: {{date}}
tags: [kb, about, reviews]
abstract: "What belongs in reviews/: code and design reviews, one folder per reviewer."
---

# reviews/

- **Holds:** code and design reviews, under `reviews/<reviewer>/`.
- **Naming:** `<YYYY-MM-DD>-<repo>-<subject>-review.md`; a later round adds `-r<N>` before `-review`.
- **Type:** `review`, with `reviewer` and `subject` required. Optional: `verdict` (approved, approved_with_notes, change_requested, rejected, commented), `round`, `repo`, `pr`, `head`, `base`, `adr`. `status`: in-progress or completed.
- **Rules:** a reviewer writes only in their own folder. Record the commit reviewed (`head`).
