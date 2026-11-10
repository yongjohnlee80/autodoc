package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/userperm"
	"github.com/yongjohnlee80/autodoc/internal/userperm/userpermtest"
)

// TestTheWindowAsksTheAgentsOwnCrewSetup: crewSetupOf runs the agent's own autodoc as `crew setup
// --kind K --host window --autodoc <its wrapper> --agent-dir <its folder>` on the TUI's config, in
// the agent's folder, the profile's environment its own: no value of the profile's is on its command
// line, which any user can read. Its answer is the setup, mail_via included; output that is no setup
// is refused.
func TestTheWindowAsksTheAgentsOwnCrewSetup(t *testing.T) {
	rec, agentDir := t.TempDir(), t.TempDir()
	bin := filepath.Join(rec, "autodoc")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + shellQuote(filepath.Join(rec, "args")) + "\nenv > " + shellQuote(filepath.Join(rec, "env")) +
		"\npwd > " + shellQuote(filepath.Join(rec, "pwd")) + "\necho '{\"argv\":[],\"env\":{},\"mail_via\":\"mcp\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := crewSetupOf(bin, "/c/autodoc.toml", crew.KindClaude, agentDir, []string{"PATH=/usr/bin:/bin", "ANTHROPIC_API_KEY=s3cr3t-value"})
	if err != nil || s.MailVia != crew.MailViaMCP {
		t.Fatalf("crewSetupOf: %+v %v, want its answer, mail via MCP", s, err)
	}
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(rec, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	want := strings.Join([]string{"--config", "/c/autodoc.toml", "crew", "setup", "--kind", "claude", "--host", "window",
		"--autodoc", shellQuote(bin), "--agent-dir", agentDir, "--mail-via", "mcp"}, "\n") + "\n" // the build by its path
	if got := read("args"); got != want {
		t.Errorf("its command line:\n%s\nwant\n%s", got, want)
	}
	if strings.Contains(read("args"), "s3cr3t") || !strings.Contains(read("env"), "ANTHROPIC_API_KEY=s3cr3t-value") {
		t.Error("the profile's value is on the command line, or not in the environment")
	}
	if got := strings.TrimSpace(read("pwd")); got != agentDir {
		t.Errorf("it ran in %q, want the agent's folder %q", got, agentDir)
	}
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'something else'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := crewSetupOf(bin, "", crew.KindClaude, agentDir, nil); err == nil {
		t.Error("output that is no setup was taken")
	}
}

// TestFolderFilesStayInTheAgentsFolder: a setup's folder files are written into the agent's folder,
// 0600 (an MCP config names the session's context), and one whose path would leave it (.. or
// absolute) is refused, nothing written outside.
func TestFolderFilesStayInTheAgentsFolder(t *testing.T) {
	root := t.TempDir()
	userpermtest.ExposeNew(t, root) // a file made there the ordinary way is exposed: only a private create keeps it private
	dir := filepath.Join(root, "agent")
	if err := writeFolderFiles(dir, []crew.FolderFile{{Path: ".agents/mcp_config.json", Content: "{}"}}); err != nil {
		t.Fatal(err)
	}
	f, err := userperm.OpenPrivate(filepath.Join(dir, ".agents", "mcp_config.json")) // as this OS keeps a file private
	if err != nil {
		t.Fatalf("the folder file: %v, want it written private to this user", err)
	}
	_ = f.Close()
	for _, p := range []string{"../outside.json", filepath.Join(root, "abs.json")} {
		if err := writeFolderFiles(dir, []crew.FolderFile{{Path: p, Content: "{}"}}); !errors.Is(err, crew.ErrFolderFile) {
			t.Errorf("a folder file at %q: %v, want ErrFolderFile", p, err)
		}
	}
	for _, p := range []string{filepath.Join(root, "outside.json"), filepath.Join(root, "abs.json")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was written, outside the agent's folder", p)
		}
	}
}
