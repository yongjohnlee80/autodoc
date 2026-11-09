package app

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/kbtype"
	"github.com/yongjohnlee80/autodoc/internal/ipc"
)

// The daemon this build serves brings a KB's older KB_OPERATIONS.md up to the build's own copy,
// stamped with the managed version (version.lua's), when it starts serving the KB; a copy of a newer
// version is kept, never downgraded.
func TestTheDaemonRefreshesAKBsOlderOperationsToItsOwn(t *testing.T) {
	dir := short(t)
	state, sock := filepath.Join(dir, "state"), ipc.ScratchAddr(dir, "s.sock")
	cfg := writeConfig(t, sock, state, "")
	start(t, cfg, sock)
	cli := dial(t, sock)
	ops := func(v string) []byte {
		return []byte("---\ntype: kb\nautodoc_version: " + v + "\n---\nops " + v + "\n")
	}
	older, newer := t.TempDir(), t.TempDir()
	for root, b := range map[string][]byte{older: ops("0.3.6"), newer: ops("99.0.0")} {
		if err := os.WriteFile(filepath.Join(root, kbtype.Operations), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	call(t, cli, "workspace.add", "older", older)
	call(t, cli, "workspace.add", "newer", newer)
	got, err := os.ReadFile(filepath.Join(older, kbtype.Operations))
	if err != nil {
		t.Fatal(err)
	}
	if v := kbtype.DeclaredVersion(got); v != autodoc.KBManagedVersion() {
		t.Errorf("the older KB's copy declares %q after the daemon served it, want this build's managed version %s", v, autodoc.KBManagedVersion())
	}
	// the build's own copy, as --kb-operations renders it, but for the day it was written on
	created := regexp.MustCompile(`\ncreated: [0-9-]+\n`)
	if a, b := created.ReplaceAll(got, nil), created.ReplaceAll(managedOperations(), nil); string(a) != string(b) {
		t.Errorf("the older KB's copy is not this build's operations")
	}
	if b, _ := os.ReadFile(filepath.Join(newer, kbtype.Operations)); string(b) != string(ops("99.0.0")) {
		t.Errorf("the newer KB's copy was rewritten: %q", b)
	}
}
