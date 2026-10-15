package docs

import _ "embed"

// ThemeReference is available on the installed binary with --theme-reference.
//
//go:embed themes.md
var ThemeReference string
