package procenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

// argvHelper, set, makes this test binary print its arguments as JSON and exit: the program a
// launcher runs in these cells.
const argvHelper = "AUTODOC_PROCENV_ARGV"

func TestMain(m *testing.M) {
	if os.Getenv(argvHelper) == "1" {
		_ = json.NewEncoder(os.Stdout).Encode(os.Args[1:])
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// helperIn copies this test binary into a new folder named folder under a temporary one, and answers
// its path: a program with an awkward path, which prints the arguments it is given.
func helperIn(t *testing.T, folder string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "helper"+filepath.Ext(self))
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return dst
}

// writeLauncher writes Launcher(name, argv)'s files into a new folder and answers it.
func writeLauncher(t *testing.T, name string, argv []string) string {
	t.Helper()
	files, err := Launcher(name, argv)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.Name), f.Text, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// argvOf runs cmd as the argv helper and answers the arguments the helper was given.
func argvOf(t *testing.T, cmd *exec.Cmd) []string {
	t.Helper()
	cmd.Env = append(cmd.Env, argvHelper+"=1")
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			err = fmt.Errorf("%w: %s", err, ee.Stderr)
		}
		t.Fatalf("%s %q: %v (%s)", cmd.Path, cmd.Args, err, out)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("%s %q printed %q: %v", cmd.Path, cmd.Args, out, err)
	}
	return got
}

// A launcher needs a program to run.
func TestALauncherNeedsAProgram(t *testing.T) {
	if _, err := Launcher("autodoc", nil); err == nil {
		t.Fatal("a launcher of nothing was made")
	}
}

// The contract, on every OS: a launcher put on a PATH is found by its name as the OS finds a
// program, and runs its program with its own arguments first and then the caller's, each arriving as
// itself, the program's path being an awkward one.
func TestALauncherRunsItsProgramWithItsArgumentsFirst(t *testing.T) {
	helper := helperIn(t, awkwardFolder)
	argv := []string{helper, "--config", filepath.Join(filepath.Dir(helper), "config.toml"), "50% & ^more !x", `ends\`}
	dir := writeLauncher(t, "autodoc", argv)
	env := launcherEnv(dir)
	found, err := LookPath(env, "autodoc")
	if err != nil {
		t.Fatalf("the launcher on PATH: %v", err)
	}
	cmd := exec.Command(found, "--sub", "plain")
	cmd.Env = env
	want := append(append([]string{}, argv[1:]...), "--sub", "plain")
	if got := argvOf(t, cmd); !reflect.DeepEqual(got, want) {
		t.Errorf("found as %s, the program was given %q; want %q", found, got, want)
	}
}
