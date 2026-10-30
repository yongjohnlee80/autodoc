// Package autodoc is AutoDoc's root: its packages are below. It holds what the repository's own
// files give the binary.
package autodoc

import _ "embed"

// AgentsGuide is AGENTS.md: how an AI agent searches and calls AutoDoc. The agent terminal hands it
// to the agent it starts (docs/decisions/crew.md#an-agent-terminal-inside-autodoc).
//
//go:embed AGENTS.md
var AgentsGuide string

// KBOperations is the KB's operations manual, AutoDoc's own
// (docs/decisions/contract.md#the-operations-manual-is-autodocs-own): a KB type's scaffold writes
// it at the KB's root, and the Neovim side reads the same file through schema.templates_dir, so
// both write one text.
//
//go:embed lua/autodoc/kb/templates/KB_OPERATIONS.md
var KBOperations []byte

// AuditPrompt is docs/audit-prompt.md: what an agent asked to audit a plugin, Lua package or KB
// type reads first (docs/decisions/plugins.md#audits-are-per-commit-and-advisory,
// docs/decisions/plugins.md#one-candidate-per-install-or-update). The registry's weekly audit
// fetches the same file at a pinned tag, so both ask the same questions.
//
//go:embed docs/audit-prompt.md
var AuditPrompt string
