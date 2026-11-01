// Package autodoc is AutoDoc's root: its packages are below. It holds what the repository's own
// files give the binary.
package autodoc

import (
	_ "embed"
	"fmt"
	"strings"
	"sync"
)

// AgentsGuide is AGENTS.md: how an AI agent searches and calls AutoDoc. The agent terminal hands it
// to the agent it starts (docs/decisions/crew.md#an-agent-terminal-inside-autodoc).
//
//go:embed AGENTS.md
var AgentsGuide string

// WrittenAgentsGuide is AgentsGuide as an agent's guide carries it, read at the start of every
// session (token item 2): its generated verb and error tables (59 KB of it) become an index, every
// verb named under its domain, a people's-tools or internal one marked *, every error code with what
// to do next, and a line saying how to print a verb's whole contract with no daemon. The repository's
// AGENTS.md stays whole.
var WrittenAgentsGuide = sync.OnceValue(func() string { return indexedContracts(AgentsGuide) })

const (
	contractsStart = "<!-- autodoc:contracts:start -->"
	contractsEnd   = "<!-- autodoc:contracts:end -->"
	// tablesBelow is AGENTS.md's sentence saying the tables are below, which the index makes untrue
	tablesBelow = "Every verb is below with its parameters, its answer, the protocol it arrived in and who it is for."
	indexSays   = "Every verb is named below, by domain; `autodoc --contracts <verb or domain.*> --format md` prints its parameters, its answer, the protocol it arrived in and who it is for, with no daemon."
)

// indexedContracts is md with the generated tables between its contracts markers as an index: md
// as it is when it has no such block.
func indexedContracts(md string) string {
	start, end := strings.Index(md, contractsStart), strings.Index(md, contractsEnd)
	if start < 0 || end < start {
		return md
	}
	var b strings.Builder
	domain, errors := "", false
	verbs := []string{}
	flush := func() {
		if domain != "" {
			fmt.Fprintf(&b, "- `%s`: %s\n", domain, strings.Join(verbs, ", "))
		}
		domain, verbs = "", verbs[:0]
	}
	for _, line := range strings.Split(md[start+len(contractsStart):end], "\n") {
		switch {
		case strings.HasPrefix(line, "Verbs at protocol "):
			b.WriteString(line + "\n\n")
			b.WriteString("The verbs, by domain (* for people's tools or internal: call those only when the user asks):\n\n")
		case strings.HasPrefix(line, "### Errors"):
			flush()
			errors = true
			b.WriteString("\nThe errors, by code, with what to do next:\n\n")
		case strings.HasPrefix(line, "### `"):
			flush()
			domain = strings.Trim(strings.TrimPrefix(line, "### "), "`")
		case !strings.HasPrefix(line, "| ") || strings.HasPrefix(line, "| verb |") || strings.HasPrefix(line, "| code |") || strings.HasPrefix(line, "| --- |"):
		case errors:
			if c := cells(line); len(c) >= 3 {
				if c[2] != "" {
					fmt.Fprintf(&b, "- %s %s: %s\n", c[0], c[1], c[2])
				} else {
					fmt.Fprintf(&b, "- %s %s\n", c[0], c[1])
				}
			}
		default:
			if c := cells(line); len(c) >= 5 {
				_, verb, _ := strings.Cut(strings.Trim(c[0], "`"), ".")
				if strings.HasPrefix(c[4], "for people's tools") || strings.HasPrefix(c[4], "internal") {
					verb += "*"
				}
				verbs = append(verbs, verb)
			}
		}
	}
	flush()
	return strings.Replace(md[:start], tablesBelow, indexSays, 1) + b.String() + md[end+len(contractsEnd):]
}

// cells are a generated table row's cells, trimmed; a cell's escaped pipes stay escaped.
func cells(row string) []string {
	row = strings.ReplaceAll(strings.Trim(strings.TrimSpace(row), "|"), `\|`, "\x00")
	out := strings.Split(row, " | ")
	for i, c := range out {
		out[i] = strings.ReplaceAll(strings.TrimSpace(c), "\x00", `\|`)
	}
	return out
}

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
