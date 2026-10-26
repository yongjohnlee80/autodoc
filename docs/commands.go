package docs

import _ "embed"

// CommandsReference is available on the installed binary with --commands-reference.
//
//go:embed commands.md
var CommandsReference string
