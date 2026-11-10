package crew_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// A Claude agent's crew MCP config is named as its working folder reaches it, on every OS: its bare
// name when the agent works in its own folder, however that folder is spelled (a trailing
// separator, a "."; on Windows another case, which the disk folds as filepath.Rel does), else its
// absolute path. On Unix another case is another folder.
func TestTheMCPConfigIsNamedAsTheWorkingFolderReachesIt(t *testing.T) {
	agent := filepath.Join(t.TempDir(), "Agent")
	other := t.TempDir()
	sep := string(filepath.Separator)
	folds := filepath.Separator == '\\' // Windows' disks and filepath.Rel fold case
	caseSpelling := filepath.Join(filepath.Dir(agent), "aGENT")
	for _, c := range []struct {
		name, dir string
		bare      bool
	}{
		{"the folder itself", agent, true},
		{"with a trailing separator", agent + sep, true},
		{"with a dot", agent + sep + ".", true},
		{"another case", caseSpelling, folds},
		{"another folder", other, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			s, err := crew.SetupFor(crew.SetupInput{Kind: crew.KindClaude, Host: crew.HostWindow, Env: map[string]string{"PATH": ""},
				DataDir: t.TempDir(), Dir: c.dir, AgentDir: agent, Servers: []crew.MCPServer{{Name: crew.MCPServerName,
					Command: []string{filepath.Join(agent, "autodoc"), "crew", "mcp"}, Tools: crew.MCPToolNames()}}}, "")
			if err != nil {
				t.Fatal(err)
			}
			i := slices.Index(s.Argv, "--mcp-config")
			if i < 0 || i+1 >= len(s.Argv) {
				t.Fatalf("argv %q: no --mcp-config", s.Argv)
			}
			want := filepath.Join(agent, crew.ClaudeMCPConfig)
			if c.bare {
				want = crew.ClaudeMCPConfig
			}
			if got := s.Argv[i+1]; got != want {
				t.Errorf("--mcp-config %q, want %q", got, want)
			}
			if c.bare && strings.ContainsRune(s.Argv[i+1], filepath.Separator) {
				t.Errorf("the bare name carries a folder: %q", s.Argv[i+1])
			}
		})
	}
}
