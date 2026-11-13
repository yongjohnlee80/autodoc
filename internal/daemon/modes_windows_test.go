//go:build windows

package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// shortName is dir by its 8.3 names, as the disk gives them; ok is false where the volume keeps
// none (dir comes back as it is).
func shortName(t *testing.T, dir string) (short string, ok bool) {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]uint16, 1024)
	n, err := syscall.GetShortPathName(p, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 || int(n) > len(buf) {
		t.Logf("no short name for %s: %v", dir, err)
		return "", false
	}
	short = syscall.UTF16ToString(buf[:n])
	return short, short != dir
}

// TestAWorkspaceConfiguredByAnotherSpellingFindsItsStoreOnWindows: Windows gives one folder several
// spellings with no link involved: a drive letter in either case, and 8.3 short names. A workspace
// configured by one of them is asked where its root leads as it starts, and finds the store and the
// variables the todo registry names by the disk's own spelling.
func TestAWorkspaceConfiguredByAnotherSpellingFindsItsStoreOnWindows(t *testing.T) {
	spellings := map[string]func(t *testing.T, real string) (string, bool){
		"the drive letter in the other case": func(t *testing.T, real string) (string, bool) {
			if len(real) < 2 || real[1] != ':' {
				return "", false
			}
			alias := strings.ToLower(real[:1]) + real[1:]
			if alias == real {
				alias = strings.ToUpper(real[:1]) + real[1:]
			}
			return alias, alias != real
		},
		"its 8.3 short names": shortName,
	}
	for name, spell := range spellings {
		t.Run(name, func(t *testing.T) {
			m, _ := open(t)
			real := realTemp(t)
			// a folder whose long name has a short one
			real = filepath.Join(real, "a workspace with a long name")
			if err := os.Mkdir(real, 0o755); err != nil {
				t.Fatal(err)
			}
			alias, ok := spell(t, real)
			if !ok {
				t.Skipf("%s is not another spelling of %s on this volume", name, real)
			}
			aliasFixture(t, m, real)
			if _, err := m.Add(context.Background(), workspace.Spec{Name: "kb", Root: alias}); err != nil {
				t.Fatal(err)
			}
			if w, _ := m.Get("kb"); w.Root() != alias {
				t.Errorf("listed under %q; want the root as configured, %q", w.Root(), alias)
			}
			servedThroughAnAlias(t, m, "kb")
		})
	}
}
