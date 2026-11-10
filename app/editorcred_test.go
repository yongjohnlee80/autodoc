package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/userperm"
)

// writePrivate writes body to a new file at dir/name, private to this user as userperm makes one.
func writePrivate(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	f, err := userperm.CreatePrivate(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// A token file holds one token: at most 256 bytes, not empty; a missing one is said to be missing.
// Its privacy is userperm's, on every OS.
func TestATokenFileHoldsOneToken(t *testing.T) {
	dir := t.TempDir()
	if tok, err := readEditorCredential(writePrivate(t, dir, "tok", "tok-private\n")); err != nil || tok != "tok-private" {
		t.Fatalf("a private token file: %q %v", tok, err)
	}
	if _, err := readEditorCredential(writePrivate(t, dir, "long", strings.Repeat("x", 257))); err == nil || !strings.Contains(err.Error(), "not a token") {
		t.Fatalf("257 bytes: %v, want refused", err)
	}
	if tok, err := readEditorCredential(writePrivate(t, dir, "full", strings.Repeat("y", 256))); err != nil || len(tok) != 256 {
		t.Fatalf("256 bytes: %d %v, want the token", len(tok), err)
	}
	if _, err := readEditorCredential(writePrivate(t, dir, "empty", " \n")); err == nil || !strings.Contains(err.Error(), "0 bytes") {
		t.Fatalf("an empty file: %v, want refused", err)
	}
	if _, err := readEditorCredential(filepath.Join(dir, "none")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a missing file: %v, want os.ErrNotExist", err)
	}
}
