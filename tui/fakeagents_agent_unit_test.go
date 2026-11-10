//go:build linux || darwin

package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/agents"
)

// Copies of the agents' test fakes agent_unit_test.go declares, for the host's cells.

// fakeAgent is an agent CLI that writes where it started, with what, what it was told, and which
// autodoc it calls, to report (the screen wraps long paths), calls it, then echoes what it is sent
// until told to exit.
func fakeAgent(t *testing.T) (agent, report string) {
	t.Helper()
	dir := t.TempDir()
	p, report := filepath.Join(dir, "fake-agent"), filepath.Join(dir, "report")
	script := `#!/bin/sh
{
echo "cwd=$(pwd)"
echo "args=$*"
echo "env=$AUTODOC_WORKSPACE|$AUTODOC_ROOT|$FAKE_GREETING"
LC_ALL=C ls
echo "autodoc=$(command -v autodoc)"
} > ` + agents.ShellQuote(report) + `
autodoc --call workspace.list
echo "started $1"
while read line; do
  case "$line" in
    exit*) exit ${line#exit } ;;
  esac
  echo "heard-$line"
done
`
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p, report
}
