package tui

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// agentSetupArgs are arguments as an agent's setup gives them (crew.Setup's Argv): a JSON config
// with a space inside a value, a quote followed by a space, an apostrophe, a last backslash, none
// at all, and characters PowerShell would read outside a quoted word.
var agentSetupArgs = []string{`{"mcpServers":{"autodoc":{"command":"C:\\Program Files\\autodoc.exe","args":["mcp serve"]}}}`,
	`say "hi" now`, "it's", `ends\`, "", "!x (a)"}

// agentCommandCase is one way an agent's command reaches its CLI: the environment its shell is
// chosen in, and the profile command's call of the CLI.
type agentCommandCase struct {
	label string
	env   procenv.Env
	call  string
}

// printingCLI copies this test binary, as name, into a folder named with an apostrophe and a space,
// and answers its path: a CLI that prints what it was given (argvPrinterEnv).
func printingCLI(t *testing.T, folder, name string) string {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), folder)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	in, err := os.Open(self)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o700)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// An agent's command goes from its profile to its CLI through fillAgentCommand, agentArgv and
// withCommandArgs: the workspace's root and name, and its setup's arguments, reach the CLI each as
// itself, through each shell and kind of CLI this OS has (agentCommandCases), however the CLI's
// path, the root and the arguments are spelled.
func TestAnAgentsCommandReachesItsCLIWhole(t *testing.T) {
	for _, c := range agentCommandCases(t) {
		t.Run(c.label, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "it's a root")
			ws := feature.Workspace{ID: 1, Name: `say "hi" now it's`, Root: root} // quotes, but no space after an odd one
			filled, err := fillAgentCommand(c.env, c.call+" {root} {workspace}", ws)
			if err != nil {
				t.Fatal(err)
			}
			prog, args := agentArgv(c.env, filled)
			if args, err = withCommandArgs(c.env, args, agentSetupArgs); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(prog, args...)
			cmd.Env = c.env.With(argvPrinterEnv + "=1")
			out, err := cmd.Output()
			if err != nil {
				t.Fatalf("%s %q: %v (%s)", prog, args, err, out)
			}
			var got []string
			if err := json.Unmarshal(out, &got); err != nil {
				t.Fatalf("%s %q printed %q", prog, args, out)
			}
			if want := append([]string{root, ws.name}, agentSetupArgs...); !reflect.DeepEqual(got, want) {
				t.Errorf("the CLI was given %q\nwant %q\n(the line %q)", got, want, args[len(args)-1])
			}
		})
	}
}

