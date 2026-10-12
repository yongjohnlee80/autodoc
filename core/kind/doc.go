// Package kind is how AutoDoc reads a file, decided by its extension and never by sniffing its
// content: Markdown, plain text, YAML, a registered chunker's, or a
// derived document format, read only as the text a build's deriver makes of it. The indexer, the
// document API and the editor all ask here, so they agree.
//
// An extension is compared lowercased with its dot (Ext). The order of the decision is fixed:
// ".txt" is Text and ".yaml" and ".yml" are YAML; then a derived format (DerivedExtensions, or one
// the build's deriver names) is Derived; then a registered chunker's extension is Registered; then
// a workspace's own text extension is Text; anything else is Markdown.
//
// Traits are the facts of a kind wherever it is read. A Table is a package's own behaviour per
// kind, checked complete when it is built, so a kind added here cannot be forgotten by a package
// that reads kinds. ParseMarkdown and ParseDerived are the two Markdown dialects: a workspace's,
// and a derived document's text.
//
// Concurrency: the package does no I/O, and its functions and methods are functions of their
// arguments and of package tables written only at init. Any number of goroutines may call them.
// DerivedExtensions is an exported slice that nothing in AutoDoc writes; a caller must not modify
// it. A Table is written only by NewTable, so its Of is safe for concurrent use; the entries are
// the caller's values. Registrations and Resolver read their fields on each call, so a caller
// must not change those fields while other goroutines use the value, and Resolver.Of calls
// Resolver.Text on each call: a Resolver shared between goroutines needs a Text that is safe for
// concurrent use.
package kind
