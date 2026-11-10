//go:build linux || darwin

package tui

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// Copies of the agents' test fakes agentargs_unix_test.go declares, for the host's cells.

// agentCommandCases on Unix: /bin/sh, the CLI by its quoted path.
func agentCommandCasesCopy(t *testing.T) []agentCommandCase {
	cli := printingCLI(t, "o'brien dir", "agent")
	return []agentCommandCase{{label: "sh", env: procenv.Env(nil), call: shellQuote(cli)}}
}
