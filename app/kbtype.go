package app

import (
	"fmt"
	"io"
	"time"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/kbtype"
)

// checkKBType is --check-kbtype: dir ("" the working directory) read as a KB type, as AutoDoc reads
// one it stages, and the files it would create listed; a type that breaks a rule is the error.
// Nothing in dir runs.
func checkKBType(dir string, out io.Writer) error {
	if dir == "" {
		dir = "."
	}
	t, err := kbtype.Read(dir)
	if err != nil {
		return err
	}
	plan := t.Plan(kbtype.Vars{Date: time.Now().Format(time.DateOnly)}, autodoc.KBOperations)
	fmt.Fprintf(out, "%s (%s): %d folders, %d files\n", t.Name, t.Title, len(t.Folders), len(plan))
	for _, f := range t.Folders {
		mark := ""
		if f.Immutable {
			mark = "  (immutable)"
		}
		fmt.Fprintf(out, "  %s/  %s%s\n", f.Path, f.Purpose, mark)
	}
	return nil
}
