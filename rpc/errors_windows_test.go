//go:build windows

package rpc

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"golang.org/x/sys/windows"
)

// TestWireErrSaysAFileInUse: a write refused because another program holds the file (a sharing or
// a lock violation) is said as such, a file refused, not an internal error, and without its path.
func TestWireErrSaysAFileInUse(t *testing.T) {
	for _, errno := range []windows.Errno{windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION} {
		err := wireErr(fmt.Errorf("save: %w", &fs.PathError{Op: "write", Path: `C:\secret\a.md`, Err: errno}))
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != CodeFileRefused || re.Message != errFileInUse.Message {
			t.Errorf("%v: %v, want the file refused, in use", errno, err)
		} else if strings.Contains(re.Message, "secret") {
			t.Errorf("the message %q carries the path", re.Message)
		}
	}
}
