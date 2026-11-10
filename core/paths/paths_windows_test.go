package paths_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/paths"
)

// On Windows a drive's path is absolute in either form, and its slash form keeps the drive; a
// drive's current directory ("C:x") and a bare drive ("C:") are not absolute.
func TestADrivesPathIsAbsoluteInEitherForm(t *testing.T) {
	for in, want := range map[string]bool{`C:\Users\u`: true, "C:/Users/u": true, `\\server\share\kb`: true, "C:x": false, "C:": false, `Users\u`: false} {
		if got := paths.IsAbs(in); got != want {
			t.Errorf("IsAbs(%q) = %v, want %v", in, got, want)
		}
	}
	for in, want := range map[string]string{`C:\Users\u\kb`: "C:/Users/u/kb", `C:\Users\u\kb\`: "C:/Users/u/kb", `C:\Users\.\u\..\v`: "C:/Users/v"} {
		if got := paths.Slash(in); got != want {
			t.Errorf("Slash(%q) = %q, want %q", in, got, want)
		}
	}
}
