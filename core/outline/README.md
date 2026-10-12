# core/outline

A document's structure for navigation: a Markdown file's headings, and the breadcrumb
at a place in any document. The daemon's `doc.outline` and the editor's live outline both read it,
so a saved file and an unsaved buffer are outlined alike. The caller passes the bytes; the package
reads no file.

| Name | What |
| --- | --- |
| `Read(src, k, title)` | a `*Doc` of `src` read as kind `k`; `title` (the file's name) is the plain-text breadcrumb. One per version of the text |
| `Doc` | a document read for navigation |
| `(*Doc).Headings()` | the headings in source order; none outside Markdown and a derived document's text |
| `(*Doc).Crumbs(line, col)` | the breadcrumb at a 1-based line and a byte column: the headings above it, outermost first (Markdown), the key path (YAML), or the title (plain text, a registered chunker's file) |
| `Heading` | `ID` (its GitHub-style slug, numbered when repeated, stable within one version), `Level` (1 to 6), `Text`, `Line` (1-based) and `Byte` (offset) |

A `Doc` is not changed after `Read`, so it is safe for concurrent use. `Read` keeps `src` without
copying it, so the caller must not change `src` while the `Doc` is in use; `Headings` returns the
`Doc`'s own slice, which must not be changed either.
