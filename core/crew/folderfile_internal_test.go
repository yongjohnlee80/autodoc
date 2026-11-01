package crew

import (
	"errors"
	"testing"
)

// TestSetupForKeepsFolderFilesInTheAgentsFolder: a kind's folder file is written into the agent's
// own folder only: SetupFor refuses a setup whose file path is absolute or climbs out ("..")
// (ErrFolderFile), or that has no agent folder to write it in; a local path is kept (the mutant:
// the check dropped).
func TestSetupForKeepsFolderFilesInTheAgentsFolder(t *testing.T) {
	const kind = "folder-test"
	var path string
	kindSetups[kind] = func(_ SetupInput, _ string, s *Setup) error {
		s.FolderFiles = append(s.FolderFiles, FolderFile{Path: path, Content: "{}"})
		return nil
	}
	t.Cleanup(func() { delete(kindSetups, kind) })
	for _, bad := range []string{"/etc/passwd", "../outside.json", "a/../../outside.json", ""} {
		path = bad
		if _, err := SetupFor(SetupInput{Kind: kind, AgentDir: t.TempDir()}, ""); !errors.Is(err, ErrFolderFile) {
			t.Errorf("a folder file at %q: %v, want ErrFolderFile", bad, err)
		}
	}
	path = ".agents/mcp_config.json"
	if s, err := SetupFor(SetupInput{Kind: kind, AgentDir: t.TempDir()}, ""); err != nil || len(s.FolderFiles) != 1 {
		t.Errorf("a local folder file: %+v %v, want it kept", s, err)
	}
	if _, err := SetupFor(SetupInput{Kind: kind}, ""); !errors.Is(err, ErrFolderFile) {
		t.Errorf("a folder file with no agent folder: %v, want ErrFolderFile", err)
	}
}
