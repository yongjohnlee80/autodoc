package agents

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// The agents' cells' fixtures agentargs_test.go held, which move with the agents.

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

// agentSetupArgs are arguments as an agent's setup gives them (crew.Setup's Argv): a JSON config
// with a space inside a value, a quote followed by a space, an apostrophe, a last backslash, none
// at all, and characters PowerShell would read outside a quoted word.
var agentSetupArgs = []string{`{"mcpServers":{"autodoc":{"command":"C:\\Program Files\\autodoc.exe","args":["mcp serve"]}}}`,
	`say "hi" now`, "it's", `ends\`, "", "!x (a)"}
