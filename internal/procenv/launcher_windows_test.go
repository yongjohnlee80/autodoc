//go:build windows

package procenv

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// awkwardFolder is a folder name with what cmd.exe reads in a batch file: a space, %, &, ^ and !
// (% around a name it would expand, set or not).
const awkwardFolder = `a b%PATH%c&d^e!f`

// launcherEnv is this process's environment with PATH dir alone, and PATHEXT Windows' own.
func launcherEnv(dir string) Env {
	return Env(nil).With("PATH="+dir, "PATHEXT=.COM;.EXE;.BAT;.CMD")
}

// On Windows the launcher is name.cmd, which a PATHEXT lookup finds, and the /bin/sh script for Git's
// shell.
func TestALauncherIsABatchFileAndAScriptOnWindows(t *testing.T) {
	files, err := Launcher("autodoc", []string{`C:\Program Files\AutoDoc\autodoc.exe`, "--config", `C:\a 50%\b\`})
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, f := range files {
		names = append(names, f.Name)
	}
	if !reflect.DeepEqual(names, []string{"autodoc", "autodoc.cmd"}) {
		t.Fatalf("the launcher's files %q", names)
	}
	batch := string(files[1].Text)
	if !strings.Contains(batch, `"C:\Program Files\AutoDoc\autodoc.exe" "--config" "C:\a 50%%\b\\" %*`) ||
		!strings.Contains(batch, "setlocal DisableDelayedExpansion") {
		t.Fatalf("the batch file %q", batch)
	}
	dir := writeLauncher(t, "autodoc", []string{`C:\x\autodoc.exe`})
	if got, err := LookPath(launcherEnv(dir), "autodoc"); err != nil || got != filepath.Join(dir, "autodoc.cmd") {
		t.Fatalf("a PATHEXT lookup of autodoc: %q, %v; want autodoc.cmd", got, err)
	}
}

// An argument a batch file can't carry as itself, a double quote or a line break, is refused (no
// Windows path holds either).
func TestALauncherRefusesAnArgumentHoldingAQuote(t *testing.T) {
	for _, arg := range []string{`say "hi"`, "two\nlines", "cr\r"} {
		if _, err := Launcher("autodoc", []string{`C:\x\autodoc.exe`, arg}); !errors.Is(err, ErrLauncherArg) {
			t.Errorf("%q: %v; want ErrLauncherArg", arg, err)
		}
	}
}

// The launcher, found on PATH, runs its program with the arguments intact through cmd.exe and
// through PowerShell (both Windows PowerShell and pwsh where it is there), and through Git's shell
// where the runner has one.
func TestALauncherRunsThroughEachWindowsShell(t *testing.T) {
	helper := helperIn(t, awkwardFolder)
	argv := []string{helper, "--config", filepath.Join(filepath.Dir(helper), "config.toml"), "50% & ^more !x"}
	dir := writeLauncher(t, "autodoc", argv)
	want := append(append([]string{}, argv[1:]...), "plain")
	env := launcherEnv(dir + string(os.PathListSeparator) + filepath.Join(os.Getenv("SystemRoot"), "System32"))
	shells := [][]string{
		{filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe"), "/d", "/c", "autodoc plain"},
		{filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-Command", "autodoc plain"},
	}
	if pwsh, err := exec.LookPath("pwsh"); err == nil {
		shells = append(shells, []string{pwsh, "-NoLogo", "-NoProfile", "-Command", "autodoc plain"})
	}
	if sh, err := exec.LookPath("sh"); err == nil {
		shells = append(shells, []string{sh, "-c", "autodoc plain"})
	}
	for _, s := range shells {
		cmd := exec.Command(s[0], s[1:]...)
		cmd.Env = env.With("PATH=" + env.Get("PATH") + string(os.PathListSeparator) + filepath.Dir(s[0]))
		if got := argvOf(t, cmd); !reflect.DeepEqual(got, want) {
			t.Errorf("through %s: the program was given %q; want %q", filepath.Base(s[0]), got, want)
		}
	}
}

// On Windows the PATH entry is the folder itself, no link made; a folder holding ';' is refused.
func TestThePathEntryIsTheFolderOnWindows(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), ".bin", "x")
	if got, err := PathEntry(link, target), MakePathEntry(link, target); err != nil || got != target {
		t.Fatalf("%q, %v; want the folder %s", got, err, target)
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("a link was made")
	}
	odd := filepath.Join(t.TempDir(), "a;b")
	if err := MakePathEntry(link, odd); err == nil || !strings.Contains(err.Error(), `";"`) {
		t.Fatalf("a folder holding ';': %v; want it refused, naming ';'", err)
	}
}