// A value an agent's command can't carry as itself on this OS's shell (usershell.Shell.Carries: on
// Windows what cmd.exe reads again in a batch file's %*) refuses the command instead of changing
// it: a {root} holding one fails the fill, a setup's argument holding one fails the arguments. On
// Unix both reach the CLI.
func TestAnAgentsCommandRefusesAValueItCannotCarry(t *testing.T) {
	for _, c := range agentCommandCases(t) {
		t.Run(c.label, func(t *testing.T) {
			refused := runtime.GOOS == "windows" // stated here, not asked of Carries, so a Carries that refuses nothing fails
			ws := feature.Workspace{ID: 1, Name: "kb", Root: filepath.Join(t.TempDir(), "50% & more")}
			_, err := fillAgentCommand(c.env, c.call+" {root}", ws)
			if refused != (err != nil) || (err != nil && !errors.Is(err, usershell.ErrNotCarried)) {
				t.Errorf("a root holding %% and &: %v; want refused %v", err, refused)
			}
			// a workspace name with a space after a double quote: Windows PowerShell 5.1 would split
			// the quoted {workspace}, so there it is refused; every other shell hands it on whole
			split := feature.Workspace{ID: 1, Name: `a"b c`, Root: t.TempDir()}
			filled, err := fillAgentCommand(c.env, c.call+" {workspace}", split)
			if winps := strings.HasPrefix(c.label, "winps"); winps != (err != nil) || (err != nil && !errors.Is(err, usershell.ErrNotCarried)) {
				t.Errorf("the workspace %q: %v; want refused %v", split.Name, err, winps)
			} else if err == nil {
				prog, args := agentArgv(c.env, filled)
				cmd := exec.Command(prog, args...)
				cmd.Env = c.env.With(argvPrinterEnv + "=1")
				out, err := cmd.Output()
				var got []string
				if err != nil || json.Unmarshal(out, &got) != nil || !reflect.DeepEqual(got, []string{split.name}) {
					t.Errorf("the workspace %q reached the CLI as %q (%v, %s)", split.name, got, err, out)
				}
			}
			// a setup's argument naming a path with a variable's name in it, the variable set, and one
			// across two lines: each is refused before anything runs, not expanded or cut at the break
			env := c.env.With("AUTODOC_CELL_VAR=expanded")
			prog, args := agentArgv(env, c.call)
			for _, arg := range []string{
				`{"mcpServers":{"autodoc":{"command":"C:\\tools\\%AUTODOC_CELL_VAR%\\autodoc.exe"}}}`,
				"two\nlines",
				`{"k":"50% & more"}`,
			} {
				_, err = withCommandArgs(env, args, []string{"--settings", arg})
				if refused != (err != nil) || (err != nil && !errors.Is(err, usershell.ErrNotCarried)) {
					t.Errorf("%s: the setup argument %q: %v; want refused %v", prog, arg, err, refused)
				}
			}
			if !refused { // where nothing is refused, each reaches the CLI as itself
				carried := []string{`{"command":"C:\\tools\\%AUTODOC_CELL_VAR%\\autodoc.exe"}`, "two\nlines"}
				full, err := withCommandArgs(env, args, carried)
				if err != nil {
					t.Fatal(err)
				}
				cmd := exec.Command(prog, full...)
				cmd.Env = env.With(argvPrinterEnv + "=1")
				out, err := cmd.Output()
				var got []string
				if err != nil || json.Unmarshal(out, &got) != nil || !reflect.DeepEqual(got, carried) {
					t.Errorf("the CLI was given %q (%v, %s); want %q", got, err, out, carried)
				}
			}
		})
	}
}

// A command refused before it runs (a {workspace} its shell can't carry, a setup's argument it
// can't carry) is never spawned: the start is refused in its tab, nothing registers for a fill's
// refusal, and the tab's starter answers the refusal without calling spawn. A NUL, which no
// shell can carry, makes the case on every OS.
func TestARefusedAgentCommandIsNeverSpawned(t *testing.T) {
	t.Run("a workspace the command can't carry", func(t *testing.T) {
		a, c, w := crewAgents(t, `[{"name":"One","command":"one {workspace}","codename":"one"}]`)
		c.ws.name = "kb\x00x"
		p, _ := a.profile("one")
		instance, err := a.startInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		if instance.refusal == nil || !errors.Is(instance.refusal, usershell.ErrNotCarried) || calledTimes(w, "crew.register") != 0 {
			t.Fatalf("refusal %v, registered %d; want refused before anything registers", instance.refusal, calledTimes(w, "crew.register"))
		}
		if _, spawned, err := startedCmd(a, c, instance); spawned || !errors.Is(err, usershell.ErrNotCarried) {
			t.Fatalf("its tab's start: spawned %v, %v; want the refusal and no spawn", spawned, err)
		}
	})
	t.Run("a setup's argument the command can't carry", func(t *testing.T) {
		a, c, _ := crewAgents(t, crewKinds)
		setup := a.setupRun
		a.setupRun = func(kind, dir string, environ []string) (crew.Setup, error) {
			s, err := setup(kind, dir, environ)
			s.Argv = append(s.Argv, "--settings", "x\x00y")
			return s, err
		}
		p, _ := a.profile("claude-one")
		instance, err := a.startInstance(p)
		if err != nil {
			t.Fatal(err)
		}
		awaitStart(t, c, instance)
		if _, spawned, err := startedCmd(a, c, instance); spawned || !errors.Is(err, usershell.ErrNotCarried) {
			t.Fatalf("its tab's start: spawned %v, %v; want the setup's refusal and no spawn", spawned, err)
		}
	})
}
