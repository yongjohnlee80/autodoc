//go:build linux || darwin

package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

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
} > ` + ShellQuote(report) + `
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

// {root} and {workspace} are one shell word each, whatever they hold.
func TestAnAgentsCommandIsFilledInQuoted(t *testing.T) {
	t.Parallel()
	ws := feature.Workspace{Name: "it's", Root: "/a b/$HOME"}
	got, err := fillAgentCommand(nil, "agent {root} --ws={workspace}", ws)
	if want := `agent '/a b/$HOME' --ws='it'\''s'`; err != nil || got != want {
		t.Fatalf("filled %q, %v; want %q", got, err, want)
	}
}

// A profile without a name or a command is dropped as the preference is read, and a preference
// that is not JSON reads as none.
func TestAgentProfilesReadOnlyTheWholeOnes(t *testing.T) {
	t.Parallel()
	got := AgentProfilesOf(`[{"name":"a","command":"x"},{"name":" ","command":"y"},{"name":"b","command":""},{"name":"c","command":"z"}]`)
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "c" {
		t.Fatalf("read %v, want a and c", got)
	}
	for _, bad := range []string{"not json", `[{"name":"a","command":"x"},5]`} {
		if got := AgentProfilesOf(bad); got != nil {
			t.Fatalf("read %v from %s", got, bad)
		}
	}
	if AgentProfilesJSON(nil) != "[]" {
		t.Fatalf("no profiles written as %s", AgentProfilesJSON(nil))
	}
}

// With no config named, the agent's autodoc is the build alone, on the default config.
func TestAnAgentsAutodocWithoutAConfigIsTheBuildAlone(t *testing.T) {
	t.Parallel()
	files, err := agentWrapper("/opt/auto doc", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Skip("this OS puts the build itself on the agent's PATH: no launcher to read")
	}
	if got, want := string(files[0].Text), "exec '/opt/auto doc' \"$@\"\n"; !strings.HasSuffix(got, want) || strings.Contains(got, "--config") {
		t.Fatalf("the wrapper is\n%s\nwant it to end %q, with no --config", got, want)
	}
}
