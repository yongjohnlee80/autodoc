//go:build windows

package usershell

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// shellsHere is every shell this runner has: pwsh, Windows PowerShell and cmd.exe.
func shellsHere(t *testing.T) []Shell {
	var out []Shell
	for _, name := range []string{"pwsh", "powershell"} {
		if p, err := procenv.LookPath(nil, name); err == nil {
			out = append(out, Shell{Program: p, kind: powershell})
		}
	}
	return append(out, Shell{Program: comspec(procenv.Env(os.Environ())), kind: cmd})
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

// On Windows the shell is pwsh on PATH, else powershell.exe, else %COMSPEC%, else cmd.exe in
// %SystemRoot%: found through the program's own PATH and PATHEXT.
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
		{"powershell alone", env("PATH=" + only), Shell{filepath.Join(only, "powershell.exe"), powershell}},
		{"neither, ComSpec set", env("PATH="+none, `ComSpec=E:\cmd.exe`), Shell{`E:\cmd.exe`, cmd}},
		{"neither, no ComSpec", env("PATH=" + none), Shell{`D:\Win\System32\cmd.exe`, cmd}},
	} {
		if got := Commands(c.env); got != c.want {
			t.Errorf("%s: Commands = %+v, want %+v", c.what, got, c.want)
		}
	}
	if got := Interactive(env("PATH=" + both)); !reflect.DeepEqual(got, []string{filepath.Join(both, "pwsh.exe"), "-NoLogo"}) {
		t.Errorf("Interactive = %q", got)
	}
}
