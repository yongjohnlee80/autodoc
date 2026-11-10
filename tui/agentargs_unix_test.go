//go:build linux || darwin

package tui

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// agentCommandCases on Unix: /bin/sh, the CLI by its quoted path.
func agentCommandCases(t *testing.T) []agentCommandCase {
	cli := agentCLI(t, "o'brien dir", "agent")
	return []agentCommandCase{{label: "sh", env: procenv.Env(nil), call: shellQuote(cli)}}
}
