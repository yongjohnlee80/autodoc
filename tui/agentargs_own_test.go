package tui

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/usershell"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

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
			if want := append([]string{root, ws.Name}, agentSetupArgs...); !reflect.DeepEqual(got, want) {
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
				if err != nil || json.Unmarshal(out, &got) != nil || !reflect.DeepEqual(got, []string{split.Name}) {
					t.Errorf("the workspace %q reached the CLI as %q (%v, %s)", split.Name, got, err, out)
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
