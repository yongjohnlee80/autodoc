package docs

import "embed"

// Tutorials are the short tutorials (tutorials/NN-name.md, README.md their index and no page):
// AutoDoc's Help › Tutorial shows them in their files' order, and the docs site publishes the same
// files.
//
//go:embed tutorials/[0-9]*.md
var Tutorials embed.FS
