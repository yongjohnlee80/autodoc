package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// runMain runs this binary as autodoc with args and env, as TestMain lets a test: its exit code and
// what it wrote.
func runMain(t *testing.T, env []string, args ...string) (int, string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(append(os.Environ(), "AUTODOC_TEST_MAIN=1"), env...)
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return exit.ExitCode(), string(out)
	case err != nil:
		t.Fatal(err)
	}
	return 0, string(out)
}

// TestArguments: a workspace's name is --ui's alone, and one at most; --export's flags are its
// alone; no mode is the usage; and --version says which build this is.
func TestArguments(t *testing.T) {
	for _, c := range []struct {
		args []string
		code int
		says string
	}{
		{[]string{"--version"}, 0, "autodoc " + testOptions.Version},
		{[]string{"kb"}, 2, "unexpected arguments: [kb]"},
		{[]string{"--serve", "kb"}, 2, "only --ui, --call and --export take one"},
		{[]string{"--call", "workspace.list", "[]", "extra"}, 2, "only --ui, --call and --export take one"},
		// refused before anything is dialled: exit 1, saying why
		{[]string{"--config", "/nonexistent/autodoc.toml", "--call", "workspace.list", "{"}, 1, "--call: the parameters are not JSON"},
		{[]string{"--ui", "kb", "notes"}, 2, "unexpected arguments: [kb notes]"},
		// --export's own flags are refused without it
		{[]string{"--base", "/tmp", "--version"}, 2, "--base is for --export"},
		{[]string{"--ui", "--theme", "dark"}, 2, "--theme is for --export"},
		{[]string{"--output", "x.html"}, 2, "--output is for --export"},
		{nil, 2, "-serve"},
	} {
		code, out := runMain(t, nil, c.args...)
		if code != c.code || !strings.Contains(out, c.says) {
			t.Errorf("autodoc %v: exit %d, %q; want exit %d saying %q", c.args, code, out, c.code, c.says)
		}
	}
}

// TestServeRefusesAWorkspaceSection: --serve reads the config where XDG_CONFIG_HOME puts it, and one
// that still lists its workspaces stops it, saying where workspaces are kept now.
func TestServeRefusesAWorkspaceSection(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "autodoc"), 0o700); err != nil {
		t.Fatal(err)
	}
	conf := "[[workspace]]\nname = \"kb\"\nroot = \"" + t.TempDir() + "\"\n"
	if err := os.WriteFile(filepath.Join(home, "autodoc", "config.toml"), []byte(conf), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := runMain(t, []string{"XDG_CONFIG_HOME=" + home, "XDG_RUNTIME_DIR=" + short(t), "XDG_DATA_HOME=" + t.TempDir()}, "--serve")
	if code != 1 || !strings.Contains(out, "workspaces are kept in the store now") {
		t.Errorf("--serve over a [[workspace]] config: exit %d, %q", code, out)
	}
}

// TestMainReturnsItsExitCodes: Main returns what the process would exit with, and writes where it
// is told: the version on stdout, usage and refusals on stderr; -h is not an error.
func TestMainReturnsItsExitCodes(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "autodoc.toml")
	if err := os.WriteFile(bad, []byte("not = [toml\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		args         []string
		code         int
		stdout, says string
	}{
		{[]string{"--version"}, 0, "autodoc v1.2.3\n", ""},
		{[]string{"-h"}, 0, "", "-serve"},
		{[]string{"--no-such-flag"}, 2, "", "flag provided but not defined"},
		{nil, 2, "", "-serve"},
		// --ensure starts, and --restart replaces, the daemon --print-endpoint names: alone each is refused
		{[]string{"--ensure"}, 2, "", "--ensure and --restart go with --print-endpoint"},
		{[]string{"--restart"}, 2, "", "--ensure and --restart go with --print-endpoint"},
		// --restart reads its config before it stops anything
		{[]string{"--config", bad, "--print-endpoint", "--restart"}, 1, "", "config: invalid"},
		// a --print-endpoint that cannot read its config prints no endpoint (a client reads stdout's
		// first line as one) and says why
		{[]string{"--config", bad, "--print-endpoint"}, 1, "", "config: invalid"},
		{[]string{"--export", "html", "--output", filepath.Join(t.TempDir(), "x.html"), filepath.Join(t.TempDir(), "missing.md")}, 1, "", "autodoc:"},
	} {
		var stdout, stderr bytes.Buffer
		code := run(context.Background(), c.args, Options{Version: "v1.2.3"}, &stdout, &stderr)
		if code != c.code || stdout.String() != c.stdout || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%v: exit %d, stdout %q, stderr %q; want exit %d, stdout %q, stderr saying %q", c.args, code, stdout.String(), stderr.String(), c.code, c.stdout, c.says)
		}
	}
}

// upper is a chunker registered for an extension Main must refuse.
type upper struct{}

func (upper) Version() string                          { return "1" }
func (upper) Chunk(search.Doc) ([]search.Chunk, error) { return nil, nil }

// TestMainRefusesABadRegistration: a registration outside the rules stops every mode at entry,
// naming it, before anything is served.
func TestMainRefusesABadRegistration(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), []string{"--version"}, Options{Version: "v1", Chunkers: map[string]search.Chunker{".GO": upper{}}}, &stdout, &stderr)
	if code != 1 || stdout.Len() != 0 || !strings.Contains(stderr.String(), `".GO"`) {
		t.Fatalf("exit %d, stdout %q, stderr %q; want 1 naming .GO", code, stdout.String(), stderr.String())
	}
}

// pdfDeriver derives .pdf as autorag's does, for the capabilities' sake: nothing is derived yet.
type pdfDeriver struct{}

func (pdfDeriver) Formats() []string                { return []string{".pdf"} }
func (pdfDeriver) Describe(string) (string, string) { return "autorag/pdf", "1" }
func (pdfDeriver) Derive(context.Context, string, io.ReaderAt, int64) (Derived, error) {
	return Derived{}, errors.New("not wired")
}

// TestABuildsDeriverIsReported: Options.Deriver is validated at entry (a bad one stops every mode,
// naming its format) and its formats are in sys.capabilities with their identities.
func TestABuildsDeriverIsReported(t *testing.T) {
	var stdout, stderr bytes.Buffer
	bad := badDeriver{}
	if code := run(context.Background(), []string{"--version"}, Options{Version: "v1", Deriver: bad}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), `".pptx"`) {
		t.Fatalf("a bad deriver: exit %d, %q", code, stderr.String())
	}
	reg, err := registrations.New(nil, pdfDeriver{})
	if err != nil {
		t.Fatal(err)
	}
	dir := short(t)
	sock := filepath.Join(dir, "a.sock")
	cfg := writeConfig(t, sock, filepath.Join(dir, "state"), "")
	startAs(t, cfg, sock, build{version: "v1", reg: reg})
	got, err := rpc.RegistrationsOf(call(t, dial(t, sock), "sys.capabilities"))
	if err != nil || got.Formats[".pdf"] != (registrations.Format{ID: "autorag/pdf", Version: "1"}) {
		t.Fatalf("capabilities: %+v, %v", got, err)
	}
}

type badDeriver struct{ pdfDeriver }

func (badDeriver) Formats() []string { return []string{".pptx"} }
