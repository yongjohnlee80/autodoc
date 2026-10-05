// Package autodoc is AutoDoc's root: its packages are below. It holds what the repository's own
// files give the binary.
package autodoc

import _ "embed"

// AgentsGuide is AGENTS.md: how an AI agent searches and calls AutoDoc. The agent terminal hands it
// to the agent it starts (ADR 1791213400).
//
//go:embed AGENTS.md
var AgentsGuide string
