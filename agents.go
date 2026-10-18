// Package autodoc is AutoDoc's root: its packages are below. It holds what the repository's own
// files give the binary.
package autodoc

import _ "embed"

// AgentsGuide is AGENTS.md: how an AI agent searches and calls AutoDoc. The agent terminal hands it
// to the agent it starts (ADR 1791213400).
//
//go:embed AGENTS.md
var AgentsGuide string

// KBOperations is the KB's operations manual, AutoDoc's own (ADR 1792349867 §4.5): a KB type's
// scaffold writes it at the KB's root, and the Neovim side reads the same file through
// schema.templates_dir, so both write one text.
//
//go:embed lua/autodoc/kb/templates/KB_OPERATIONS.md
var KBOperations []byte
