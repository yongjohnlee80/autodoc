//go:build linux || darwin

package procenv

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// On Unix a launcher is one /bin/sh script.
func TestALauncherIsOneScriptOnUnix(t *testing.T) {
	files, err := Launcher("autodoc", []string{"/opt/auto doc", "--config", "it's"})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "autodoc" ||
		string(files[0].Text) != "#!/bin/sh\nexec '/opt/auto doc' '--config' 'it'\\''s' \"$@\"\n" {
		t.Fatalf("the launcher %q", files)
	}
}

// On Unix the PATH entry is the link, made to the folder and remade when it points elsewhere or
// nowhere; the program's own folder is not it; a link whose own path holds ':' is refused.
func TestThePathEntryIsALinkOnUnix(t *testing.T) {
	target := t.TempDir()
	program := filepath.Join(t.TempDir(), "autodoc")
	link := filepath.Join(t.TempDir(), ".bin", "x")
	for _, stale := range []string{"", t.TempDir(), filepath.Join(t.TempDir(), "gone")} {
		if stale != "" {
			_ = os.Remove(link)
			if err := os.Symlink(stale, link); err != nil {
				t.Fatal(err)
			}
		}
		err := MakePathEntry(link, target, program)
		if cur, _ := os.Readlink(link); err != nil || PathEntry(link, target, program) != link || cur != target {
			t.Fatalf("from %q: %v, the link %q; want %s to %s", stale, err, cur, link, target)
		}
	}
	odd := filepath.Join(t.TempDir(), "a:b", "x")
	if err := MakePathEntry(odd, target, program); err == nil || !strings.Contains(err.Error(), `":"`) {
		t.Fatalf("a link holding ':': %v; want it refused, naming ':'", err)
	}
}
