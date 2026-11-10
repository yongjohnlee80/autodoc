package tui

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/internal/usershell"
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
			ws := wsInfo{id: 1, name: "kb it's", root: root}
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
			refused := usershell.Commands(c.env).Carries("50% & more") != nil
			ws := wsInfo{id: 1, name: "kb", root: filepath.Join(t.TempDir(), "50% & more")}
			_, err := fillAgentCommand(c.env, c.call+" {root}", ws)
			if refused != (err != nil) || (err != nil && !errors.Is(err, usershell.ErrNotCarried)) {
				t.Errorf("a root holding %% and &: %v; want refused %v", err, refused)
			}
			prog, args := agentArgv(c.env, c.call)
			_, err = withCommandArgs(c.env, args, []string{`{"k":"50% & more"}`})
			if refused != (err != nil) || (err != nil && !errors.Is(err, usershell.ErrNotCarried)) {
				t.Errorf("%s: a setup argument holding %% and &: %v; want refused %v", prog, err, refused)
			}
		})
	}
}
