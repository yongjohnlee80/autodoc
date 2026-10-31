//go:build unix

package app

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestCrewSetupPrintsTheAgentsSetup: `autodoc --config F crew setup --kind K` prints crew.SetupFor's
// answer as JSON, AutoDoc's own files in F's data folder; the profile's environment is the one it
// runs under, read and never printed back; a missing kind, a stray argument, an unknown host, or
// an --env (a profile's variable on the command line, which any user can read) is usage (exit 2).
func TestCrewSetupPrintsTheAgentsSetup(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	data := t.TempDir()
	cfg := filepath.Join(t.TempDir(), "c.toml")
	if err := os.WriteFile(cfg, []byte("[server]\ndata_dir = \""+data+"\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	run1 := func(args ...string) (int, string, string) {
		var out, errOut bytes.Buffer
		code := run(t.Context(), append([]string{"--config", cfg}, args...), Options{Version: "v1.2.3"}, &out, &errOut)
		return code, strings.TrimSpace(out.String()), errOut.String()
	}
	t.Setenv("OPENCODE_CONFIG_CONTENT", `{"theme":"x"}`)
	t.Setenv("CLAUDE_CONFIG_DIR", "/accounts/work/.claude")
	code, out, errOut := run1("crew", "setup", "--kind", "opencode")
	if code != 0 {
		t.Fatalf("crew setup --kind opencode: exit %d, %s", code, errOut)
	}
	var s crew.Setup
	if err := json.Unmarshal([]byte(out), &s); err != nil {
		t.Fatalf("%q: %v", out, err)
	}
	if !strings.Contains(s.Env["OPENCODE_CONFIG_CONTENT"], `"theme":"x"`) ||
		!strings.Contains(s.Env["OPENCODE_CONFIG_CONTENT"], filepath.Join(data, "opencode", crew.OpenCodePluginName)) {
		t.Errorf("OPENCODE_CONFIG_CONTENT %q: want the profile's own value extended with the plugin in --config's data folder", s.Env["OPENCODE_CONFIG_CONTENT"])
	}
	if strings.Contains(out, "/accounts/work") {
		t.Errorf("the profile's environment was printed back: %s", out)
	}
	for _, bad := range [][]string{{"crew", "setup"}, {"crew", "setup", "--kind", "claude", "extra"},
		{"crew", "setup", "--kind", "claude", "--host", "emacs"}, {"crew", "setup", "--kind", "claude", "--env", "CLAUDE_CONFIG_DIR=/x"}} {
		if code, out, _ := run1(bad...); code != 2 || out != "" {
			t.Errorf("%v: exit %d, %q; want usage", bad, code, out)
		}
	}
}
