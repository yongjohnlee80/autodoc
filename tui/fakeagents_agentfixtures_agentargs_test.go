package tui

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
)

// Copies of the agents' test fakes agentfixtures_agentargs_test.go declares, for the host's cells.

// agentCommandCase is one way an agent's command reaches its CLI: the environment its shell is
// chosen in, and the profile command's call of the CLI.
type agentCommandCaseCopy struct {
	label string
	env   procenv.Env
	call  string
}

// printingCLI copies this test binary, as name, into a folder named with an apostrophe and a space,
// and answers its path: a CLI that prints what it was given (argvPrinterEnv).
func printingCLICopy(t *testing.T, folder, name string) string {
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
