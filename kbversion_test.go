package autodoc

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/kbtype"
)

// TestTheManagedKBFilesCarryTheirVersion: version.lua's digest is the managed files' as embedded,
// so a change to KB_OPERATIONS.md or _schema/frontmatter.yaml without a new version fails here: a KB
// stamped with the old version would never take the new copy (revision 6 shipped in v0.3.7 while
// version.lua still said 0.3.6).
func TestTheManagedKBFilesCarryTheirVersion(t *testing.T) {
	h := sha256.New()
	h.Write(KBOperations)
	h.Write([]byte{0})
	h.Write(KBFrontmatter)
	if got, want := hex.EncodeToString(h.Sum(nil)), kbManagedDigest(); got != want {
		t.Fatalf("the managed KB files changed since version.lua's autodoc = %q: set autodoc to the release that ships them and digest = %q",
			KBManagedVersion(), got)
	}
	if _, ok := kbtype.Older(KBManagedVersion(), "0.0.0"); !ok {
		t.Fatalf("version.lua's autodoc = %q is no MAJOR.MINOR.PATCH: no KB could compare it", KBManagedVersion())
	}
}
