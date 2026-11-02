package autodoc

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/kbtype"
)

// The managed files and version.lua, as the repository holds them.
const (
	kbOperationsPath  = "lua/autodoc/kb/templates/KB_OPERATIONS.md"
	kbFrontmatterPath = "lua/autodoc/kb/templates/_schema/frontmatter.yaml"
	kbVersionPath     = "lua/autodoc/kb/version.lua"
)

// TestAManagedFileChangedSinceTheLastReleaseCarriesANewVersion: when KB_OPERATIONS.md or
// _schema/frontmatter.yaml differs from the last release's (the newest v* tag this commit holds),
// version.lua's autodoc is newer than that release's, or a KB stamped with the old version would
// never take the new copy (revision 6 shipped in v0.3.7 while version.lua still said 0.3.6). It
// reads the release through git, so a clone without the tag (a shallow one) skips, saying so; CI's
// fetches every tag.
func TestAManagedFileChangedSinceTheLastReleaseCarriesANewVersion(t *testing.T) {
	tag, err := exec.Command("git", "describe", "--tags", "--abbrev=0", "--match", "v*", "HEAD").Output()
	if err != nil {
		t.Skip("no release tag in this clone (a shallow one): nothing to compare against")
	}
	release := strings.TrimSpace(string(tag))
	at := func(path string) ([]byte, bool) {
		b, err := exec.Command("git", "show", release+":"+path).Output()
		return b, err == nil
	}
	oldVersionLua, ok := at(kbVersionPath)
	if !ok {
		t.Skipf("%s has no %s: the managed version began after it", release, kbVersionPath)
	}
	oldVersion := kbVersionField.FindSubmatch(oldVersionLua)
	if oldVersion == nil {
		t.Fatalf("%s's %s has no autodoc = \"…\"", release, kbVersionPath)
	}
	var changed []string
	for path, now := range map[string][]byte{kbOperationsPath: KBOperations, kbFrontmatterPath: KBFrontmatter} {
		if then, ok := at(path); !ok || !bytes.Equal(then, now) {
			changed = append(changed, path)
		}
	}
	if len(changed) == 0 {
		return
	}
	if newer, ok := kbtype.Older(string(oldVersion[1]), KBManagedVersion()); !ok || !newer {
		t.Fatalf("%v changed since %s, but version.lua's autodoc %q is not newer than its %q: set it to the release that ships them",
			changed, release, KBManagedVersion(), oldVersion[1])
	}
}

// TestTheManagedVersionIsAVersion: version.lua's autodoc is MAJOR.MINOR.PATCH, so every KB can
// compare it.
func TestTheManagedVersionIsAVersion(t *testing.T) {
	if _, ok := kbtype.Older(KBManagedVersion(), "0.0.0"); !ok {
		t.Fatalf("version.lua's autodoc = %q is no MAJOR.MINOR.PATCH: no KB could compare it", KBManagedVersion())
	}
}
