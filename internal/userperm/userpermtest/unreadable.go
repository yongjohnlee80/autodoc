//go:build linux || darwin || windows

package userpermtest

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/userperm"
)

// unreadable skips the test when this process still opens path through userperm's private read:
// it reads the file whatever its permissions (root on Unix; on Windows, as a backup privilege may
// let userperm's backup-semantics open), so a file it may write but not read can't be made.
func unreadable(t testing.TB, path string) {
	t.Helper()
	if f, err := userperm.OpenPrivate(path); err == nil {
		_ = f.Close()
		t.Skip("this process reads the file whatever its permissions (root on Unix; on Windows, as a backup privilege may let it): no file it can't read can be made")
	}
}
