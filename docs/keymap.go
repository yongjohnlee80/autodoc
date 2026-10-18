package docs

import _ "embed"

// KeymapReference is available on the installed binary with --keymap-reference.
//
//go:embed keymap.md
var KeymapReference string
