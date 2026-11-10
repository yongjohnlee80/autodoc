//go:build windows

package procenv

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// On Windows a launcher is no file: the build is on the PATH itself.
func TestALauncherIsNoFileOnWindows(t *testing.T) {
	files, err := Launcher("autodoc", []string{`C:\x\autodoc.exe`, "--config", `C:\c.toml`})
	if err != nil || len(files) != 0 {
		t.Fatalf("the launcher %q, %v; want none", files, err)
	}
}

// On Windows the PATH entry is the program's own folder, no link made; a folder holding ';' is
// refused.
func TestThePathEntryIsTheBuildsFolderOnWindows(t *testing.T) {
	program := filepath.Join(t.TempDir(), "autodoc.exe")
	link := filepath.Join(t.TempDir(), ".bin", "x")
	if got, err := PathEntry(link, t.TempDir(), program), MakePathEntry(link, t.TempDir(), program); err != nil || got != filepath.Dir(program) {
		t.Fatalf("%q, %v; want the build's folder %s", got, err, filepath.Dir(program))
	}
	if _, err := os.Lstat(link); err == nil {
		t.Fatal("a link was made")
	}
	odd := filepath.Join(t.TempDir(), "a;b", "autodoc.exe")
	if err := MakePathEntry(link, t.TempDir(), odd); err == nil || !strings.Contains(err.Error(), `";"`) {
		t.Fatalf("a build folder holding ';': %v; want it refused, naming ';'", err)
	}
}

// buildAs copies this test binary into a new folder (a space and an apostrophe in its name) as
// autodoc.exe, and answers its path: the build an agent's PATH leads to.
func buildAs(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "o'brien build")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "autodoc.exe")
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

// An agent whose PATH starts with the build's folder (PathEntry), its config in $AUTODOC_CONFIG,
// runs autodoc --call with a JSON argument from cmd.exe, Windows PowerShell, pwsh and Go's own
// exec: each finds the build as autodoc, its arguments arrive as themselves, and its config is the
// window's. (Windows PowerShell 5.1 strips an embedded quote from any program's argument on its
// own; that is 5.1's, the same with no AutoDoc in the way, and its case is checked for the words it
// keeps.)
func TestAnAgentsAutodocIsTheBuildWithItsConfigOnWindows(t *testing.T) {
	build := buildAs(t)
	config := filepath.Join(t.TempDir(), "window's config.toml")
	sys32 := filepath.Join(os.Getenv("SystemRoot"), "System32")
	path := PathEntry("", "", build) + string(os.PathListSeparator) + sys32 + string(os.PathListSeparator) + filepath.Join(sys32, "WindowsPowerShell", "v1.0")
	env := Env(nil).With("PATH="+path, "PATHEXT=.COM;.EXE;.BAT;.CMD", "AUTODOC_CONFIG="+config, argvHelper+"=1")
	call := []string{"--call", "search.query", `["kb","a b.md"]`}
	type run struct {
		name string
		cmd  *exec.Cmd
		ps51 bool
	}
	found, err := LookPath(env, "autodoc") // as Go's exec finds a program on the agent's PATH
	if err != nil || found != build {
		t.Fatalf("autodoc on the agent's PATH: %q, %v; want the build %s", found, err, build)
	}
	runs := []run{
		{name: "go exec", cmd: exec.Command(found, call...)},
		{name: "powershell", cmd: exec.Command(filepath.Join(sys32, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoLogo", "-NoProfile", "-Command",
			`autodoc --call search.query '["kb","a b.md"]'`), ps51: true},
	}
	cmdExe := exec.Command(filepath.Join(sys32, "cmd.exe"))
	cmdExe.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "autodoc --call search.query "[\"kb\",\"a b.md\"]""`}
	runs = append(runs, run{name: "cmd", cmd: cmdExe})
	if pwsh, err := exec.LookPath("pwsh"); err == nil {
		runs = append(runs, run{name: "pwsh", cmd: exec.Command(pwsh, "-NoLogo", "-NoProfile", "-Command",
			`autodoc --call search.query '["kb","a b.md"]'`)}) // its own default passing: an .exe's arguments as themselves
	}
	for _, r := range runs {
		r.cmd.Env = env
		out, err := r.cmd.Output()
		if err != nil {
			t.Errorf("%s: %v (%s)", r.name, err, out)
			continue
		}
		var got struct {
			Args   []string
			Config string
		}
		if err := json.Unmarshal(out, &got); err != nil {
			t.Errorf("%s printed %q", r.name, out)
			continue
		}
		want := call
		if r.ps51 {
			want = []string{"--call", "search.query", "[kb,a b.md]"} // 5.1's own stripping, with any program
		}
		if !reflect.DeepEqual(got.Args, want) || got.Config != config {
			t.Errorf("%s: autodoc was given %q with config %q; want %q and %q", r.name, got.Args, got.Config, want, config)
		}
	}
}
