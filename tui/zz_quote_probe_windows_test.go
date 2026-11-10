//go:build windows

package tui

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
)

// scratch probe, not for merge: an agent's command through the real entry (fillAgentCommand,
// agentArgv, withCommandArgs) under Windows PowerShell 5.1 and pwsh, a printer of its own argv as
// the program, with a workspace root and name holding a space and an apostrophe.
func TestProbeAgentCommandQuoting(t *testing.T) {
	src := os.Getenv("PROBE_PRINTER")
	if src == "" {
		t.Skip("no PROBE_PRINTER")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	progDir := filepath.Join(t.TempDir(), "o'brien dir")
	if err := os.MkdirAll(progDir, 0o700); err != nil {
		t.Fatal(err)
	}
	printer := filepath.Join(progDir, "printer.exe")
	if err := os.WriteFile(printer, b, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "it's a root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	ws := wsInfo{id: 1, name: "kb it's", root: root}
	sys32 := filepath.Join(os.Getenv("SystemRoot"), "System32")
	pwsh, _ := exec.LookPath("pwsh")
	envs := []struct {
		label string
		env   procenv.Env
	}{
		{"winps", procenv.Env(nil).With("PATH=" + progDir + ";" + sys32 + ";" + filepath.Join(sys32, "WindowsPowerShell", "v1.0"))},
	}
	if pwsh != "" {
		envs = append(envs, struct {
			label string
			env   procenv.Env
		}{"pwsh", procenv.Env(nil).With("PATH=" + progDir + ";" + filepath.Dir(pwsh) + ";" + sys32)})
	}
	extra := []string{"", `a"b`, `{"k":"v w"}`, "it's", `ends\`, "50% & ^x"}
	for _, e := range envs {
		sh := usershell.Commands(e.env)
		t.Logf("PROBE %s: the shell is %s", e.label, sh.Program)
		for _, command := range []string{
			"printer {root} {workspace}",
			"& " + sh.Quote(printer) + " {root} {workspace}",
		} {
			filled := fillAgentCommand(e.env, command, ws)
			prog, args := agentArgv(e.env, filled)
			args = withCommandArgs(e.env, args, extra)
			cmd := exec.Command(prog, args...)
			cmd.Env = e.env
			out, err := cmd.CombinedOutput()
			var got []string
			want := append([]string{root, ws.name}, extra...)
			if jerr := json.Unmarshal(out, &got); jerr != nil {
				t.Errorf("PROBE %s %q: FAILED %v: %s (line %q)", e.label, command, err, out, args[len(args)-1])
				continue
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("PROBE %s %q: MANGLED got %q want %q (line %q)", e.label, command, got, want, args[len(args)-1])
			} else {
				t.Logf("PROBE %s %q: intact %q (line %q)", e.label, command, got, args[len(args)-1])
			}
		}
	}
}
