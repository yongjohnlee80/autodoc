// Package outline is a document's structure for navigation (ADR 0212 §6): a Markdown file's
// headings, and the breadcrumb at a place in any document. The breadcrumb is the headings above the
// place in Markdown (and in a derived document's text, which is Markdown), the key path in YAML,
// and the title in plain text and a registered chunker's file. The daemon's doc.outline and the
// editor's live outline both read it, so a saved file and an unsaved buffer are outlined alike.
//
// Read parses the text once; a Doc is for one version of the text, and is built again when the
// text changes. The package reads no file: the caller passes the bytes.
//
// Concurrency: a Doc is not changed after Read, so its methods may be called from any number of
// goroutines. Read keeps src, not a copy: the caller must not change src while the Doc is in use.
// Headings returns the Doc's own slice, which callers must not change; Crumbs returns a new slice
// on each call. Read may be called from any goroutine.
package outline
