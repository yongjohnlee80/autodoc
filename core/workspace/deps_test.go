package workspace_test

import (
	"os/exec"
	"strings"
	"testing"
)

// A workspace's spec, its patterns and its defaults are this package's: it reads no daemon config,
// so config (the daemon's own settings) and the packages that serve workspaces both build on it
// without a cycle.
func TestWorkspaceImportsNoConfig(t *testing.T) {
	const mod = "github.com/yongjohnlee80/autodoc/"
	out, err := exec.Command("go", "list", "-deps", mod+"core/workspace").Output()
	if err != nil {
		t.Skipf("go list core/workspace: %v", err)
	}
	if strings.Contains(string(out), mod+"core/config\n") {
		t.Error("core/workspace imports core/config")
	}
}
