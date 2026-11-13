package localfs

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// LinkOldName leaves nothing by Windows' rule, and by Linux's and macOS's a relative link at the old
// name to the renamed folder beside it; a pair in two folders is refused.
func TestLinkOldNameLinksBesideItByItsOSsRule(t *testing.T) {
	dir := t.TempDir()
	old, renamed := filepath.Join(dir, "old"), filepath.Join(dir, "new")
	if err := os.Mkdir(renamed, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := linkOldName(old, renamed, true); err != nil {
		t.Fatalf("by Windows' rule: %v", err)
	}
	if _, err := os.Lstat(old); err == nil {
		t.Error("by Windows' rule a link was left")
	}
	if err := linkOldName(filepath.Join(dir, "a", "old"), renamed, false); err == nil {
		t.Error("a link across two folders was made")
	}
	if runtime.GOOS == "windows" {
		return // a symbolic link there needs a privilege the rule never asks for
	}
	if err := linkOldName(old, renamed, false); err != nil {
		t.Fatal(err)
	}
	if to, err := os.Readlink(old); err != nil || to != "new" {
		t.Errorf("the link: %q, %v; want the renamed folder's own name", to, err)
	}
}
