package localfs

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReadDirIfAnyAnswersAlikeOnEveryOS: nothing there is no entries and no error; a folder is its
// entries; a file is an error wrapping ErrNotAFolder, on Windows too, where reading a file as a
// folder reports "not found".
func TestReadDirIfAnyAnswersAlikeOnEveryOS(t *testing.T) {
	dir := t.TempDir()
	if es, err := ReadDirIfAny(filepath.Join(dir, "absent")); es != nil || err != nil {
		t.Errorf("nothing there: %v, %v; want no entries and no error", es, err)
	}
	file := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(file, []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if es, err := ReadDirIfAny(dir); err != nil || len(es) != 1 || es[0].Name() != "a.txt" {
		t.Errorf("a folder: %v, %v; want its one entry", es, err)
	}
	if es, err := ReadDirIfAny(file); es != nil || !errors.Is(err, ErrNotAFolder) {
		t.Errorf("a file: %v, %v; want an error wrapping ErrNotAFolder", es, err)
	}
}
