package procenv

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
)

// Get reads the last setting of a name, as a process does; With sets and removes; nil is the
// process's own.
func TestAnEnvReadsItsOwnVariables(t *testing.T) {
	e := Env{"A=1", "B=2", "A=3", "C="}
	if got := e.Get("A"); got != "3" {
		t.Errorf("Get(A) = %q, want the later setting, 3", got)
	}
	if got := e.Get("D"); got != "" {
		t.Errorf("Get(D) = %q, want none", got)
	}
	w := e.With("B=4", "C", "E=5")
	if got := w.Get("B") + "," + w.Get("E"); got != "4,5" {
		t.Errorf("With: B,E = %q, want 4,5", got)
	}
	if slices.ContainsFunc(w, func(kv string) bool { return kv == "C=" }) {
		t.Errorf("With(C) kept C: %q", w)
	}
	if e.Get("B") != "2" {
		t.Error("With changed the Env it was given")
	}
	if v, set := e.Lookup("C"); !set || v != "" {
		t.Errorf("Lookup(C) = %q, %v; want set, empty", v, set)
	}
	if _, set := e.Lookup("D"); set {
		t.Error("Lookup(D) says set; it is not")
	}
	env := e.Environ()
	env[0] = "Z=9"
	if e[0] != "A=1" {
		t.Error("Environ's result is the Env's own slice")
	}
	if got, want := Env(nil).Get("PATH"), os.Getenv("PATH"); got != want {
		t.Errorf("nil Get(PATH) = %q, the process's is %q", got, want)
	}
	if !slices.Equal(Env(nil).Environ(), os.Environ()) {
		t.Error("nil Environ is not os.Environ")
	}
	if got := Env(nil).With("AUTODOC_PROCENV_CELL=1"); got.Get("PATH") != os.Getenv("PATH") || got.Get("AUTODOC_PROCENV_CELL") != "1" {
		t.Error("With on nil does not start from the process's environment")
	}
}

// The contract, on every OS: a program is found on the Env's PATH, never the process's; a file
// that may not be run is not; a match through a relative entry comes with exec.ErrDot; a path is
// taken as it is; nil is exec.LookPath.
func TestAProgramIsFoundOnTheEnvsPATH(t *testing.T) {
	bin, other, plain := t.TempDir(), t.TempDir(), t.TempDir()
	prog := writeProgram(t, bin, "autodoc-procenv-cell", true)
	writeProgram(t, plain, "autodoc-procenv-cell", false)
	e := Env{"PATH=" + other + string(os.PathListSeparator) + plain + string(os.PathListSeparator) + bin}
	if got, err := LookPath(e, "autodoc-procenv-cell"); err != nil || got != prog {
		t.Errorf("LookPath = %q, %v; want %q, past the entry where it may not be run", got, err, prog)
	}
	if got := Resolve(e, "autodoc-procenv-cell"); got != prog {
		t.Errorf("Resolve = %q, want %q", got, prog)
	}
	if _, err := LookPath(Env{"PATH=" + other}, "autodoc-procenv-cell"); !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("LookPath on a PATH without it: %v, want exec.ErrNotFound", err)
	}
	if got := Resolve(Env{"PATH=" + other}, "autodoc-procenv-cell"); got != "autodoc-procenv-cell" {
		t.Errorf("Resolve of a program not found = %q, want the name itself", got)
	}
	if _, err := LookPath(nil, "autodoc-procenv-cell"); err == nil {
		t.Error("nil found the program on the Env's PATH: want the process's, which hasn't it")
	}
	if got, err := LookPath(e, prog); err != nil || got != prog {
		t.Errorf("LookPath of a path = %q, %v; want it as it is", got, err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	rel, err := filepath.Rel(wd, bin)
	if err != nil {
		t.Skip("the temporary folder has no path relative to this one")
	}
	if _, err := LookPath(Env{"PATH=" + rel}, "autodoc-procenv-cell"); !errors.Is(err, exec.ErrDot) {
		t.Errorf("LookPath through a relative entry: %v, want exec.ErrDot", err)
	}
}
