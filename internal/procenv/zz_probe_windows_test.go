//go:build windows

package procenv

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
)

// scratch probe, not for merge: each caller argument through each route, logged, never failing.
func TestProbeCallerArgs(t *testing.T) {
	helper := helperIn(t, "probe dir")
	bound := []string{helper, "--config", filepath.Join(filepath.Dir(helper), "config.toml")}
	dir := writeLauncher(t, "autodoc", bound)
	sys32 := filepath.Join(os.Getenv("SystemRoot"), "System32")
	env := launcherEnv(dir + ";" + sys32)
	cases := [][]string{
		{"--call", `["kb","a b.md"]`},
		{`say "hi"`},
		{"50%"},
		{"%PATH%"},
		{"a&b"},
		{"a & b"},
		{"!x"},
		{`ends\`},
		{`ends\`, "next"},
		{"a^b", "a|b", "<x>"},
	}
	run := func(cmd *exec.Cmd) ([]string, string) {
		cmd.Env = append(append([]string{}, env...), argvHelper+"=1")
		out, err := cmd.CombinedOutput()
		var got []string
		if jerr := json.Unmarshal(out, &got); jerr != nil {
			return nil, strings.TrimSpace(string(out)) + " / " + func() string {
				if err != nil {
					return err.Error()
				}
				return ""
			}()
		}
		return got, ""
	}
	report := func(route string, args, got []string, fail string) {
		want := append(append([]string{}, bound[1:]...), args...)
		switch {
		case fail != "":
			t.Logf("PROBE %-10s %q -> FAILED: %.200s", route, args, fail)
		case reflect.DeepEqual(got, want):
			t.Logf("PROBE %-10s %q -> intact", route, args)
		default:
			t.Logf("PROBE %-10s %q -> MANGLED: got %q", route, args, got[min(2, len(got)):])
		}
	}
	cmdQuote := func(a string) string { // as a user types it in cmd: quoted, inner quote as \"
		return `"` + strings.ReplaceAll(a, `"`, `\"`) + `"`
	}
	pwsh, _ := exec.LookPath("pwsh")
	sh, _ := exec.LookPath("sh")
	for _, args := range cases {
		// Go's own exec of the .cmd
		got, fail := run(exec.Command(filepath.Join(dir, "autodoc.cmd"), args...))
		report("go-exec", args, got, fail)
		// cmd /c, the line as a user types it
		line := "autodoc"
		for _, a := range args {
			line += " " + cmdQuote(a)
		}
		c := exec.Command(filepath.Join(sys32, "cmd.exe"))
		c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /c "` + line + `"`}
		got, fail = run(c)
		report("cmd-typed", args, got, fail)
		// PowerShell, the arguments as an array splatted onto the command
		j, _ := json.Marshal(args)
		ps := "$a = @(ConvertFrom-Json $env:PROBE_ARGS); autodoc @a"
		for _, p := range [][2]string{{"winps", filepath.Join(sys32, "WindowsPowerShell", "v1.0", "powershell.exe")}, {"pwsh", pwsh}} {
			if p[1] == "" {
				continue
			}
			c := exec.Command(p[1], "-NoLogo", "-NoProfile", "-Command", ps)
			c.Env = []string{"PROBE_ARGS=" + string(j)}
			cc := *c
			cmd := &cc
			cmd.Env = nil
			cmd2 := exec.Command(p[1], "-NoLogo", "-NoProfile", "-Command", ps)
			env2 := append(append([]string{}, env...), "PROBE_ARGS="+string(j), argvHelper+"=1")
			cmd2.Env = env2
			out, err := cmd2.CombinedOutput()
			var g []string
			if jerr := json.Unmarshal(out, &g); jerr != nil {
				report(p[0], args, nil, strings.TrimSpace(string(out))+" / "+func() string {
					if err != nil {
						return err.Error()
					}
					return ""
				}())
			} else {
				report(p[0], args, g, "")
			}
		}
		// Git's shell: "$@" onto autodoc, which finds the sh script
		if sh != "" {
			got, fail = run(exec.Command(sh, append([]string{"-c", `autodoc "$@"`, "sh"}, args...)...))
			report("git-sh", args, got, fail)
		}
	}
}
