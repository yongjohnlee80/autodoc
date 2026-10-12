// Package export renders a Markdown document as a standalone HTML page or as plain text, and draws
// a single Mermaid diagram as a page of its own, for an export and for a preview.
//
// An HTML page is whole: its colours are one of the shipped themes' (Themes, ThemeOf, Background),
// its Content-Security-Policy allows no network access, and an image is shown as its alt text. A
// ```mermaid block is drawn in the page by the vendored mermaid.js (core/export/mermaid); the page
// then holds that script, and its policy admits it and the one that starts it by their digests. A
// page without a diagram has no script. Options.Base makes the source's relative links file: URLs
// of the files they name, so the page's links reach them wherever it is written. Plain text drops
// frontmatter, link reference definitions and raw HTML, and keeps code blocks as written.
//
// Concurrency: every exported function may be called from any number of goroutines. Each call
// parses its own tree from the source and does not change the source; the package's shared state
// is read-only after init (the themes, the mermaid regexp) or built once under a sync.Once (the
// vendored script and its digest). The package does not read the disk; RenderWith reads the
// process's working directory when Options.Base is relative.
package export
