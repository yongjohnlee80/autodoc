package pluginproto_test

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	autodocModule = "github.com/yongjohnlee80/autodoc"
	pluginSDK     = autodocModule + "/plugin"
)

// goList is `go list args…` run at the module's root, its lines.
func goList(t *testing.T, args ...string) []string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", append([]string{"list"}, args...)...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list %v: %v: %s", args, err, stderr)
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// isSDK reports whether an import path is the plugin SDK or a package under it.
func isSDK(path string) bool { return path == pluginSDK || strings.HasPrefix(path, pluginSDK+"/") }

// TestCoreDependsOnNoPluginSDK: nothing core's packages or their tests depend on (go list -deps
// -test ./core/...) imports the plugin SDK. Core is what the SDK and the host stand on, never the
// other way: the protocol's versions, which core/manifests checks a manifest against, are this
// package's. A failure names each package that imports the SDK, and what of it.
func TestCoreDependsOnNoPluginSDK(t *testing.T) {
	var found []string
	for _, line := range goList(t, "-deps", "-test", "-f", "{{.ImportPath}}|{{join .Imports \" \"}}", "./core/...") {
		pkg, imports, _ := strings.Cut(line, "|")
		if pkg, _, _ = strings.Cut(pkg, " "); isSDK(pkg) { // a test variant's " [x.test]" dropped
			continue // the SDK's own imports are its business: what matters is who brought it in
		}
		for _, imp := range strings.Fields(imports) {
			if isSDK(imp) {
				if f := pkg + " imports " + imp; !slices.Contains(found, f) {
					found = append(found, f)
				}
			}
		}
	}
	if len(found) > 0 {
		t.Errorf("core depends on the plugin SDK: %s; the protocol's versions are core/pluginproto's", strings.Join(found, "; "))
	}
}

// TestPluginprotoImportsNothing: the package's code imports nothing, so the SDK, which imports it,
// carries no more of AutoDoc into a plugin than its constants.
func TestPluginprotoImportsNothing(t *testing.T) {
	if imports := goList(t, "-f", "{{join .Imports \" \"}}", "./core/pluginproto"); len(imports) != 1 || imports[0] != "" {
		t.Errorf("core/pluginproto imports %q; want nothing", imports)
	}
}
