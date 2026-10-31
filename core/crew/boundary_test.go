package crew_test

import (
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// moduleRoots are the comms module's packages (ADR 1793271500 r15 P1), each with its sub-packages:
// the registry and wakers, mail, the human inbox, and the verb framework they serve under. The
// module must be extractable for another product, so nothing in it, tests included, may import
// any other package of AutoDoc's.
var moduleRoots = []string{"core/module", "core/crew", "core/mail", "core/inbox"}

const autodocModule = "github.com/yongjohnlee80/autodoc"

// TestTheCommsModuleImportsNothingOfAutoDocOutsideIt (ADR 1793271500 r15 P1): every package the
// module's packages and their tests depend on (go list -deps -test) that is AutoDoc's lies inside
// the module. An import of core/store, app, tui, rpc or internal/* fails it, naming the package
// pulled in.
func TestTheCommsModuleImportsNothingOfAutoDocOutsideIt(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"list", "-deps", "-test", "-f", "{{.ImportPath}}"}
	for _, r := range moduleRoots {
		args = append(args, "./"+r+"/...")
	}
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		var stderr string
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("go list: %v: %s", err, stderr)
	}
	var outside []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		pkg := packageOf(line)
		rel, ok := strings.CutPrefix(pkg, autodocModule+"/")
		if pkg != autodocModule && (!ok || inModule(rel)) || slices.Contains(outside, pkg) {
			continue // not AutoDoc's (the module's root package is), or the module's own
		}
		outside = append(outside, pkg)
	}
	if len(outside) > 0 {
		t.Errorf("the comms module depends on AutoDoc outside it: %v; it may import only %v (and anything outside AutoDoc)", outside, moduleRoots)
	}
}

// packageOf is a go list -test line's package: its test variant's bracket, and the .test and _test
// suffixes of a test binary and an external test package, dropped.
func packageOf(line string) string {
	pkg, _, _ := strings.Cut(line, " ")
	pkg = strings.TrimSuffix(pkg, ".test")
	return strings.TrimSuffix(pkg, "_test")
}

func inModule(rel string) bool {
	for _, r := range moduleRoots {
		if rel == r || strings.HasPrefix(rel, r+"/") {
			return true
		}
	}
	return false
}
