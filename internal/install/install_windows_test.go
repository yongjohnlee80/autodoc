//go:build windows

package install_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/install"
)

// The record is where install-autodoc.ps1 writes it, %LOCALAPPDATA%\autodoc; XDG's variable means
// nothing here.
func TestThePathIsUnderLocalAppData(t *testing.T) {
	t.Setenv("LOCALAPPDATA", `C:\Users\u\AppData\Local`)
	t.Setenv("XDG_STATE_HOME", `C:\xdg\state`)
	if p, err := install.Path(); err != nil || p != `C:\Users\u\AppData\Local\autodoc\install.toml` {
		t.Fatalf("Path = %q, %v", p, err)
	}
}
