//go:build windows

package usershell

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// shellsHere is every shell this runner has that runs command lines: pwsh and Windows PowerShell.
func shellsHere(t *testing.T) []Shell {
	var out []Shell
	if p, err := procenv.LookPath(nil, "pwsh"); err == nil {
		out = append(out, Shell{Program: p, kind: powershell})
	}
	return append(out, Commands(procenv.Env{"PATH=", "SystemRoot=" + os.Getenv("SystemRoot")})) // Windows PowerShell where Windows keeps it
}

// fakeProgram makes an empty name.exe in a fresh folder and answers the folder.
func fakeProgram(t *testing.T, names ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n+".exe"), nil, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// On Windows a command line is pwsh's on PATH, else Windows PowerShell's on PATH, else Windows
// PowerShell's where Windows keeps it, never cmd's; a terminal tab falls back to %COMSPEC%, else
// cmd.exe in %SystemRoot%. Each is found through the program's own PATH and PATHEXT.
func TestTheShellsOnWindows(t *testing.T) {
	both := fakeProgram(t, "pwsh", "powershell")
	only := fakeProgram(t, "powershell")
	none := t.TempDir()
	base := []string{"PATHEXT=.COM;.EXE", "SystemRoot=D:\\Win"}
	env := func(kv ...string) procenv.Env { return procenv.Env(append(append([]string(nil), base...), kv...)) }
	for _, c := range []struct {
		what string
		env  procenv.Env
		want Shell
	}{
		{"pwsh and powershell", env("PATH=" + both), Shell{filepath.Join(both, "pwsh.exe"), powershell}},
		{"powershell alone", env("PATH=" + only), Shell{filepath.Join(only, "powershell.exe"), winps}},
		{"neither", env("PATH="+none, `ComSpec=E:\cmd.exe`), Shell{`D:\Win\System32\WindowsPowerShell\v1.0\powershell.exe`, winps}},
	} {
		if got := Commands(c.env); got != c.want {
			t.Errorf("%s: Commands = %+v, want %+v", c.what, got, c.want)
		}
	}
	for _, c := range []struct {
		what string
		env  procenv.Env
		want []string
	}{
		{"pwsh", env("PATH=" + both), []string{filepath.Join(both, "pwsh.exe"), "-NoLogo"}},
		{"powershell alone", env("PATH=" + only), []string{filepath.Join(only, "powershell.exe"), "-NoLogo"}},
		{"neither, ComSpec set", env("PATH="+none, `ComSpec=E:\cmd.exe`), []string{`E:\cmd.exe`}},
		{"neither, no ComSpec", env("PATH=" + none), []string{`D:\Win\System32\cmd.exe`}},
	} {
		if got := Interactive(c.env); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: Interactive = %q, want %q", c.what, got, c.want)
		}
	}
}
