package agents

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/userperm"
	"github.com/yongjohnlee80/autodoc/internal/userperm/userpermtest"
)

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
